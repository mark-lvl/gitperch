// Package tui implements the read-only terminal dashboard.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
)

type loader func(context.Context) (app.Snapshot, error)

// Model holds the dashboard state. New supplies asynchronous refresh behavior
// through load so the view remains independent of configuration and Git setup.
type Model struct {
	ctx               context.Context
	load              loader
	noColor           bool
	rows              []app.Row
	warnings          []string
	selected          map[string]bool
	highlight         int
	filter            string
	filtering         bool
	help              bool
	details           bool
	detailOffset      int
	message           string
	loadErr           string
	loading           bool
	width             int
	height            int
	scroll            int
	generation        uint64
	loadCancel        context.CancelFunc
	actions           *app.Actions
	preparing         bool
	running           bool
	preview           *app.Preview
	syncIntent        app.Action
	previewCursor     int
	previewLineOffset int
	actionCancel      context.CancelFunc
	actionCtx         context.Context
	actionGeneration  uint64
	events            chan app.Event
	results           map[string]app.Event
	interrupted       bool
	actionFailed      bool
}

type snapshotMsg struct {
	generation uint64
	snapshot   app.Snapshot
	err        error
}

type childExitedMsg struct{ err error }

// New creates a TUI model. load must honor its context; refreshing cancels an
// older load and ignores any late result from it.
func New(ctx context.Context, load func(context.Context) (app.Snapshot, error), noColor bool) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Model{
		ctx:      ctx,
		load:     load,
		noColor:  noColor,
		selected: make(map[string]bool),
		width:    80,
		height:   24,
	}
}

func (m *Model) Init() tea.Cmd { return m.refresh() }

