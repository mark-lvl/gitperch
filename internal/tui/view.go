package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
)

var scopes = []string{"All", "Attention", "Changed", "Ahead", "Behind", "Issues"}

// Attention is deliberately broader than dirty: unknown tracking state and
// interrupted operations need review even when the worktree is clean.
func attentionRank(row app.Row) int {
	s := row.Status
	switch {
	case s.Error != "":
		return 6
	case s.Conflicts > 0 || s.Operation != "":
		return 5
	case s.ComparisonKnown && s.Ahead > 0 && s.Behind > 0:
		return 4
	case s.Dirty():
		return 3
	case s.Detached || s.Unborn || s.Upstream == "" || !s.ComparisonKnown:
		return 2
	case s.Ahead > 0 || s.Behind > 0:
		return 1
	default:
		return 0
	}
}

func (m *Model) attentionRank(row app.Row) int {
	if result, ok := m.results[row.Path]; ok && (result.State == app.Failed || result.State == app.OutcomeUnknown) {
		return 7
	}
	return attentionRank(row)
}

func (m *Model) inScope(row app.Row) bool {
	s := row.Status
	switch m.scope {
	case 1:
		return m.attentionRank(row) > 0
	case 2:
		return s.Dirty()
	case 3:
		return s.ComparisonKnown && s.Ahead > 0
	case 4:
		return s.ComparisonKnown && s.Behind > 0
	case 5:
		return hasIssue(row)
	default:
		return true
	}
}

func hasIssue(row app.Row) bool {
	s := row.Status
	return s.Error != "" || s.Conflicts > 0 || s.Operation != "" || s.Detached || s.Unborn || s.Upstream == "" || !s.ComparisonKnown
}

type dashboardLayout struct {
	split                                                     bool
	listWidth, inspectorWidth, body, slots, rowHeight, bottom int
}

// One shared layout controls both rendering and keyboard pagination.
func (m *Model) layout() dashboardLayout {
	w, h := max(1, m.width), max(1, m.height)
	l := dashboardLayout{listWidth: w, rowHeight: 1, bottom: 1}
	// Header, view/search line, table title, and footer have fixed reservations.
	if h >= 20 {
		l.bottom += min(7, max(4, h/4))
	}
	if m.notice() != "" {
		l.bottom++
	}
	l.body = max(1, h-3-l.bottom)
	l.slots = max(1, l.body-2)
	l.split = w >= 120 && h >= 20
	if l.split {
		l.inspectorWidth = min(34, w/4)
		l.listWidth = w - l.inspectorWidth - 3
	}
	return l
}

func (m *Model) View() tea.View {
	if m.preview != nil {
		return m.previewView()
	}
	if m.palette {
		return m.paletteView()
	}
	if m.help {
		return m.helpView()
	}
	if m.details {
		return m.detailsView()
	}
	if m.width < 60 || m.height < 12 {
		return m.screen([]string{"Terminal too small", "Minimum recommended size: 60x12", "Resize terminal · q quit"})
	}
	l := m.layout()
	lines := []string{m.summaryLine(), m.searchLine(), ""}
	list := m.repositoryList(l)
	if l.split {
		attention := m.attentionPanel(l.inspectorWidth, l.body)
		for i := 0; i < l.body; i++ {
			lines = append(lines, cell(list[i], l.listWidth)+m.style(" │ ", border, false)+cell(attention[i], l.inspectorWidth))
		}
	} else {
		lines = append(lines, list...)
	}
	previewHeight := l.bottom - 1
	if m.notice() != "" {
		previewHeight--
	}
	if previewHeight > 0 {
		lines = append(lines, fitLines(m.compactInspector(), previewHeight)...)
	}
	if notice := m.notice(); notice != "" {
		lines = append(lines, m.style(" "+notice, amber, false))
	}
	lines = append(lines, m.footer())
	return m.screen(lines)
}

func (m *Model) summaryLine() string {
	attention := 0
	for _, row := range m.rows {
		if m.attentionRank(row) > 0 {
			attention++
		}
	}
	location := m.workspace
	if location == "" && m.highlightedRow() != nil {
		location = gitcli.SafeText(m.highlightedRow().Path)
	}
	summary := fmt.Sprintf("%d repos · ! %d attention", len(m.rows), attention)
	if m.loading {
		summary += " · loading"
	}
	if m.preparing {
		summary += " · review"
	}
	if m.running {
		summary += " · " + m.progressLabel()
	}
	locationWidth := m.width - ansi.StringWidth(summary) - ansi.StringWidth(" repodash   ") - 2
	left := m.style(" repodash", accent, true)
	if locationWidth > 4 {
		left += "  " + m.style(truncatePath(location, locationWidth), muted, false)
	}
	return m.between(left, summary+" ", m.width)

}

