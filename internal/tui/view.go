package tui

import (
	"fmt"
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
	split                                                     bool // wide selected preview: changes alongside recent commits
	listWidth, inspectorWidth, body, slots, rowHeight, bottom int
}

// Geometry follows content as well as the viewport. A small workspace must not
// create a screenful of blank rows between repositories and their context.
func (m *Model) layout() dashboardLayout {
	w, h := max(1, m.width), max(1, m.height)
	l := dashboardLayout{listWidth: max(1, w-4), rowHeight: 1, bottom: 2}
	preview := 0
	if h >= 20 {
		desired := 6
		if row := m.highlightedRow(); row != nil {
			count := max(row.Status.Changes, row.Status.Conflicts) + row.Status.Untracked
			if result, ok := m.detailCache[row.Path]; ok {
				count = len(result.data.Files)
				if w >= 120 {
					count = max(count, len(result.data.Commits))
				}
			}
			desired = max(6, count+5)
		}
		preview = min(desired, 12, max(6, h/3))
	}
	notice := 0
	if m.notice() != "" {
		notice = 1
	}
	capacity := max(1, h-7-preview-notice)
	l.body = min(max(4, len(m.visibleRows())+1), capacity)
	l.slots = max(1, l.body-1)
	l.bottom += preview + notice
	l.split = w >= 120 && preview > 0
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
	return m.screen(m.workspaceLines())
}

func (m *Model) workspaceLines() []string {
	l := m.layout()
	w := l.listWidth
	lines := []string{m.summaryLineAt(w), m.searchLineAt(w), ""}
	lines = append(lines, m.repositoryList(l)...)
	previewHeight := l.bottom - 2
	if m.notice() != "" {
		previewHeight--
	}
	if previewHeight > 0 {
		lines = append(lines, m.selectedPreview(w, previewHeight)...)
	}
	if notice := m.notice(); notice != "" {
		lines = append(lines, m.style(cell(notice, w), amber, false))
	}
	lines = append(lines, m.rule(w), m.footer())
	return m.frame(lines, m.width)
}

func (m *Model) summaryLineAt(w int) string {
	attention := 0
	for _, row := range m.rows {
		if m.attentionRank(row) > 0 {
			attention++
		}
	}
	location := m.workspace
	if location == "" {
		if row := m.highlightedRow(); row != nil {
			location = row.Path
		}
	}
	location = gitcli.SafeText(location)
	right := m.badge(fmt.Sprintf("%d repos", len(m.rows)), muted)
	if attention > 0 {
		right += " " + m.badge(fmt.Sprintf("! %d attention", attention), amber)
	}
	activity := ""
	if m.loading {
		activity = "refreshing"
	}
	if m.preparing {
		activity = "preparing"
	}
	if m.running {
		activity = m.progressLabel()
	}
	if activity != "" && w >= 100 {
		right += "  " + m.style(activity, working, false)
	}
	brand := m.style("◇ repodash", accent, true)
	locationWidth := w - ansi.StringWidth(brand) - ansi.StringWidth(right) - 5
	left := brand
	if locationWidth > 3 {
		left += "  " + m.style(truncatePath(location, locationWidth), accent, false)
	}
	return m.between(left, right, w)
}
func (m *Model) badge(text, color string) string {
	return m.style("["+text+"]", color, false)
}
func (m *Model) searchLineAt(w int) string {
	if m.filtering || m.filter != "" {
		query := gitcli.SafeText(m.filter)
		if m.filtering {
			query += "▏"
		}
		return m.between(m.style("/ "+truncatePath(query, max(1, w-25)), accent, true), m.style("Esc clear · Enter open", muted, false), w)
	}
	all, focus := m.style("All repositories", muted, false), m.style("Focus", muted, false)
	if m.scope == 0 {
		all = m.style("All repositories", accent, true)
	} else if m.scope == 1 {
		focus = m.style("Focus", accent, true)
	}
	left := all + "  " + focus
	if m.scope > 1 {
		left += "  " + m.style(scopes[m.scope], accent, true)
	}
	if m.scope == 1 {
		hidden := 0
		for _, row := range m.rows {
			if m.attentionRank(row) == 0 {
				hidden++
			}
		}
		left += m.style(fmt.Sprintf(" · %d healthy hidden", hidden), muted, false)
	}
	return m.between(left, m.style("Tab view · / search · ? help", muted, false), w)
}
func (m *Model) repositoryList(l dashboardLayout) []string {
	indices := m.visibleRows()
	lines := []string{m.tableHeader(l.listWidth)}
	if len(indices) == 0 {
		title, hint := "No repositories discovered", "Check workspace paths; r scans again."
		if m.loading {
			title, hint = "Scanning your workspace…", "Repository status will appear here."
		} else if m.filter != "" {
			title, hint = "No repositories match this filter", "Esc clears your search."
		} else if m.scope != 0 {
			title, hint = "Nothing needs attention in this view", "Tab returns to all repositories."
		}
		lines = append(lines, "", m.style(title, ink, true), m.style(hint, muted, false))
	}
	for pos := m.scroll; pos < min(len(indices), m.scroll+l.slots); pos++ {
		lines = append(lines, m.tableRow(m.rows[indices[pos]], pos == m.highlight, l.listWidth))
	}
	return fitLines(lines, l.body)
}

