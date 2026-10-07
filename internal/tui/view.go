package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

var scopes = []string{"All", "Attention", "Changed", "Ahead", "Behind", "Issues"}

// attention is the row's Git attention plus this session's failed or
// uncertain action on it, which the snapshot cannot know about.
func (m *Model) attention(row app.Row) app.Attention {
	a := row.Attention()
	if result, ok := m.results[row.Path]; ok && (result.State == app.Failed || result.State == app.OutcomeUnknown) {
		a = a.With(app.ReasonActionFailed)
	}
	return a
}

func (m *Model) inScope(row app.Row) bool {
	s := row.Status
	switch m.scope {
	case 1:
		return m.attention(row).Needs()
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
	split                          bool // wide selected preview: changes alongside recent commits
	listWidth, body, slots, bottom int
}

const (
	previewCommits = 10 // commits the card is sized for when files do not need more
	previewMargin  = 1  // blank rows kept between the list and a grown card
)

// Geometry follows content as well as the viewport. Spare height goes between
// the repository list and the preview, which stays docked above the footer.
func (m *Model) layout() dashboardLayout {
	w, h := max(1, m.width), max(1, m.height)
	l := dashboardLayout{listWidth: max(1, w-4), bottom: 2}
	notice := 0
	if m.notice() != "" {
		notice = 1
	}
	preview := 0
	if h >= 20 {
		desired := 6
		if row := m.highlightedRow(); row != nil {
			count := max(row.Status.Changes, row.Status.Conflicts) + row.Status.Untracked
			if result, ok := m.detailCache[row.Path]; ok {
				count = len(result.data.Files)
				if w >= 120 {
					// Beside the files, the card always has room for the last
					// previewCommits commits; only more files make it taller.
					count = max(count, min(len(result.data.Commits), previewCommits))
				}
			}
			desired = max(6, count+5)
		}
		// The card grows into rows the list does not need, keeping a margin
		// below the list; a long list still leaves it a third of the screen.
		listNeed := max(4, len(m.visibleRows())+1)
		preview = min(desired, max(6, h/3, h-6-notice-listNeed-previewMargin))
	}
	capacity := max(1, h-6-preview-notice)
	l.body = min(max(4, len(m.visibleRows())+1), capacity)
	if m.showsMark() {
		l.body = capacity // the mark centers in the rows above the preview
	}
	l.slots = max(1, l.body-1)
	l.bottom += preview + notice
	l.split = w >= 120 && preview > 0
	return l
}

func (m *Model) View() tea.View {
	if m.preview != nil {
		return m.previewView()
	}
	if m.cleanup != nil {
		return m.cleanupView()
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
	lines := []string{m.summaryLineAt(w), m.searchLineAt(w)}
	lines = append(lines, m.repositoryList(l)...)
	// The frame fills the terminal: header and list at the top, preview,
	// notice and key hints anchored to the bottom.
	lines = append(lines, make([]string, max(0, m.height-2-len(lines)-l.bottom))...)
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
	lines = append(lines, "", m.footer())
	return m.frame(lines, m.width)
}

func (m *Model) summaryLineAt(w int) string {
	attention, critical := 0, false
	for _, row := range m.rows {
		a := m.attention(row)
		if a.Needs() {
			attention++
		}
		critical = critical || a.Level == app.Critical
	}
	location := m.workspace
	if location == "" {
		if row := m.highlightedRow(); row != nil {
			location = row.Path
		}
	}
	location = gitcli.SafeText(location)
	right := m.chip(fmt.Sprintf("%d repos", len(m.rows)), ink)
	if attention > 0 {
		color := amber
		if critical {
			color = danger
		}
		right += " " + m.chip(fmt.Sprintf("! %d attention", attention), color)
	}
	activity := ""
	if m.refreshing() {
		activity = "refreshing"
	}
	if m.preparing {
		activity = "preparing"
	}
	if m.running {
		activity = m.progressLabel()
	}
	if activity != "" && w >= 100 {
		right = m.style(m.spinner()+" "+activity, working, false) + "  " + right
	}
	if w >= 70 {
		right += "  " + m.style(m.now().Format("15:04"), ink, false)
	}
	brand := m.style(m.symbols().brand+" gitperch", accent, true)
	locationWidth := w - ansi.StringWidth(brand) - ansi.StringWidth(right) - 5
	left := brand
	if locationWidth > 3 {
		left += "  " + m.style(truncatePath(location, locationWidth), accent, false)
	}
	return m.between(left, right, w)
}

// The second header line is a quiet rule in the default view. It names the
// active search or scope only when one narrows the list, plus the scroll range.
func (m *Model) searchLineAt(w int) string {
	hidden := 0
	for _, row := range m.rows {
		if !m.attention(row).Needs() {
			hidden++
		}
	}
	if m.filtering || m.filter != "" {
		query := gitcli.SafeText(m.filter)
		if m.filtering {
			query += "▏"
		}
		context := scopes[m.scope]
		if m.scope == 1 {
			context = fmt.Sprintf("Focus · %d healthy hidden", hidden)
		}
		return m.between(m.style("/ "+query, accent, true), m.style(context, muted, false), w)
	}
	position := ""
	visible := len(m.visibleRows())
	if l := m.layout(); visible > l.slots {
		position = fmt.Sprintf("%d–%d/%d", m.scroll+1, min(visible, m.scroll+l.slots), visible)
	}
	if m.refreshing() {
		position = m.spinner() + " Refreshing local status…"
	}
	if m.scope == 0 {
		return m.labelRule(position, w)
	}
	left := m.style(scopes[m.scope], accent, true)
	if m.scope == 1 {
		left = m.style("Focus", accent, true) + m.style(fmt.Sprintf(" · %d healthy hidden", hidden), muted, false)
	}
	if position == "" {
		position = "Tab all repositories"
	}
	return m.between(left, m.style(position, muted, false), w)
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
		message := []string{m.style(title, ink, true), m.style(hint, muted, false)}
		if m.showsMark() {
			if block := m.markBlock(message, l.listWidth, l.body-1); block != nil {
				return fitLines(append(lines, block...), l.body)
			}
		}
		lines = append(lines, "")
		lines = append(lines, message...)
	}
	for pos := m.scroll; pos < min(len(indices), m.scroll+l.slots); pos++ {
		lines = append(lines, m.tableRow(m.rows[indices[pos]], pos, pos == m.highlight, m.treePrefix(pos, indices), m.isContext(indices[pos]), l.listWidth))
	}
	return fitLines(lines, l.body)
}

// Columns follow the terminal tier: wide shows branch and last activity,
// medium leaves the branch to the preview card, narrow numbers rows for 1–9.
type tableColumns struct{ prefix, index, name, branch, status, changes, updated int }

func columns(w int) tableColumns {
	c := tableColumns{prefix: 5, status: 20, changes: 10}
	wide := w >= 116
	if wide {
		c.updated = 9
	}
	if w < 76 {
		c.index = 1
		c.prefix += c.index + 1
		c.status = 17
	}
	// Gaps separate name, status and changes, plus each optional column.
	gaps := 2
	if c.updated > 0 {
		gaps++
	}
	if wide {
		gaps++
	}
	rest := max(6, w-c.prefix-c.status-c.changes-c.updated-gaps)
	if wide {
		c.name = min(28, max(6, rest*2/5))
		c.branch = min(40, max(1, rest-c.name))
	} else {
		c.name = min(28, rest)
	}
	// Spread leftover width so columns span the table instead of one gap.
	spare := max(0, rest-c.name-c.branch)
	share := spare / 4
	c.name += share
	c.status += share
	if wide {
		c.branch += share
	} else {
		c.status += share
	}
	c.changes += spare - 3*share
	return c
}
func (m *Model) tableHeader(w int) string {
	c := columns(w)
	delta := "Δ"
	if m.iconMode == "ascii" {
		delta = "+/-"
	}
	line := strings.Repeat(" ", c.prefix) + cell("REPO", c.name) + " "
	if c.branch > 0 {
		line += cell("BRANCH", c.branch) + " "
	}
	line += cell("STATUS", c.status) + " " + cell(delta, c.changes)
	if c.updated > 0 {
		line += " " + cell("UPDATED", c.updated)
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
			return m.spinner() + " running", working
		case app.Queued:
			return "◐ queued", muted
		case app.Cancelled:
			return "! cancelled", amber
		}
	}
	if w := row.Worktree; w != nil && w.Bare {
		return "bare repository", muted
	}
	if w := row.Worktree; w != nil && w.Prunable {
		return "◌ stale · directory missing", amber
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
	case row.Worktree != nil && row.Worktree.Locked:
		return "⊘ locked", muted
	case s.Detached && s.HeadUnreferenced:
		return "! detached", amber
	case s.Detached:
		return "detached", muted
	case s.Unborn:
		return "! no commits", amber
	case s.Upstream == "":
		return "! no upstream", amber
	case !s.ComparisonKnown:
		return "! unknown refs", amber
	case s.Ahead > 0:
		noun := "commits"
		if s.Ahead == 1 {
			noun = "commit"
		}
		return fmt.Sprintf("%s %d %s", icons.ahead, s.Ahead, noun), accent
	case s.Behind > 0:
		return fmt.Sprintf("%s %d behind", icons.behind, s.Behind), amber
	default:
		return icons.clean + " clean", success
	}
}
func (m *Model) tableRow(row app.Row, position int, highlighted bool, tree string, context bool, w int) string {
	icons := m.symbols()
	pointer, mark := " ", " "
	if highlighted {
		pointer = icons.pointer
	}
	if m.selected[row.Path] {
		mark = "●"
		if m.iconMode == "ascii" {
			mark = "*"
		}
	}
	c := columns(w)
	line := m.style(pointer+mark, accent, true) + " "
	if c.index > 0 {
		number := ""
		if position < 9 {
			number = fmt.Sprint(position + 1)
		}
		line += m.style(cell(number, c.index), muted, false) + " "
	}
	// The icon names the row's repository group; status keeps its own column.
	line += m.style(icons.repo, m.groupColor(row), false) + " " + m.nameCell(row, tree, context, highlighted, c.name) + " "
	if c.branch > 0 {
		line += m.branchCell(row, c.branch) + " "
	}
	line += m.statusCell(row, c.status) + " " + m.changeCell(row, c.changes)
	if c.updated > 0 {
		line += " " + m.style(cell(relativeTime(m.now(), row.Status.LastActivity), c.updated), muted, false)
	}
	line = cell(line, w)
	if highlighted && !m.noColor {
		return backgroundText(line, selection)
	}
	return line
}

