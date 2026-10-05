package tui

import (
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"strings"
)

func (m *Model) selectedPreview(w, h int) []string {
	lines := []string{m.sectionRule("Selected repository", w)}
	row := m.highlightedRow()
	if row == nil {
		return fitLines(append(lines, m.style("Select a repository to inspect its changes.", muted, false)), h)
	}
	name := cell(gitcli.SafeText(row.Name), min(28, max(8, w/3)))
	branch := branchLabel(*row)
	if row.Status.Upstream != "" {
		branch += " → " + gitcli.SafeText(row.Status.Upstream)
	}
	lines = append(lines, m.style(strings.TrimRight(name, " "), accent, true)+"  "+m.style(truncateMiddle(branch, max(1, w-ansi.StringWidth(strings.TrimRight(name, " "))-2)), branchColor, false))
	if h >= 8 {
		lines = append(lines, m.style(truncatePath(gitcli.SafeText(row.Path), w), muted, false))
	}
	result, loaded := m.detailCache[row.Path]
	split := w >= 116 && loaded && len(result.data.Commits) > 0
	leftWidth := w
	if split {
		leftWidth = (w - 3) * 55 / 100
	}
	rightWidth := w - leftWidth - 3
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
	available := max(1, h-len(lines)-1)
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
				c := result.data.Commits[i]
				right = m.style(c.OID, accent, false) + "  " + gitcli.SafeText(c.Subject)
			}
			line = cell(line, leftWidth) + m.style(" │ ", border, false) + cell(right, rightWidth)
		}
		lines = append(lines, line)
	}
	hint := m.previewHint(*row)
	if loaded && len(result.data.Files) > available {
		hint = m.between(cell(hint, max(1, w-24)), m.style("[ / ] scroll · Enter all", muted, false), w)
	}
	lines = append(lines, hint)
	return fitLines(lines, h)
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
	return m.style(cell(code, 2), color, true) + " " + m.style(cell(truncatePath(gitcli.SafeText(file.Path), pathWidth), pathWidth), ink, false) + " " + delta
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
	available := height - 4
	if height >= 8 {
		available--
	}
	return max(0, len(data.data.Files)-max(1, available))
}