func (m *Model) attentionPanel(w, h int) []string {
	lines := []string{m.style(" ATTENTION", amber, true), ""}
	count := 0
	indices := make([]int, len(m.rows))
	for i := range m.rows {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool { return m.attentionRank(m.rows[indices[i]]) > m.attentionRank(m.rows[indices[j]]) })
	for _, i := range indices {
		row := m.rows[i]
		if m.attentionRank(row) == 0 {
			continue
		}
		label, color := m.primaryStatus(row)
		lines = append(lines, " "+m.style(cell(gitcli.SafeText(row.Name), w-2), color, false), " "+m.style(cell(label, w-2), muted, false), "")
		count++
		if count == 3 {
			break
		}
	}
	if count == 0 {
		lines = append(lines, " No attention needed.")
	}
	lines = append(lines, m.style(" Tab Focus / All", muted, false))
	return fitLines(lines, h)
}

func (m *Model) searchLine() string {
	query := ""
	if m.filtering {
		query = " / " + gitcli.SafeText(m.filter) + "▏"
	} else if m.filter != "" {
		query = " / " + gitcli.SafeText(m.filter) + " · Esc clear"
	}
	label := scopes[m.scope]
	if m.scope == 1 {
		label = "Focus"
	}
	hidden := ""
	if m.scope == 1 {
		hidden = fmt.Sprintf(" · %d healthy hidden", len(m.rows)-len(m.visibleRows()))
	}
	return m.between(" "+m.style(label, accent, true)+hidden+query, m.style("/ search · : actions · ? help ", muted, false), m.width)
}

func (m *Model) repositoryList(l dashboardLayout) []string {
	indices := m.visibleRows()
	header := fmt.Sprintf(" REPOSITORIES  %d/%d", len(indices), len(m.rows))
	if len(indices) > 0 {
		header += fmt.Sprintf("  ·  %d–%d", m.scroll+1, min(len(indices), m.scroll+l.slots))
	}
	// View is side-effect free; Update owns scroll position.
	lines := []string{m.style(header, muted, true), m.tableHeader(l.listWidth)}
	if len(indices) == 0 {
		title, hint := "No repositories discovered", "Check workspace paths; press r to scan again."
		if m.loading {
			title, hint = "Scanning your workspace…", "Repository status will appear here."
		} else if m.filter != "" {
			title, hint = "No repositories match this filter", "Press Esc to clear your search."
		} else if m.scope != 0 {
			title, hint = "No repositories in this view", "Press Tab to explore another view."
		}
		lines = append(lines, "", " "+m.style(title, ink, true), " "+hint)
	}
	end := min(len(indices), m.scroll+l.slots)
	for pos := m.scroll; pos < end; pos++ {
		row := m.rows[indices[pos]]
		line := m.tableRow(row, pos == m.highlight, l.listWidth)
		lines = append(lines, line)
		if l.rowHeight == 2 {
			path := "      " + gitcli.SafeText(row.Path)
			lines = append(lines, m.style(cell(path, l.listWidth), muted, false))
		}
	}
	return fitLines(lines, l.body)
}