// badgeNameMin is how much of a repository name a wider group badge must leave.
const badgeNameMin = 12

// nameCell shows the tree prefix and name, followed by a collapsed group's
// badge. The name is truncated before the badge. Context parents are muted.
func (m *Model) nameCell(row app.Row, tree string, context, highlighted bool, w int) string {
	nameColor := ink
	if context {
		nameColor = muted
	}
	name := tree + gitcli.SafeText(row.Name)
	// Use the widest badge that leaves the name readable (whole, or at least
	// badgeNameMin cells), then the narrowest that fits at all.
	badges, badge := m.groupBadges(row), ""
	for _, candidate := range badges {
		if w-ansi.StringWidth(candidate)-1 >= min(ansi.StringWidth(name), badgeNameMin) {
			badge = candidate
			break
		}
	}
	if n := len(badges); badge == "" && n > 0 && w-ansi.StringWidth(badges[n-1])-1 >= 1 {
		badge = badges[n-1]
	}
	if badge == "" {
		return m.style(cell(name, w), nameColor, highlighted)
	}
	room := w - ansi.StringWidth(badge) - 1
	name = ansi.Truncate(name, room, "…")
	text := m.style(name, nameColor, highlighted) + " " + m.style(badge, muted, false)
	return text + strings.Repeat(" ", max(0, w-ansi.StringWidth(name)-1-ansi.StringWidth(badge)))
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
	case s.Detached && s.HeadUnreferenced:
		return "detached", amber
	case s.Detached:
		return "detached", muted
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
	case s.Detached && s.HeadUnreferenced:
		return "No branch or tag contains this detached HEAD. Create a branch in your shell to keep its commits."
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
	return fmt.Sprintf("Batch running · %d/%d finished", done, m.actionTotal)
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
	return m.chip(key, accent) + " " + m.style(label, ink, false)
}
func (m *Model) footer() string {
	w := max(1, m.width-4)
	enter := m.symbols().enter
	if m.preparing || m.running {
		// Results held while preparing belong to the previous batch.
		activity := "Preparing…"
		if m.running {
			activity = m.progressLabel()
		}
		return m.hintBar("", []hint{{"Esc", "Cancel", 0}}, w) + "  " + m.style(m.spinner()+" "+activity, working, false)
	}
	if m.filtering {
		return m.hintBar("", []hint{{"↑↓", "Choose", 1}, {enter, "Open", 0}, {"Esc", "Clear", 0}}, w)
	}
	if len(m.selected) > 0 && m.actions != nil {
		selected := []hint{{"f", "Fetch", 1}, {"p", "Push", 0}, {"l", "Pull", 0}}
		if m.cleanupSupported {
			selected = append(selected, hint{"c", "Clean up", 3})
		}
		selected = append(selected, hint{"Space", "Toggle", 2}, hint{":", "Command", 0})
		return m.hintBar(fmt.Sprintf("%d selected  ", len(m.selected)), selected, w)
	}
	hints := []hint{{"↑↓", "Move", 2}, {enter, "Open", 0}, {"d", "Diff", 1}}
	if row := m.highlightedRow(); row != nil && m.actions != nil {
		s := row.Status
		if s.Error == "" && s.Operation == "" && s.Conflicts == 0 && !s.Detached && !s.Unborn && s.Upstream != "" && s.ComparisonKnown {
			if s.Ahead > 0 && s.Behind == 0 {
				hints = append(hints, hint{"p", "Push", 0})
			}
			if s.Behind > 0 && s.Ahead == 0 && !s.Dirty() {
				hints = append(hints, hint{"l", "Pull", 0})
			}
		}
	}
	hints = append(hints, hint{"Space", "Select", 4})
	if m.cleanupSupported && m.actions != nil {
		hints = append(hints, hint{"c", "Clean up", 5})
	}
	hints = append(hints, hint{"o", "Shell", 4}, hint{"/", "Search", 3}, hint{"r", "Refresh", 4}, hint{":", "Command", 0}, hint{"?", "Help", 3})
	return m.hintBar("", hints, w)
}