func (m *Model) refresh() tea.Cmd {
	if m.load == nil {
		m.loadErr = "no dashboard loader configured"
		return nil
	}
	if m.loadCancel != nil {
		m.loadCancel()
	}
	m.generation++
	generation := m.generation
	ctx, cancel := context.WithCancel(m.ctx)
	m.loadCancel = cancel
	m.loading = true
	m.loadErr = ""
	return func() tea.Msg {
		snapshot, err := m.load(ctx)
		return snapshotMsg{generation: generation, snapshot: snapshot, err: err}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.actionMessage(msg); handled {
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.width < 1 {
			m.width = 1
		}
		if m.height < 1 {
			m.height = 1
		}
		m.keepHighlightVisible()
	case snapshotMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		m.applySnapshot(msg.snapshot)
		m.loading = false
		if m.loadCancel != nil {
			m.loadCancel()
			m.loadCancel = nil
		}
		if msg.err != nil {
			m.loadErr = msg.err.Error()
		} else {
			m.loadErr = ""
		}
	case childExitedMsg:
		if msg.err != nil {
			m.message = "Child process: " + msg.err.Error()
		} else {
			m.message = "Child process finished"
		}
		return m, m.refresh()
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *Model) applySnapshot(snapshot app.Snapshot) {
	oldHighlight := ""
	visible := m.visibleRows()
	if m.highlight >= 0 && m.highlight < len(visible) {
		oldHighlight = m.rows[visible[m.highlight]].Path
	}
	oldSelection := m.selected
	m.rows = append([]app.Row(nil), snapshot.Rows...)
	m.warnings = m.warnings[:0]
	for _, warning := range snapshot.Warnings {
		m.warnings = append(m.warnings, gitcli.SafeText(warning.Path+": "+warning.Message))
	}
	m.selected = make(map[string]bool)
	for _, row := range m.rows {
		if oldSelection[row.Path] && matches(row, m.filter) {
			m.selected[row.Path] = true
		}
	}
	indices := m.visibleRows()
	m.highlight = 0
	if oldHighlight != "" {
		for i, index := range indices {
			if m.rows[index].Path == oldHighlight {
				m.highlight = i
				break
			}
		}
	}
	if len(indices) == 0 {
		m.scroll = 0
	} else {
		m.keepHighlightVisible()
	}
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.preparing || m.running {
		if key == "q" || key == "ctrl+c" || key == "esc" {
			if m.actionCancel != nil {
				m.actionCancel()
			}
			m.message = "Cancellation requested; waiting for outcomes"
		}
		return nil
	}
	if m.preview != nil {
		return m.previewKey(key)
	}
	if key == "ctrl+c" {
		m.interrupted = true
		if m.loadCancel != nil {
			m.loadCancel()
			m.loadCancel = nil
		}
		return tea.Quit
	}
	if m.details {
		switch key {
		case "q":
			return tea.Quit
		case "esc", "d":
			m.details = false
		case "j", "down":
			m.detailOffset++
		case "k", "up":
			if m.detailOffset > 0 {
				m.detailOffset--
			}
		}
		return nil
	}
	if m.filtering {
		switch key {
		case "enter":
			m.filtering = false
		case "esc":
			m.filtering = false
		case "backspace":
			text := []rune(m.filter)
			if len(text) > 0 {
				m.filter = string(text[:len(text)-1])
				m.clearSelection()
				m.highlight, m.scroll = 0, 0
			}
		default:
			text := msg.Key().Text
			if text != "" && msg.Key().Mod == 0 {
				m.filter += text
				m.clearSelection()
				m.highlight, m.scroll = 0, 0
			}
		}
		return nil
	}
	if m.help && key != "?" && key != "esc" && key != "q" {
		return nil
	}
	indices := m.visibleRows()
	switch key {
	case "q":
		if m.loadCancel != nil {
			m.loadCancel()
			m.loadCancel = nil
		}
		return tea.Quit
	case "?":
		m.help = !m.help
	case "d":
		m.details = true
		m.detailOffset = 0
	case "esc":
		if m.help {
			m.help = false
		} else if m.filter != "" {
			m.filter = ""
			m.clearSelection()
			m.highlight, m.scroll = 0, 0
		} else {
			m.results = nil
			m.message = ""
		}
	case "/":
		m.filtering = true
		m.filter = ""
		m.clearSelection()
		m.highlight, m.scroll = 0, 0
	case "r":
		m.message = "Refreshing repository status"
		return m.refresh()
	case "j", "down":
		if m.highlight+1 < len(indices) {
			m.highlight++
			m.keepHighlightVisible()
		}
	case "k", "up":
		if m.highlight > 0 {
			m.highlight--
			m.keepHighlightVisible()
		}
	case " ", "space":
		if row := m.highlightedRow(); row != nil {
			m.selected[row.Path] = !m.selected[row.Path]
			if !m.selected[row.Path] {
				delete(m.selected, row.Path)
			}
		}
	case "a":
		allSelected := len(indices) > 0
		for _, index := range indices {
			if !m.selected[m.rows[index].Path] {
				allSelected = false
				break
			}
		}
		if allSelected {
			m.clearSelection()
		} else {
			m.clearSelection()
			for _, index := range indices {
				m.selected[m.rows[index].Path] = true
			}
		}
	case "f", "p", "l":
		if m.actions == nil {
			m.message = "This action is not available yet"
			return nil
		}
		m.syncIntent = ""
		if key == "p" {
			m.syncIntent = app.Push
		} else if key == "l" {
			m.syncIntent = app.Pull
		}
		return m.preparePreview(app.Fetch)
	case "enter":
		return m.launchShell()
	case "g":
		return m.launchLazyGit()
	}
	return nil
}

// ExitCode reports command interruption or partial/operation failure after the
// dashboard closes. Dismissing displayed results does not erase a failed batch.
func (m *Model) ExitCode() int {
	if m.interrupted {
		return 130
	}
	if m.actionFailed || m.loadErr != "" || len(m.warnings) > 0 {
		return 1
	}
	for _, row := range m.rows {
		if row.Status.Error != "" {
			return 1
		}
	}
	return 0
}

func (m *Model) launchShell() tea.Cmd {
	row := m.highlightedRow()
	if row == nil {
		m.message = "Select a repository first"
		return nil
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-i")
	cmd.Dir = row.Path
	cmd.Env = gitcli.ChildEnvironment(os.Environ())
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return childExitedMsg{err: err} })
}

func (m *Model) launchLazyGit() tea.Cmd {
	row := m.highlightedRow()
	if row == nil {
		m.message = "Select a repository first"
		return nil
	}
	path, err := exec.LookPath("lazygit")
	if err != nil {
		m.message = "LazyGit is not installed or not on PATH"
		return nil
	}
	cmd := exec.Command(path)
	cmd.Dir = row.Path
	cmd.Env = gitcli.ChildEnvironment(os.Environ())
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return childExitedMsg{err: err} })
}

func (m *Model) clearSelection() { m.selected = make(map[string]bool) }

func (m *Model) highlightedRow() *app.Row {
	indices := m.visibleRows()
	if m.highlight < 0 || m.highlight >= len(indices) {
		return nil
	}
	return &m.rows[indices[m.highlight]]
}

func (m *Model) visibleRows() []int {
	indices := make([]int, 0, len(m.rows))
	for i, row := range m.rows {
		if matches(row, m.filter) {
			indices = append(indices, i)
		}
	}
	return indices
}