// Columns share their geometry with row rendering. Medium/narrow widths move
// branch context into the preview rather than crushing every column.
func columnWidths(w int) (int, int, int) {
	if w < 120 {
		return max(8, w-35), 0, 18
	}
	available := w - 9
	return available * 32 / 100, available * 25 / 100, available * 23 / 100
}
func (m *Model) tableHeader(w int) string {
	name, branch, status := columnWidths(w)
	if w >= 90 {
		branch = max(18, w/4)
		name = max(10, w-branch-status-21)
	}
	header := "      " + cell("REPO", name) + " "
	if branch > 0 {
		header += cell("BRANCH", branch) + " "
	}
	return m.style(header+cell("STATUS", status)+" SYNC", muted, false)
}
func (m *Model) primaryStatus(row app.Row) (string, string) {
	s := row.Status
	icons := m.symbols()
	switch {
	case s.Error != "":
		return icons.failed + " failed", danger
	case s.Conflicts > 0:
		return "! conflict", danger
	case s.Operation != "":
		return "! " + gitcli.SafeText(s.Operation), amber
	case s.Dirty():
		return icons.changed + " changed", amber
	case s.ComparisonKnown && s.Ahead > 0 && s.Behind > 0:
		return "! diverged", danger
	case s.Detached:
		return "! detached", amber
	case s.Unborn:
		return "! no commits", amber
	case s.Upstream == "":
		return "! no upstream", amber
	case !s.ComparisonKnown:
		return "! unknown refs", amber
	case s.Ahead > 0:
		return icons.ahead + " unpublished", accent
	case s.Behind > 0:
		return icons.behind + " behind", amber
	default:
		return icons.clean + " clean", muted
	}
}
func (m *Model) tableRow(row app.Row, highlighted bool, w int) string {
	pointer, checkbox := " ", "[ ]"
	if highlighted {
		pointer = m.symbols().pointer
	}
	if m.selected[row.Path] {
		checkbox = "[x]"
	}
	name, branch, status := columnWidths(w)
	if w >= 90 {
		branch = max(18, w/4)
		name = max(10, w-branch-status-21)
	}
	label, color := m.primaryStatus(row)
	if result, ok := m.results[row.Path]; ok {
		label = string(result.State)
		color = resultColor(result.State)
	}
	sync := "—"
	if row.Status.ComparisonKnown && (row.Status.Ahead > 0 || row.Status.Behind > 0) {
		sync = fmt.Sprintf("%s%d %s%d", m.symbols().ahead, row.Status.Ahead, m.symbols().behind, row.Status.Behind)
	}
	if m.iconMode == "ascii" && sync == "—" {
		sync = "-"
	}
	line := pointer + " " + checkbox + " " + cell(gitcli.SafeText(row.Name), name) + " "
	if branch > 0 {
		line += m.style(cell(truncateMiddle(branchLabel(row), branch), branch), muted, false) + " "
	}
	line += m.style(cell(label, status), color, false) + " " + m.style(sync, muted, false)
	if highlighted && !m.noColor {
		return lipgloss.NewStyle().Background(lipgloss.Color(selection)).Foreground(lipgloss.Color(ink)).Bold(true).Render(cell(line, w))
	}
	return cell(line, w)
}

func branchLabel(row app.Row) string {
	if row.Status.Detached {
		return "(detached)"
	}
	if row.Status.Unborn {
		return gitcli.SafeText(row.Status.Branch) + " (unborn)"
	}
	return gitcli.SafeText(row.Status.Branch)
}

func worktreeLabel(row app.Row) (string, string) {
	s := row.Status
	switch {
	case s.Error != "":
		return "error", danger
	case s.Conflicts > 0:
		return fmt.Sprintf("%d conflicts", s.Conflicts), danger
	case s.Operation != "":
		return gitcli.SafeText(s.Operation), amber
	case s.Dirty():
		var parts []string
		if s.Changes > 0 {
			parts = append(parts, fmt.Sprintf("%d changed", s.Changes))
		}
		if s.Untracked > 0 {
			parts = append(parts, fmt.Sprintf("%d new", s.Untracked))
		}
		return strings.Join(parts, " + "), amber
	default:
		return "clean", muted
	}
}

func syncLabel(row app.Row) (string, string) {
	s := row.Status
	switch {
	case s.Error != "":
		return "unknown", muted
	case s.Detached:
		return "detached", amber
	case s.Unborn:
		return "no commits", amber
	case s.Upstream == "":
		return "no upstream", amber
	case !s.ComparisonKnown:
		return "unknown", amber
	case s.Ahead > 0 && s.Behind > 0:
		return fmt.Sprintf("↑%d ↓%d", s.Ahead, s.Behind), danger
	case s.Ahead > 0:
		return fmt.Sprintf("↑%d ahead", s.Ahead), accent
	case s.Behind > 0:
		return fmt.Sprintf("↓%d behind", s.Behind), amber
	default:
		return "up to date", accent
	}
}