type tableColumns struct{ name, branch, status, changes, sync int }

func columns(w int) tableColumns {
	c := tableColumns{status: 17, changes: 10}
	if w >= 80 {
		c.branch = min(40, w/3)
		c.sync = 9
	}
	// Two marker cells, a repository glyph, and a gap between visible columns.
	gaps := 2
	if c.branch > 0 {
		gaps++
	}
	if c.sync > 0 {
		gaps++
	}
	c.name = max(6, w-5-c.branch-c.status-c.changes-c.sync-gaps)
	if c.branch > 0 {
		c.name = min(32, c.name)
	}
	return c
}
func (m *Model) tableHeader(w int) string {
	c := columns(w)
	line := "     " + cell("REPOSITORY", c.name) + " "
	if c.branch > 0 {
		line += cell("BRANCH", c.branch) + " "
	}
	line += cell("STATUS", c.status) + " " + cell("CHANGES", c.changes)
	if c.sync > 0 {
		line += " " + cell("SYNC", c.sync)
	}
	return m.style(cell(line, w), muted, false)
}
func (m *Model) primaryStatus(row app.Row) (string, string) {
	s := row.Status
	icons := m.symbols()
	if result, ok := m.results[row.Path]; ok {
		switch result.State {
		case app.Failed, app.OutcomeUnknown:
			return icons.failed + " failed", danger
		case app.Running:
			return "● running", working
		case app.Queued:
			return "◐ queued", muted
		case app.Cancelled:
			return "! cancelled", amber
		}
	}
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
	pointer, mark := " ", " "
	if highlighted {
		pointer = m.symbols().pointer
	}
	if m.selected[row.Path] {
		mark = "●"
		if m.iconMode == "ascii" {
			mark = "*"
		}
	}
	repoIcon := "▱"
	if m.iconMode == "nerd" {
		repoIcon = "\uf07b"
	}
	if m.iconMode == "ascii" {
		repoIcon = "/"
	}
	c := columns(w)
	label, color := m.primaryStatus(row)
	line := m.style(pointer+mark, accent, true) + " " + m.style(repoIcon, amber, false) + " " + m.style(cell(gitcli.SafeText(row.Name), c.name), ink, highlighted) + " "
	if c.branch > 0 {
		line += m.style(cell(truncateMiddle(branchLabel(row), c.branch), c.branch), branchColor, false) + " "
	}
	line += m.style(cell(label, c.status), color, false) + " " + m.changeCell(row, c.changes)
	if c.sync > 0 {
		line += " " + m.style(cell(m.trackingCounts(row), c.sync), muted, false)
	}
	line = cell(line, w)
	if highlighted && !m.noColor {
		return backgroundText(line, selection)
	}
	return line
}
func (m *Model) trackingCounts(row app.Row) string {
	s := row.Status
	if !s.ComparisonKnown {
		return "—"
	}
	parts := []string{}
	if s.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("%s%d", m.symbols().ahead, s.Ahead))
	}
	if s.Behind > 0 {
		parts = append(parts, fmt.Sprintf("%s%d", m.symbols().behind, s.Behind))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " ")
}
func (m *Model) changeCell(row app.Row, w int) string {
	if result, ok := m.detailCache[row.Path]; ok && result.err == nil {
		added, deleted := 0, 0
		for _, f := range result.data.Files {
			added += f.Added
			deleted += f.Deleted
		}
		if added > 0 || deleted > 0 {
			return cell(m.delta(added, deleted), w)
		}
	}
	files := max(row.Status.Changes, row.Status.Conflicts) + row.Status.Untracked
	if files > 0 {
		return m.style(cell(fileCount(files), w), muted, false)
	}
	return m.style(cell("—", w), muted, false)
}
func (m *Model) delta(added, deleted int) string {
	parts := []string{}
	if added > 0 {
		parts = append(parts, m.style(fmt.Sprintf("+%d", added), success, false))
	}
	if deleted > 0 {
		parts = append(parts, m.style(fmt.Sprintf("-%d", deleted), danger, false))
	}
	return strings.Join(parts, " ")
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

func (m *Model) keyHint(key, label string) string {
	return m.style("["+key+"]", accent, false) + " " + m.style(label, ink, false)
}
func (m *Model) footer() string {
	if m.preparing || m.running {
		return m.keyHint("Esc", "Cancel") + "  " + m.style(m.progressLabel(), muted, false)
	}
	if m.filtering {
		return m.keyHint("↑↓", "Choose") + "  " + m.keyHint("Enter", "Open") + "  " + m.keyHint("Esc", "Clear")
	}
	if len(m.selected) > 0 && m.actions != nil {
		return fmt.Sprintf("%d selected  ", len(m.selected)) + m.keyHint("f", "Fetch") + "  " + m.keyHint("p", "Push") + "  " + m.keyHint("l", "Pull") + "  " + m.keyHint(":", "Actions")
	}
	parts := []string{m.keyHint("Enter", "Open"), m.keyHint("d", "Diff")}
	if row := m.highlightedRow(); row != nil && m.actions != nil {
		s := row.Status
		if s.Error == "" && s.Operation == "" && s.Conflicts == 0 && !s.Detached && !s.Unborn && s.Upstream != "" && s.ComparisonKnown {
			if s.Ahead > 0 && s.Behind == 0 {
				parts = append(parts, m.keyHint("p", "Push"))
			}
			if s.Behind > 0 && s.Ahead == 0 && !s.Dirty() {
				parts = append(parts, m.keyHint("l", "Pull"))
			}
		}
	}
	if m.width >= 100 {
		parts = append(parts, m.keyHint("Space", "Select"), m.keyHint("o", "Shell"))
	}
	parts = append(parts, m.keyHint(":", "Actions"))
	return strings.Join(parts, "  ")
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
	if right == "" {
		return cell(left, width)
	}
	available := width - ansi.StringWidth(right) - 2
	if available < 1 {
		return cell(right, width)
	}
	return cell(left, available) + "  " + right
}

func (m *Model) screen(lines []string) tea.View {
	w, h := max(1, m.width), max(1, m.height)
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		if m.iconMode == "ascii" {
			lines[i] = strings.NewReplacer("…", "~", "·", "|", "→", "->", "↑", "^", "↓", "v", "—", "-", "▏", "|", "─", "-", "│", "|", "╭", "+", "╮", "+", "╰", "+", "╯", "+", "◇", "*", "●", "*", "◐", "o", "▱", "/", "↵", "Enter").Replace(lines[i])
		}
		lines[i] = ansi.Truncate(lines[i], w, "")
	}
	if !m.noColor {
		lines = fitLines(lines, h)
		for i := range lines {
			lines[i] = backgroundText(cell(lines[i], w), background)
		}
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

// A single light frame holds the workspace; it does not stretch a divider through
// dozens of empty rows when there are only a handful of repositories.
func (m *Model) frame(content []string, w int) []string {
	if w < 4 {
		return content
	}
	top := m.style("╭"+strings.Repeat("─", w-2)+"╮", border, false)
	bottom := m.style("╰"+strings.Repeat("─", w-2)+"╯", border, false)
	if m.iconMode == "ascii" {
		top = m.style("+"+strings.Repeat("-", w-2)+"+", border, false)
		bottom = top
	}
	lines := []string{top}
	for _, line := range content {
		lines = append(lines, m.style("│", border, false)+" "+cell(line, w-4)+" "+m.style("│", border, false))
	}
	return append(lines, bottom)
}
func (m *Model) sectionRule(title string, w int) string {
	label := "─ " + title + " "
	return m.style(label+strings.Repeat("─", max(0, w-ansi.StringWidth(label))), border, false)
}

func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// Nested foreground styles reset SGR attributes. Restore the background after
// each reset so selected rows and the dark canvas remain continuous.
func backgroundText(text, color string) string {
	prefix := ansi.NewStyle().BackgroundColor(lipgloss.Color(color)).String()
	return prefix + strings.NewReplacer("\x1b[m", "\x1b[m"+prefix, "\x1b[0m", "\x1b[0m"+prefix).Replace(text) + ansi.ResetStyle
}