func (m *Model) helpContent() []string {
	lines := []string{
		m.style(" gitperch / Keyboard guide", accent, true), m.rule(max(1, m.width)),
		" NAVIGATION", " ↑↓ / j k      Move between repositories", " [ / ]         Scroll the selected preview's changed files", " PgUp / PgDn   Move one page · Home / End jump to first / last", " 1–9           Jump to that row (numbered in narrow layouts)",
		" Tab / Shift+Tab  Toggle All / Focus; more filters live in Actions",
		" → / ←         Expand / collapse a repository's worktrees · ← on a worktree selects its repository",
		fmt.Sprintf(" %-14sCollapsed group: 2 worktrees, 1 stale, 1 needs attention", m.symbols().worktree+"2 "+m.symbols().stale+"1 !1"),
		" /             Search name, path or branch · arrows move · Enter opens", " s             Toggle name / attention order · r refresh local status",
		m.autoRefreshHelp(),
		"", " REPOSITORY ACTIONS", " Enter / d     Open repository overview / changes · Tab switches section", " o             Open a shell in the highlighted worktree", " g             Open LazyGit in the highlighted worktree",
		"", " BULK OPERATIONS", " Space         Toggle selection · a selects / deselects visible rows",
		" f             Fetch selected repositories", " p / l         Fetch, then confirm push / FF pull in a popup",
		" Search/view changes clear selection. Push/pull use the highlighted row when none are selected.",
		"", " GLOBAL COMMANDS", " : / Ctrl+K    Fuzzy command palette · arrows choose · Enter runs", " ?             Help · q quit · Ctrl+C interrupt", "", " AGENT ACTIONS", " No agent integration is configured in this application.", "", " Sync counts use locally known refs. Fetch checks the remote.", " ↑ ahead · ↓ behind · unknown never means up to date.",
		" Esc cancels the confirmation popup; during a batch it requests cancellation.", " Esc clears search, then dismisses results. q quits; Ctrl+C interrupts.",
	}
	if m.cleanupSupported {
		// Documented only where Clean up is offered.
		for i, line := range lines {
			if strings.HasPrefix(line, " p / l ") {
				lines = slices.Insert(lines, i+1, " c             Clean up merged worktrees and branches (review first)")
				break
			}
		}
	}
	return lines
}

