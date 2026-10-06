package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"strings"
)

type detailLoader func(context.Context, string) (gitcli.RepoDetails, error)
type detailResult struct {
	data gitcli.RepoDetails
	err  error
}
type detailMsg struct {
	path       string
	generation uint64
	result     detailResult
}

func (m *Model) EnableDetails(load detailLoader) { m.loadDetails = load }
func (m *Model) ensureDetail() tea.Cmd {
	if m.closing || m.loadDetails == nil || m.preparing || m.running {
		return nil
	}
	row := m.highlightedRow()
	if row == nil {
		return nil
	}
	if _, ok := m.detailCache[row.Path]; ok {
		return nil
	}
	if m.detailPath == row.Path {
		return nil
	}
	if m.detailCancel != nil {
		m.detailCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.detailCancel = cancel
	m.detailGeneration++
	generation := m.detailGeneration
	m.detailPath = row.Path
	path, load := row.Path, m.loadDetails
	return func() tea.Msg {
		data, err := load(ctx, path)
		return detailMsg{path, generation, detailResult{data, err}}
	}
}
func (m *Model) repositoryDetails() []string {
	row := m.highlightedRow()
	if row == nil {
		return m.detailsContent()
	}
	w := max(1, m.width)
	title := m.between(m.style(" Repository: ", ink, true)+m.style(gitcli.SafeText(row.Name), accent, true), m.style("Esc to back ", muted, false), w)
	lines := []string{title, m.rule(w)}
	// Wide terminals list sections in a side column; narrow ones keep tabs inline.
	if m.detailNavWidth() == 0 {
		tabs := append([]string(nil), detailTabs...)
		for i := range tabs {
			if i == m.detailTab {
				tabs[i] = m.style("["+tabs[i]+"]", accent, true)
			} else {
				tabs[i] = m.style(tabs[i], muted, false)
			}
		}
		lines = append(lines, " "+strings.Join(tabs, "  "))
	}
	lines = append(lines, " "+m.detailActions(*row), m.style(" Path: "+gitcli.SafeText(row.Path), muted, false))
	branch := branchLabel(*row)
	if row.Status.Upstream != "" {
		branch += " → " + gitcli.SafeText(row.Status.Upstream)
	}
	label, color := m.primaryStatus(*row)
	lines = append(lines, " "+m.style(branch, branchColor, false), " "+m.style(label, color, false)+"  "+m.previewHint(*row))
	result, loaded := m.detailCache[row.Path]
	if row.Status.Error != "" {
		lines = append(lines, "", m.style("Error: "+gitcli.SafeText(row.Status.Error), danger, false))
	}
	if result.err != nil {
		lines = append(lines, "", m.style("Preview error: "+gitcli.SafeText(result.err.Error()), danger, false))
	}
	appendFiles := func(limit int) {
		lines = append(lines, "", m.style(" Working tree", accent, true))
		if loaded && len(result.data.Files) == 0 && result.err == nil {
			lines = append(lines, m.style(" No uncommitted changes", muted, false))
		}
		for _, file := range result.data.Files[:min(limit, len(result.data.Files))] {
			lines = append(lines, " "+m.renderFile(file, max(1, w-m.detailNavWidth()-3)))
		}
		if len(result.data.Files) > limit {
			lines = append(lines, m.style(fmt.Sprintf(" %d more files · Tab for Changes", len(result.data.Files)-limit), muted, false))
		}
	}
	appendCommits := func(limit int) {
		lines = append(lines, "", m.style(" Recent commits", accent, true))
		if loaded && len(result.data.Commits) == 0 && result.err == nil {
			lines = append(lines, m.style(" No commits", muted, false))
		}
		for _, commit := range result.data.Commits[:min(limit, len(result.data.Commits))] {
			lines = append(lines, " "+m.renderCommit(commit, max(1, w-m.detailNavWidth()-3)))
		}
	}
	switch m.detailTab {
	case 0:
		appendFiles(4)
		appendCommits(3)
	case 1:
		appendFiles(len(result.data.Files))
		lines = append(lines, "", m.style(" Tracked patch · unstaged, then staged", accent, true))
		if m.patch != nil && m.patch.path == row.Path {
			if m.patch.err != nil {
				lines = append(lines, m.style(" "+gitcli.SafeText(m.patch.err.Error()), danger, false))
			}
			lines = append(lines, m.patch.lines...)
			if len(m.patch.lines) == 0 && m.patch.err == nil {
				lines = append(lines, m.style(" No tracked patch · untracked file contents are not included", muted, false))
			}
		} else if m.loadPatch != nil {
			lines = append(lines, m.style(" Loading patch…", muted, false))
		}
	case 2:
		appendCommits(len(result.data.Commits))
	case 3:
		operation := gitcli.SafeText(row.Status.Operation)
		if operation == "" {
			operation = "idle"
		}
		upstream := gitcli.SafeText(row.Status.Upstream)
		if upstream == "" {
			upstream = "none"
		}
		lines = append(lines, "", m.style(" Worktree metadata", accent, true), " Branch: "+branchLabel(*row), " Upstream: "+upstream, " Common Git directory: "+gitcli.SafeText(row.Status.CommonDir), " Operation: "+operation)
		if !row.LastFetch.IsZero() {
			lines = append(lines, " Last successful fetch: "+row.LastFetch.Format("2006-01-02 15:04:05"))
		}
	}
	if m.loadDetails != nil && !loaded {
		lines = append(lines, m.style(" Loading repository context…", muted, false))
	}
	if m.attentionRank(*row) > 0 {
		lines = append(lines, "", m.style(" Next step", accent, true), " "+m.guidance(*row))
	}
	diagnostics := m.detailsContent()
	for i, line := range diagnostics {
		if strings.Contains(line, "BATCH RESULTS") || strings.Contains(line, "WORKSPACE WARNINGS") {
			lines = append(lines, diagnostics[i:]...)
			break
		}
	}
	if m.loadErr != "" {
		lines = append(lines, "Load error: "+gitcli.SafeText(m.loadErr))
	}
	return lines
}

var detailTabs = []string{"Overview", "Changes", "Commits", "Worktree"}

// detailNavWidth is the section column, including its separator; zero keeps
// the inline tab row on terminals too narrow to spare it.
func (m *Model) detailNavWidth() int {
	if m.highlightedRow() == nil || m.width < 70 {
		return 0
	}
	return 17
}

// detailNav lists sections with the changed-file count beside Changes.
func (m *Model) detailNav(height int) []string {
	w := m.detailNavWidth() - 1
	lines := []string{""}
	for i, tab := range detailTabs {
		count := ""
		if row := m.highlightedRow(); i == 1 && row != nil {
			files := max(row.Status.Changes, row.Status.Conflicts) + row.Status.Untracked
			if result, ok := m.detailCache[row.Path]; ok && result.err == nil {
				files = len(result.data.Files)
			}
			if files > 0 {
				count = m.chip(fmt.Sprint(files), muted)
			}
		}
		label := " " + tab
		line := m.style(cell(label, w-ansi.StringWidth(count)-1), muted, false) + count
		if i == m.detailTab {
			line = m.style(m.symbols().pointer, accent, true) + m.style(cell(tab, w-ansi.StringWidth(count)-2), accent, true) + count
			if !m.noColor {
				line = backgroundText(cell(line, w-1), selection)
			}
		}
		lines = append(lines, line, "")
	}
	lines = fitLines(lines, height)
	for i := range lines {
		lines[i] = cell(lines[i], w) + m.style("│", border, false)
	}
	return lines
}

// detailActions shows only keys that work here for this repository.
func (m *Model) detailActions(row app.Row) string {
	hints := []hint{{"d", "Diff", 0}, {"o", "Shell", 1}}
	if m.lazyGitAvailable {
		hints = append(hints, hint{"g", "LazyGit", 2})
	}
	s := row.Status
	if m.actions != nil && s.Error == "" && s.Operation == "" && s.Conflicts == 0 && !s.Detached && !s.Unborn && s.Upstream != "" && s.ComparisonKnown {
		if s.Ahead > 0 && s.Behind == 0 {
			hints = append(hints, hint{"p", "Push", 0})
		}
		if s.Behind > 0 && s.Ahead == 0 && !s.Dirty() {
			hints = append(hints, hint{"l", "Pull", 0})
		}
	}
	hints = append(hints, hint{":", "More", 0})
	return m.hintBar("", hints, max(1, m.width-m.detailNavWidth()-2))
}

func (m *Model) closeReads() {
	m.closing = true
	if m.loadCancel != nil {
		m.loadCancel()
	}
	if m.detailCancel != nil {
		m.detailCancel()
	}
	if m.patchCancel != nil {
		m.patchCancel()
	}
}