func nextStep(row app.Row) string {
	s := row.Status
	switch {
	case s.Error != "":
		return "Status unavailable. Open diagnostics with d; refresh with r after resolving the error."
	case s.Conflicts > 0:
		return "Resolve conflicts in your shell or LazyGit before syncing."
	case s.Operation != "":
		return "Finish the Git operation in your shell or LazyGit before syncing."
	case s.Detached:
		return "HEAD is detached. Check out a branch in your shell or LazyGit to sync."
	case s.Unborn:
		return "Create the first commit and configure an upstream in your shell."
	case s.Upstream == "":
		return "Configure an upstream in your shell to compare and sync this branch."
	case !s.ComparisonKnown:
		return "Tracking comparison is unknown. Select with Space, then f to review a fetch."
	case s.Ahead > 0 && s.Behind > 0:
		return "Histories have diverged. Review and reconcile in your shell or LazyGit."
	case s.Behind > 0 && s.Dirty():
		return "Commit or stash local changes in your shell before a fast-forward pull."
	case s.Behind > 0:
		return "Press l to review a fast-forward pull."
	case s.Ahead > 0:
		return "Press p to review a push. Only committed changes are included."
	case s.Dirty():
		return "Review local changes in your shell or LazyGit. Tracking refs are up to date."
	default:
		return "Worktree is clean and locally known tracking refs are up to date. Select and fetch to check the remote."
	}
}

func (m *Model) guidance(row app.Row) string {
	if result, ok := m.results[row.Path]; ok && (result.State == app.Failed || result.State == app.OutcomeUnknown) {
		text := strings.ToLower(result.Message)
		if strings.Contains(text, "non-fast-forward") || strings.Contains(text, "fetch first") || strings.Contains(text, "diverged") {
			return "Remote history needs review. Open details for the Git error; reconcile in your shell before retrying."
		}
		if strings.Contains(text, "permission denied") || strings.Contains(text, "authentication") {
			return "Check remote authentication in your shell. Open details for the Git error, then retry explicitly."
		}
		return "Operation failed or outcome uncertain. Open details for the Git error; refresh before retrying."
	}

	if m.actions == nil && row.Status.Error == "" && row.Status.Upstream != "" && !row.Status.Detached && !row.Status.Unborn && row.Status.Conflicts == 0 && row.Status.Operation == "" {
		return "Read-only dashboard. Open your shell or LazyGit to review changes and synchronize. Counts use locally known refs."
	}
	return nextStep(row)
}

func (m *Model) compactInspector() []string {
	lines := []string{m.rule(m.width)}
	if row := m.highlightedRow(); row != nil {
		context := branchLabel(*row)
		if row.Status.Upstream != "" {
			context += " → " + gitcli.SafeText(row.Status.Upstream)
		}
		lines = append(lines, " "+m.style(gitcli.SafeText(row.Name), accent, true)+" · "+truncateMiddle(context, max(10, m.width-ansi.StringWidth(row.Name)-5)))
		lines = append(lines, m.style(" "+truncatePath(gitcli.SafeText(row.Path), m.width-2), muted, false))
		lines = append(lines, " "+cell(m.guidance(*row), m.width-2))
		lines = append(lines, m.previewLines(*row)...)
	} else {
		lines = append(lines, " Highlight a repository to see its details.")
	}
	return lines
}

func (m *Model) notice() string {
	if m.loadErr != "" {
		return "Load error: " + gitcli.SafeText(m.loadErr) + " · d details"
	}
	if m.message != "" {
		return gitcli.SafeText(m.message)
	}
	if len(m.warnings) > 0 {
		return fmt.Sprintf("%d workspace warnings · d to review", len(m.warnings))
	}
	return ""
}

func (m *Model) progressLabel() string {
	done := 0
	for _, event := range m.results {
		if event.State != app.Queued && event.State != app.Running {
			done++
		}
	}
	return fmt.Sprintf("Batch running · %d/%d finished", done, len(m.selected))
}

func resultColor(state app.State) string {
	switch state {
	case app.Failed, app.OutcomeUnknown:
		return danger
	case app.Succeeded:
		return accent
	default:
		return amber
	}
}

func (m *Model) footer() string {
	if m.preparing || m.running {
		return " Esc cancel operation · waiting for outcomes"
	}
	if m.filtering {
		return " ↑↓ choose · Enter open · Esc clear"
	}
	if len(m.selected) > 0 && m.actions != nil {
		return fmt.Sprintf(" %d selected · f Fetch · p Push · l Pull · : Actions", len(m.selected))
	}
	footer := " Enter Open · d Changes · o Shell"
	if row := m.highlightedRow(); row != nil && m.actions != nil {
		s := row.Status
		if s.Error == "" && s.Operation == "" && s.Conflicts == 0 && !s.Detached && !s.Unborn && s.Upstream != "" {
			if s.Ahead > 0 && s.Behind == 0 {
				footer += " · p Push"
			}
			if s.Behind > 0 && s.Ahead == 0 && !s.Dirty() {
				footer += " · l Pull"
			}
		}
	}
	return footer + " · : Actions"
}