func (m *Model) autoRefreshHelp() string {
	if m.autoRefresh <= 0 {
		return "               Automatic refresh is off (ui.refresh_seconds = 0)"
	}
	return fmt.Sprintf("               Status also refreshes every %s while the list is idle", m.autoRefresh)
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
			lines[i] = strings.NewReplacer("…", "~", "·", "|", "→", "->", "↑", "^", "↓", "v", "—", "-", "▏", "|", "─", "-", "│", "|", "╭", "+", "╮", "+", "╰", "+", "╯", "+", "◇", "*", "●", "*", "◐", "o", "▱", "/", "▰", "/", "↵", "Enter").Replace(lines[i])
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
	return m.documentPartsAt(content, footer, max(1, m.width))
}

// documentPartsAt wraps the body to bodyWidth, leaving room for a side column.
func (m *Model) documentPartsAt(content []string, footer string, bodyWidth int) (header, body, foot []string, page int) {
	w, h := max(1, m.width), max(1, m.height)
	headerCount := min(len(content), 2, max(0, h-1))
	header = content[:headerCount]
	body = wrapBody(content[headerCount:], bodyWidth)
	for _, line := range strings.Split(footer, "\n") {
		foot = append(foot, strings.Split(ansi.Wrap(line, w, " "), "\n")...)
	}
	foot = foot[:min(len(foot), max(1, h/3))]
	page = max(0, h-len(header)-len(foot))
	return
}

