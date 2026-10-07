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
	if row == nil || !row.Selectable() {
		return nil
	}
	if _, ok := m.detailCache[row.Path]; ok && !m.detailStale[row.Path] {
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

// repositoryDetails returns the details document without the tracked patch,
// and where the patch belongs (-1 when it is not shown); see detailParts.
func (m *Model) repositoryDetails() (lines []string, patchAt int) {
	patchAt = -1
	row := m.highlightedRow()
	if row == nil {
		return m.detailsContent(), patchAt
	}
	w := max(1, m.width)
	title := m.between(m.style(" Repository: ", ink, true)+m.style(gitcli.SafeText(row.Name), accent, true), m.style("Esc to back ", muted, false), w)
	lines = []string{title, m.rule(w)}
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
	label, color := m.primaryStatus(*row)
	if !row.Selectable() {
		// A stale or bare worktree has no repository to inspect: show what Git
		// reported instead of values derived from an empty status.
		lines = append(lines, m.style(" Path: "+gitcli.SafeText(row.Path), muted, false), " "+m.style(label, color, false))
		lines = append(lines, "", m.style(" "+unavailableFact(*row), muted, false))
		if m.detailTab == 3 {
			lines = append(lines, m.worktreeGroupLines(*row)...)
		}
		return m.appendDetailDiagnostics(lines), patchAt
	}
	lines = append(lines, " "+m.detailActions(*row), m.style(" Path: "+gitcli.SafeText(row.Path), muted, false))
	branch := branchLabel(*row)
	if row.Status.Upstream != "" {
		branch += " → " + gitcli.SafeText(row.Status.Upstream)
	}
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
			patchAt = len(lines)
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
		lines = append(lines, m.worktreeGroupLines(*row)...)
	}
	if m.loadDetails != nil && !loaded {
		lines = append(lines, m.style(" Loading repository context…", muted, false))
	}
	if a := m.attention(*row); a.Needs() {
		color := amber
		if a.Level == app.Critical {
			color = danger
		}
		lines = append(lines, "", m.style(" Needs attention", accent, true)+m.style(" · "+a.Level.String(), color, false))
		for _, reason := range a.Reasons {
			lines = append(lines, " - "+row.Describe(reason))
		}
		lines = append(lines, "", m.style(" Next step", accent, true), " "+m.guidance(*row))
	}
	return m.appendDetailDiagnostics(lines), patchAt
}

// appendDetailDiagnostics adds batch results, workspace warnings and a load
// error to the details document.
func (m *Model) appendDetailDiagnostics(lines []string) []string {
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

// unavailableFact explains why a row has no repository context.
func unavailableFact(row app.Row) string {
	if w := row.Worktree; w != nil && w.Bare {
		return "Bare repository · no working tree"
	}
	reason := "directory missing"
	if w := row.Worktree; w != nil && w.PrunableReason != "" {
		reason = gitcli.SafeText(w.PrunableReason)
	}
	return "Directory missing · no repository context · " + reason
}

// worktreeGroupLines lists the highlighted row's group (its main repository and
// linked worktrees) for the Worktree section. It reads only the snapshot, so it
// renders for rows whose details cannot be loaded, such as stale worktrees.
// A repository without linked worktrees has no group to list.
func (m *Model) worktreeGroupLines(row app.Row) []string {
	key := groupKey(row)
	var members []app.Row
	for _, other := range m.rows {
		if groupKey(other) == key {
			members = append(members, other)
		}
	}
	if len(members) < 2 {
		return nil
	}
	lines := []string{"", m.style(" Worktrees", accent, true)}
	for _, other := range members {
		state, color := worktreeLabel(other)
		kind := "linked"
		if isParent(other) {
			kind = "main"
		}
		if w := other.Worktree; w != nil {
			switch {
			case w.Bare:
				state, color = "bare", muted
			case w.Prunable:
				state, color = "stale · "+gitcli.SafeText(w.PrunableReason), amber
			case w.Locked && w.LockReason != "":
				state += " · locked: " + gitcli.SafeText(w.LockReason)
			case w.Locked:
				state += " · locked"
			}
			if w.OutsideRoots {
				kind += ", outside roots"
			}
		}
		lines = append(lines,
			" "+gitcli.SafeText(other.Name)+"  "+m.style(branchLabel(other), muted, false)+"  "+m.style(state, color, false)+"  "+m.style("("+kind+")", muted, false),
			"   "+m.style(gitcli.SafeText(other.Path), muted, false))
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