func matches(row app.Row, filter string) bool {
	filter = strings.ToLower(filter)
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(row.Name), filter) ||
		strings.Contains(strings.ToLower(row.Path), filter) ||
		strings.Contains(strings.ToLower(row.Status.Branch), filter)
}

func (m *Model) keepHighlightVisible() {
	indices := m.visibleRows()
	if m.highlight < 0 {
		m.highlight = 0
	}
	if m.highlight >= len(indices) && len(indices) > 0 {
		m.highlight = len(indices) - 1
	}
	page := m.pageSize()
	if m.highlight < m.scroll {
		m.scroll = m.highlight
	} else if m.highlight >= m.scroll+page {
		m.scroll = m.highlight - page + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *Model) pageSize() int {
	if m.height <= 0 {
		return 1
	}
	if m.height-9 < 1 {
		return 1
	}
	return m.height - 9
}

func (m *Model) View() tea.View {
	if m.preview != nil {
		return m.previewView()
	}
	if m.details {
		return m.detailsView()
	}
	width := m.width
	if width < 1 {
		width = 1
	}
	header := "repodash · ahead/behind: locally known refs"
	if m.actions == nil {
		header = "repodash · read-only · ahead/behind: locally known refs"
	}
	lines := []string{m.style(header, "#7DDA58", true)}
	if m.filtering {
		lines = append(lines, "Filter: "+gitcli.SafeText(m.filter)+"▏")
	} else if m.filter != "" {
		lines = append(lines, "Filter: "+gitcli.SafeText(m.filter)+" ( / edit )")
	} else {
		lines = append(lines, "Repositories")
	}
	if m.loading {
		lines[1] += "  · refreshing"
	}
	if m.preparing {
		lines[1] += " · preparing preview"
	}
	if m.running {
		lines[1] += " · batch running (q cancels)"
	}
	var detail []string
	if m.loadErr != "" {
		detail = append(detail, "Load error: "+gitcli.SafeText(m.loadErr))
	}
	if m.help {
		detail = append(detail,
			"j/k or arrows move · Space select · a select all visible · / filter",
			"r refresh · d details · Enter shell · g LazyGit · f fetch · p push · l FF pull · q quit/cancel",
		)
	} else {
		indices := m.visibleRows()
		if len(indices) == 0 {
			if m.filter != "" {
				detail = append(detail, "No repositories match this filter")
			} else {
				detail = append(detail, "No repositories discovered")
			}
		}
		if row := m.highlightedRow(); row != nil {
			detail = append(detail, "Path: "+gitcli.SafeText(row.Path))
			detail = append(detail, m.detailLine(*row))
			if row.Status.Error != "" {
				detail = append(detail, "Error: "+gitcli.SafeText(row.Status.Error))
			}
		}
		if len(m.warnings) > 0 {
			detail = append(detail, fmt.Sprintf("Warnings: %d · %s", len(m.warnings), m.warnings[0]))
		}
	}
	if m.message != "" {
		detail = append(detail, gitcli.SafeText(m.message))
	}
	wrappedDetail := make([]string, 0, len(detail))
	for _, line := range detail {
		wrappedDetail = append(wrappedDetail, strings.Split(ansi.Wrap(line, width, "/"), "\n")...)
	}
	footer := fmt.Sprintf("Selected: %d  ·  d details  ·  ? help  ·  q quit", len(m.selected))
	reserved := len(lines) + len(wrappedDetail) + 1
	if reserved > m.height {
		reserved = len(lines) + 1
		if reserved > m.height {
			reserved = m.height
		}
		wrappedDetail = nil
	}
	rowSlots := m.height - reserved
	if rowSlots < 0 {
		rowSlots = 0
	}
	indices := m.visibleRows()
	if !m.help && len(indices) > 0 {
		if rowSlots == 0 {
			rowSlots = 1
		}
		if m.highlight < m.scroll {
			m.scroll = m.highlight
		} else if m.highlight >= m.scroll+rowSlots {
			m.scroll = m.highlight - rowSlots + 1
		}
		end := m.scroll + rowSlots
		if end > len(indices) {
			end = len(indices)
		}
		for pos := m.scroll; pos < end; pos++ {
			idx := indices[pos]
			line := m.rowLine(m.rows[idx], pos == m.highlight)
			if pos == m.highlight {
				line = m.style(line, "#84D8FF", true)
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, wrappedDetail...)
	lines = append(lines, m.style(footer, "#AAAAAA", false))
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	if len(lines) > m.height && m.height > 0 {
		lines = append(lines[:1], lines[len(lines)-m.height+1:]...)
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

func (m *Model) rowLine(row app.Row, highlighted bool) string {
	status := row.Status
	branch := status.Branch
	if status.Detached {
		branch = "(detached)"
	}
	if status.Unborn {
		branch += " (unborn)"
	}
	upstream := status.Upstream
	if upstream == "" {
		upstream = "none"
	}
	comparison := "ahead:? behind:?"
	if status.ComparisonKnown {
		comparison = fmt.Sprintf("ahead:%d behind:%d", status.Ahead, status.Behind)
	}
	markers := make([]string, 0, 5)
	if status.Dirty() {
		markers = append(markers, "dirty")
	}
	if status.Conflicts > 0 {
		markers = append(markers, "conflicts")
	}
	if status.Detached {
		markers = append(markers, "detached")
	}
	if status.Upstream == "" {
		markers = append(markers, "no-upstream")
	}
	if status.Error != "" {
		markers = append(markers, "error")
	}
	if status.Synchronized() {
		markers = append(markers, "synchronized")
	}
	selected := "[ ]"
	if m.selected[row.Path] {
		selected = "[x]"
	}
	pointer := " "
	if highlighted {
		pointer = ">"
	}
	state := status.Operation
	if state == "" {
		state = "idle"
	}
	if result, ok := m.results[row.Path]; ok {
		state = string(result.State)
	}
	line := fmt.Sprintf("%s %s %s %s | %s | %s | c:%d u:%d x:%d | %s | %s",
		pointer, selected, gitcli.SafeText(state), gitcli.SafeText(row.Name), gitcli.SafeText(row.Path), gitcli.SafeText(branch), status.Changes,
		status.Untracked, status.Conflicts, comparison, gitcli.SafeText(strings.Join(markers, ",")))
	return line
}

func (m *Model) detailsView() tea.View {
	width, height := max(1, m.width), max(1, m.height)
	content := []string{}
	if row := m.highlightedRow(); row != nil {
		content = append(content, "Path: "+gitcli.SafeText(row.Path), m.detailLine(*row))
		if row.Status.Error != "" {
			content = append(content, "Error: "+gitcli.SafeText(row.Status.Error))
		}
	}
	if m.loadErr != "" {
		content = append(content, "Load error: "+gitcli.SafeText(m.loadErr))
	}
	content = append(content, m.warnings...)
	for _, row := range m.rows {
		if result, ok := m.results[row.Path]; ok {
			content = append(content, gitcli.SafeText(result.Path)+": "+string(result.State)+" · "+gitcli.SafeText(result.Message))
		}
	}
	if len(content) == 0 {
		content = append(content, "No repository details or warnings")
	}
	var wrapped []string
	for _, line := range content {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(line, width, "/"), "\n")...)
	}
	page := max(1, height-2)
	offset := min(m.detailOffset, max(0, len(wrapped)-page))
	lines := []string{ansi.Truncate("Details · j/k scroll · Esc back", width, "")}
	for _, line := range wrapped[offset:min(len(wrapped), offset+page)] {
		lines = append(lines, ansi.Truncate(line, width, ""))
	}
	lines = append(lines, ansi.Truncate(fmt.Sprintf("Line %d/%d · q quit", offset+1, len(wrapped)), width, ""))
	if len(lines) > height {
		lines = lines[:height]
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

func (m *Model) detailLine(row app.Row) string {
	status := row.Status
	branch := status.Branch
	if status.Detached {
		branch = "(detached)"
	}
	if status.Unborn {
		branch += " (unborn)"
	}
	upstream := status.Upstream
	if upstream == "" {
		upstream = "none"
	}
	ahead, behind := "unknown", "unknown"
	if status.ComparisonKnown {
		ahead, behind = fmt.Sprint(status.Ahead), fmt.Sprint(status.Behind)
	}
	comparisonLabel := "locally known refs"
	operation := status.Operation
	if operation == "" {
		operation = "idle"
	}
	line := fmt.Sprintf("Branch: %s · changes:%d untracked:%d conflicts:%d · ahead:%s behind:%s (%s) · upstream:%s · operation:%s",
		gitcli.SafeText(branch), status.Changes, status.Untracked, status.Conflicts, ahead, behind, comparisonLabel, gitcli.SafeText(upstream), gitcli.SafeText(operation))
	if !row.LastFetch.IsZero() {
		line += " · last fetch:" + row.LastFetch.Format("15:04:05")
	}
	if result, ok := m.results[row.Path]; ok {
		line += " · " + string(result.State) + ": " + gitcli.SafeText(result.Message)
	}
	return line
}

func (m *Model) style(text, color string, bold bool) string {
	if m.noColor {
		return text
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(bold)
	return style.Render(text)
}
