package tui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"strings"
)

// selectedPreview is a bordered card for the highlighted repository. Wide
// layouts place recent commits beside the working tree.
func (m *Model) selectedPreview(w, h int) []string {
	inner := max(1, w-4)
	row := m.highlightedRow()
	if row == nil {
		return m.card([]string{m.style("Select a repository to inspect its changes.", muted, false)}, "", w, h)
	}
	_, color := m.primaryStatus(*row)
	name := m.style(m.symbols().repo, color, false) + " " + m.style(gitcli.SafeText(row.Name), accent, true)
	age := m.style(relativeTime(m.now(), row.Status.LastActivity), muted, false)
	branchWidth := inner - ansi.StringWidth(name) - ansi.StringWidth(age) - 6
	branch := branchLabel(*row)
	// The upstream is dropped before the branch itself is truncated.
	if upstream := gitcli.SafeText(row.Status.Upstream); upstream != "" && ansi.StringWidth(branch+" → "+upstream)+2 <= branchWidth {
		branch += " → " + upstream
	}
	if glyph := m.symbols().branch; glyph != "" {
		branchWidth -= 2
		branch = glyph + " " + truncateMiddle(branch, max(1, branchWidth))
	} else {
		branch = truncateMiddle(branch, max(1, branchWidth))
	}
	header := name
	if branchWidth > 3 {
		header += "  " + m.style(branch, branchColor, false)
	}
	lines := []string{m.between(header, age, inner), m.previewHint(*row)}
	result, loaded := m.detailCache[row.Path]
	split := inner >= 112 && loaded && len(result.data.Commits) > 0
	leftWidth := inner
	if split {
		leftWidth = (inner - 3) * 55 / 100
	}
	rightWidth := inner - leftWidth - 3
	count := max(row.Status.Changes, row.Status.Conflicts) + row.Status.Untracked
	if loaded && result.err == nil {
		count = len(result.data.Files)
	}
	heading := "Working tree · " + fileCount(count)
	if count == 0 && row.Status.Error == "" {
		heading = "Working tree · clean"
	}
	head := m.style(heading, muted, false)
	if split {
		head = cell(head, leftWidth) + "   " + m.style("Recent commits", muted, false)
	}
	lines = append(lines, head)
	available := previewFileRows(h)
	fileRows := []string{}
	switch {
	case row.Status.Error != "":
		fileRows = append(fileRows, m.style(cell(gitcli.SafeText(row.Status.Error), leftWidth), danger, false))
	case m.loadDetails == nil:
		work, _ := worktreeLabel(*row)
		fileRows = append(fileRows, m.style(work, muted, false))
	case !loaded:
		fileRows = append(fileRows, m.style("Loading changes…", muted, false))
	case result.err != nil:
		fileRows = append(fileRows, m.style(cell("Preview: "+gitcli.SafeText(result.err.Error()), leftWidth), amber, false))
	default:
		start := min(m.contextOffset, max(0, len(result.data.Files)-available))
		for _, file := range result.data.Files[start:min(len(result.data.Files), start+available)] {
			fileRows = append(fileRows, m.renderFile(file, leftWidth))
		}
		if len(result.data.Files) == 0 {
			fileRows = append(fileRows, m.style("No uncommitted changes", muted, false))
		}
	}
	for i := 0; i < available; i++ {
		line := ""
		if i < len(fileRows) {
			line = fileRows[i]
		}
		if split {
			right := ""
			if i < len(result.data.Commits) {
				right = m.renderCommit(result.data.Commits[i], rightWidth)
			}
			line = cell(line, leftWidth) + m.style(" │ ", border, false) + cell(right, rightWidth)
		}
		lines = append(lines, line)
	}
	label := ""
	if loaded && len(result.data.Files) > available {
		label = "[ / ] scroll · " + m.symbols().enter + " all"
	}
	return m.card(lines, label, w, h)
}
func (m *Model) previewHint(row app.Row) string {
	label, color := syncLabel(row)
	if row.Status.Error != "" {
		return m.style("Enter details · r retry inspection", danger, false)
	}
	if row.Status.Conflicts > 0 {
		return m.style("! Resolve conflicts in your shell or LazyGit before syncing", danger, false)
	}
	if row.Status.Operation != "" {
		return m.style("! Finish the active Git operation before syncing", amber, false)
	}
	if result, ok := m.results[row.Path]; ok && (result.State == app.Failed || result.State == app.OutcomeUnknown) {
		return m.style("! Operation failed · Enter for error details", danger, false)
	}
	return m.style(label, color, false) + m.style(" · locally known refs", muted, false)
}
func (m *Model) renderFile(file gitcli.ChangedFile, w int) string {
	code := strings.TrimSpace(file.Code)
	if code == "??" {
		code = "?"
	}
	color := amber
	if strings.Contains(code, "A") || code == "?" {
		color = success
	}
	if strings.Contains(code, "D") || strings.Contains(code, "U") {
		color = danger
	}
	delta := m.delta(file.Added, file.Deleted)
	if file.Binary {
		delta = m.style("binary", muted, false)
	}
	pathWidth := max(1, w-4-ansi.StringWidth(delta)-1)
	return m.fileChip(code, color) + " " + m.style(cell(truncatePath(gitcli.SafeText(file.Path), pathWidth), pathWidth), ink, false) + " " + delta
}

func (m *Model) maxContextOffset() int {
	row := m.highlightedRow()
	if row == nil {
		return 0
	}
	data, ok := m.detailCache[row.Path]
	if !ok {
		return 0
	}
	height := m.layout().bottom - 2
	if m.notice() != "" {
		height--
	}
	return max(0, len(data.data.Files)-previewFileRows(height))
}

// previewFileRows is the room for changed files inside a card of height h:
// borders, the repository header, its status line and the column headings.
func previewFileRows(h int) int { return max(1, h-5) }

// renderCommit shows a commit with its age right-aligned when it fits.
func (m *Model) renderCommit(c gitcli.Commit, w int) string {
	left := m.style(gitcli.SafeText(c.OID), accent, false) + "  " + gitcli.SafeText(c.Subject)
	if glyph := m.symbols().branch; glyph != "" {
		left = m.style(glyph, branchColor, false) + " " + left
	}
	if c.Time.IsZero() || w < 40 {
		return cell(left, w)
	}
	return m.between(left, m.style(relativeTime(m.now(), c.Time), muted, false), w)
}

// card frames content with rounded borders and an optional bottom-right label.
func (m *Model) card(content []string, label string, w, h int) []string {
	if w < 6 || h < 3 {
		return fitLines(content, h)
	}
	inner := w - 4
	lines := []string{m.style("╭"+strings.Repeat("─", w-2)+"╮", border, false)}
	for _, line := range fitLines(content, h-2) {
		lines = append(lines, m.style("│", border, false)+" "+cell(line, inner)+" "+m.style("│", border, false))
	}
	bottom := m.style("╰"+strings.Repeat("─", w-2)+"╯", border, false)
	if label != "" && ansi.StringWidth(label)+6 <= w {
		bottom = m.style("╰"+strings.Repeat("─", w-ansi.StringWidth(label)-5)+" ", border, false) + m.style(label, muted, false) + m.style(" ─╯", border, false)
	}
	return append(lines, bottom)
}
