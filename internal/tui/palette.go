package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"sort"
	"strings"
)

type command struct{ id, label string }

// All commands dispatch into existing model/actions; unsupported integrations
// are omitted. The palette is the seam for future real agent/task commands.
func (m *Model) commands() []command {
	commands := []command{{"refresh", "Refresh workspace"}, {"focus", "Toggle Focus / all repositories"}, {"sort", "Sort attention / name"}, {"all", "Select / deselect visible repositories"}, {"help", "Keyboard help"}}
	row := m.highlightedRow()
	if row == nil {
		return commands
	}
	name := gitcli.SafeText(row.Name)
	commands = append([]command{{"details", "Open " + name + " details"}, {"changes", "Review changes in " + name}, {"shell", "Open terminal here · " + name}}, commands...)
	if m.lazyGitAvailable {
		commands = append(commands, command{"lazygit", "Open LazyGit · " + name})
	}
	if m.actions != nil {
		target := name
		if len(m.selected) > 0 {
			target = fmt.Sprintf("%d selected repositories", len(m.selected))
		}
		commands = append(commands, command{"fetch", "Fetch " + target})
		s := row.Status
		bulk := len(m.selected) > 0
		syncable := s.Error == "" && s.Conflicts == 0 && s.Operation == "" && !s.Detached && !s.Unborn && s.Upstream != ""
		if bulk || (syncable && s.Ahead > 0 && s.Behind == 0) {
			commands = append(commands, command{"push", "Push " + target})
		}
		if bulk || (syncable && s.Behind > 0 && s.Ahead == 0 && !s.Dirty()) {
			commands = append(commands, command{"pull", "Fast-forward pull " + target})
		}
	}
	return commands
}

// Subsequence scoring rewards adjacent matches and word prefixes. Stable ties
// preserve contextual action order, including when the query is empty.
func fuzzyScore(query, label string) (int, bool) {
	q, l := []rune(strings.ToLower(query)), []rune(strings.ToLower(label))
	pos, last, score := 0, -2, 0
	for _, r := range q {
		found := false
		for pos < len(l) {
			i := pos
			pos++
			if l[i] != r {
				continue
			}
			score += 1
			if i == last+1 {
				score += 4
			}
			if i == 0 || l[i-1] == ' ' {
				score += 3
			}
			last = i
			found = true
			break
		}
		if !found {
			return 0, false
		}
	}
	return score, true
}
func (m *Model) paletteCommands() []command {
	type ranked struct {
		command
		score int
	}
	var found []ranked
	for _, c := range m.commands() {
		if score, ok := fuzzyScore(m.paletteQuery, c.label); ok {
			found = append(found, ranked{c, score})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	commands := make([]command, len(found))
	for i, c := range found {
		commands[i] = c.command
	}
	return commands
}
func (m *Model) paletteKey(msg tea.KeyPressMsg) tea.Cmd {
	items := m.paletteCommands()
	switch msg.String() {
	case "esc":
		m.palette = false
	case "up", "ctrl+p":
		m.paletteCursor = max(0, m.paletteCursor-1)
	case "down", "ctrl+n":
		m.paletteCursor = min(max(0, len(items)-1), m.paletteCursor+1)
	case "enter":
		if len(items) > 0 {
			c := items[min(m.paletteCursor, len(items)-1)]
			m.palette = false
			return m.executeCommand(c.id)
		}
	case "backspace":
		r := []rune(m.paletteQuery)
		if len(r) > 0 {
			m.paletteQuery = string(r[:len(r)-1])
		}
		m.paletteCursor = 0
	default:
		if msg.Key().Mod == 0 {
			m.paletteQuery += msg.Key().Text
			m.paletteCursor = 0
		}
	}
	return nil
}
func (m *Model) executeCommand(id string) tea.Cmd {
	switch id {
	case "details", "changes":
		m.details = true
		m.detailTab = 0
		if id == "changes" {
			m.detailTab = 1
		}
		m.detailOffset = 0
	case "shell":
		return m.launchShell()
	case "lazygit":
		return m.launchLazyGit()
	case "refresh":
		return m.refresh()
	case "help":
		m.help = true
		m.helpOffset = 0
	case "all":
		return m.key(keyPress("a"))
	case "sort":
		return m.key(keyPress("s"))
	case "focus":
		path := ""
		if row := m.highlightedRow(); row != nil {
			path = row.Path
		}
		if m.scope == 1 {
			m.scope = 0
		} else {
			m.scope = 1
		}
		m.clearSelection()
		m.highlight, m.scroll = 0, 0
		for i, index := range m.visibleRows() {
			if m.rows[index].Path == path {
				m.highlight = i
				break
			}
		}
	case "fetch", "push", "pull":
		if m.actions == nil {
			return nil
		}
		if len(m.selected) == 0 {
			if row := m.highlightedRow(); row != nil {
				m.selected[row.Path] = true
			}
		}
		m.syncIntent = ""
		if id == "push" {
			m.syncIntent = app.Push
		}
		if id == "pull" {
			m.syncIntent = app.Pull
		}
		return m.preparePreview(app.Fetch)
	}
	return nil
}
func keyPress(text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(text)[0], Text: text} }
func (m *Model) paletteView() tea.View {
	lines := []string{m.style(" Actions", accent, true), " > " + gitcli.SafeText(m.paletteQuery) + "▏", ""}
	items := m.paletteCommands()
	page := max(1, m.height-5)
	start := max(0, m.paletteCursor-page+1)
	for i := start; i < min(len(items), start+page); i++ {
		prefix := "   "
		if i == m.paletteCursor {
			prefix = " > "
		}
		line := prefix + items[i].label
		if i == m.paletteCursor {
			line = m.style(line, accent, true)
		}
		lines = append(lines, line)
	}
	if len(items) == 0 {
		lines = append(lines, " No matching actions")
	}
	lines = fitLines(lines, max(0, m.height-1))
	return m.screen(append(lines, " ↑↓ choose · Enter run · Esc close"))
}
