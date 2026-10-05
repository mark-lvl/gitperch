package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"sort"
	"strconv"
	"strings"
)

type command struct{ id, label string }

// All commands dispatch into existing model/actions; unsupported integrations
// are omitted. The palette is the seam for future real agent/task commands.
func (m *Model) commands() []command {
	commands := []command{{"refresh", "Refresh workspace"}, {"focus", "Toggle Focus / all repositories"}, {"sort", "Sort attention / name"}, {"all", "Select / deselect visible repositories"}, {"help", "Keyboard help"}}
	for i := 2; i < len(scopes); i++ {
		commands = append(commands, command{fmt.Sprintf("scope:%d", i), "Show " + strings.ToLower(scopes[i]) + " repositories"})
	}
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
		syncable := s.Error == "" && s.Conflicts == 0 && s.Operation == "" && !s.Detached && !s.Unborn && s.Upstream != "" && s.ComparisonKnown
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
	m.paletteCursor = min(m.paletteCursor, max(0, len(items)-1))
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
	if strings.HasPrefix(id, "scope:") {
		scope, err := strconv.Atoi(strings.TrimPrefix(id, "scope:"))
		if err == nil && scope >= 0 && scope < len(scopes) {
			m.setScope(scope)
		}
		return nil
	}
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
		m.details = false
		m.help = false
		return m.key(keyPress("a"))
	case "sort":
		m.details = false
		m.help = false
		return m.key(keyPress("s"))
	case "focus":
		scope := 1
		if m.scope != 0 {
			scope = 0
		}
		m.setScope(scope)
	case "fetch", "push", "pull":
		if m.actions == nil {
			return nil
		}
		// Target the highlighted row without selecting it, so it cannot leak
		// into later actions after a cancelled or failed plan.
		paths := m.selectedPaths()
		if len(paths) == 0 {
			if row := m.highlightedRow(); row != nil {
				paths = []string{row.Path}
			}
		}
		m.syncIntent = ""
		if id == "push" {
			m.syncIntent = app.Push
		}
		if id == "pull" {
			m.syncIntent = app.Pull
		}
		return m.preparePreview(app.Fetch, paths)
	}
	return nil
}
func keyPress(text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(text)[0], Text: text} }

// Palette floats above the current workspace, retaining repository orientation.
func (m *Model) paletteView() tea.View {
	if m.width < 12 || m.height < 6 {
		return m.screen([]string{"Actions", "> " + gitcli.SafeText(m.paletteQuery), "Esc close"})
	}
	w := min(68, m.width-4)
	inner := w - 4
	items := m.paletteCommands()
	cursor := min(m.paletteCursor, max(0, len(items)-1))
	page := max(1, min(7, (m.height-10)/2))
	start := max(0, cursor-page+1)
	title := m.between(m.style("Actions", accent, true), m.style("Esc close", muted, false), inner)
	query := "Search commands…"
	if m.paletteQuery != "" {
		query = gitcli.SafeText(m.paletteQuery)
	}
	lines := []string{title, "", m.style("> "+cell(query+"▏", max(1, inner-2)), accent, false), m.rule(inner)}
	for i := start; i < min(len(items), start+page); i++ {
		pointer := " "
		if i == cursor {
			pointer = ">"
		}
		line := m.style(pointer+" ", accent, true) + m.style(cell(items[i].label, inner-2), ink, i == cursor)
		description := "  " + m.commandHint(items[i].id)
		if i == cursor && !m.noColor {
			line = backgroundText(line, selection)
			description = backgroundText(cell(m.style(description, muted, false), inner), selection)
		} else {
			description = m.style(description, muted, false)
		}
		lines = append(lines, line, description)
	}
	if len(items) == 0 {
		lines = append(lines, m.style("No matching actions", muted, false))
	}
	lines = append(lines, m.rule(inner), m.style("↑↓ choose   Enter run", muted, false))
	panel := m.frame(lines, w)
	if len(panel) > m.height {
		panel = panel[:m.height]
	}
	base := m.workspaceLines()
	if m.details {
		base = strings.Split(m.detailsView().Content, "\n")
	}
	base = fitLines(base, m.height)
	x, y := (m.width-w)/2, max(0, (min(m.height, len(m.workspaceLines()))-len(panel))/2)
	for i, line := range panel {
		if !m.noColor {
			line = backgroundText(line, surface)
		}
		if y+i >= m.height {
			break
		}
		under := cell(base[y+i], m.width)
		base[y+i] = ansi.Cut(under, 0, x) + cell(line, w) + ansi.Cut(under, x+w, m.width)
	}
	return m.screen(base)
}
func (m *Model) commandHint(id string) string {
	switch id {
	case "details":
		return "Overview, working tree and recent commits"
	case "changes":
		return "Changed files and tracked patch"
	case "shell":
		return "Interactive shell in the highlighted repository"
	case "lazygit":
		return "Open the installed Git interface"
	case "fetch":
		return "Review targets before contacting remotes"
	case "push":
		return "Review fetch scope, then confirm exact commits"
	case "pull":
		return "Review and confirm fast-forward integration"
	case "focus":
		return "Switch between all repos and needs attention"
	case "sort":
		return "Change order while retaining the highlighted repo"
	case "all":
		return "Toggle bulk selection of visible repositories"
	case "refresh":
		return "Reload local Git status and repository context"
	case "help":
		return "Navigation, repository and bulk operations"
	default:
		return "Filter the workspace; clear bulk selection"
	}
}
func (m *Model) setScope(scope int) {
	path := ""
	if row := m.highlightedRow(); row != nil {
		path = row.Path
	}
	m.details = false
	m.help = false
	m.scope = scope
	m.clearSelection()
	m.highlight, m.scroll = 0, 0
	for i, index := range m.visibleRows() {
		if m.rows[index].Path == path {
			m.highlight = i
			break
		}
	}
	m.keepHighlightVisible()
}