// wrapBody wraps document lines to width. Lines that already fit skip the
// wrapper, which returns them unchanged.
func wrapBody(lines []string, width int) []string {
	width = max(1, width)
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		if ansi.StringWidth(line) <= width {
			body = append(body, line)
			continue
		}
		body = append(body, strings.Split(ansi.Wrap(line, width, "/"), "\n")...)
	}
	return body
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

// labelRule is a thin divider with an optional right-aligned label.
func (m *Model) labelRule(label string, w int) string {
	if label == "" || ansi.StringWidth(label)+4 > w {
		return m.style(strings.Repeat(m.symbols().rule, max(1, w)), border, false)
	}
	rule := strings.Repeat(m.symbols().rule, w-ansi.StringWidth(label)-3)
	return m.style(rule, border, false) + " " + m.style(label, muted, false) + " " + m.style(m.symbols().rule, border, false)
}

// statusCell appends tracking counts unless the primary status already is the
// tracking state, so a dirty branch still shows what it has not yet published.
func (m *Model) statusCell(row app.Row, w int) string {
	label, color := m.primaryStatus(row)
	icons := m.symbols()
	text := m.style(label, color, false)
	tracking := label == "! diverged" || strings.HasPrefix(label, icons.ahead+" ") || strings.HasPrefix(label, icons.behind+" ")
	if counts := m.trackingCounts(row); !tracking && counts != "—" {
		text += " " + m.style(counts, muted, false)
	}
	return cell(text, w)
}

// Default branches are dimmed so feature work stands out in the branch column.
func (m *Model) branchCell(row app.Row, w int) string {
	label, color := branchLabel(row), branchColor
	if s := row.Status; !s.Detached && (s.Branch == "main" || s.Branch == "master") {
		color = muted
	}
	glyph := m.symbols().branch
	if glyph == "" || w < 4 {
		return m.style(cell(truncateMiddle(label, w), w), color, false)
	}
	return m.style(glyph, branchColor, false) + " " + m.style(cell(truncateMiddle(label, w-2), w-2), color, false)
}

type hint struct {
	key, label string
	priority   int // lower survives longer when the bar is too narrow
}

// hintBar drops the least important hints first, keeping display order.
func (m *Model) hintBar(prefix string, hints []hint, w int) string {
	render := func(hs []hint) string {
		parts := []string{}
		for _, h := range hs {
			parts = append(parts, m.keyHint(h.key, h.label))
		}
		return prefix + strings.Join(parts, "  ")
	}
	hints = append([]hint(nil), hints...)
	for len(hints) > 1 && ansi.StringWidth(render(hints)) > w {
		drop := 0
		for i := range hints {
			if hints[i].priority >= hints[drop].priority {
				drop = i
			}
		}
		hints = append(hints[:drop], hints[drop+1:]...)
	}
	return render(hints)
}