func (m *Model) helpContent() []string {
	lines := []string{
		m.style(" repodash / Keyboard guide", accent, true), m.rule(max(1, m.width)),
		" NAVIGATION", " ↑↓ / j k      Move between repositories", " PgUp / PgDn   Move one page · Home / End jump to first / last",
		" Tab / Shift+Tab  Cycle All, Attention, Changed, Ahead, Behind, Issues",
		" /             Search name, path or branch · arrows move · Enter opens", " s             Toggle name / attention order · r refresh local status",
		"", " REPOSITORY ACTIONS", " Enter / d     Open repository overview / changes · Tab switches section", " o             Open a shell in the highlighted worktree", " g             Open LazyGit in the highlighted worktree",
		"", " BULK OPERATIONS", " Space         Toggle selection · a selects / deselects visible rows",
		" f             Review fetch targets, then confirm", " p / l         Review fetch scope → fetch → review push / FF pull → confirm",
		" Search or view changes clear selection. Actions require explicit selection.",
		"", " GLOBAL COMMANDS", " : / Ctrl+K    Fuzzy command palette · arrows choose · Enter runs", " ?             Help · q quit · Ctrl+C interrupt", "", " AGENT ACTIONS", " No agent integration is configured in this application.", "", " Sync counts use locally known refs. Fetch checks the remote.", " ↑ ahead · ↓ behind · unknown never means up to date.",
		" Esc cancels previews; during a batch it requests cancellation.", " Esc clears search, then dismisses results. q quits; Ctrl+C interrupts.",
	}
	return lines
}

func (m *Model) rule(w int) string {
	return m.style(strings.Repeat(m.symbols().rule, max(1, w)), muted, false)
}

func (m *Model) style(text, color string, bold bool) string {
	if m.noColor {
		return text
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(bold).Render(text)
}

// cell measures terminal cells, including wide Unicode and styled text.
func cell(text string, width int) string {
	width = max(0, width)
	text = ansi.Truncate(text, width, "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func fitLines(lines []string, height int) []string {
	height = max(0, height)
	if len(lines) > height {
		return lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func (m *Model) between(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) screen(lines []string) tea.View {
	w, h := max(1, m.width), max(1, m.height)
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		if m.iconMode == "ascii" {
			lines[i] = strings.NewReplacer("…", "~", "·", "|", "→", "->", "↑", "^", "↓", "v", "—", "-", "▏", "|", "│", "|").Replace(lines[i])
		}
		lines[i] = ansi.Truncate(lines[i], w, "")
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

const helpFooter = " ↑↓ / PgUp/Dn scroll · ? / Esc back · q quit"

func (m *Model) helpView() tea.View {
	return m.documentView(m.helpContent(), helpFooter, m.helpOffset)
}

// Documents preserve their title and footer while the body scrolls. The same
// geometry bounds offsets in Update so reversing at the end scrolls immediately.
func (m *Model) documentParts(content []string, footer string) (header, body, foot []string, page int) {
	w, h := max(1, m.width), max(1, m.height)
	headerCount := min(len(content), 2, max(0, h-1))
	header = content[:headerCount]
	for _, line := range content[headerCount:] {
		body = append(body, strings.Split(ansi.Wrap(line, w, "/"), "\n")...)
	}
	for _, line := range strings.Split(footer, "\n") {
		foot = append(foot, strings.Split(ansi.Wrap(line, w, " "), "\n")...)
	}
	foot = foot[:min(len(foot), max(1, h/3))]
	page = max(0, h-len(header)-len(foot))
	return
}

func (m *Model) documentMaxOffset(content []string, footer string) int {
	_, body, _, page := m.documentParts(content, footer)
	return max(0, len(body)-max(1, page))
}

func (m *Model) documentView(content []string, footer string, offset int) tea.View {
	header, body, foot, page := m.documentParts(content, footer)
	offset = min(max(0, offset), max(0, len(body)-max(1, page)))
	lines := append([]string(nil), header...)
	lines = append(lines, fitLines(body[offset:min(len(body), offset+page)], page)...)
	return m.screen(append(lines, foot...))
}
