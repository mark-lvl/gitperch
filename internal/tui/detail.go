package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
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
func fileLine(file gitcli.ChangedFile, width int) string {
	delta := ""
	if file.Binary {
		delta = "binary"
	} else if file.Added > 0 || file.Deleted > 0 {
		delta = fmt.Sprintf("+%d -%d", file.Added, file.Deleted)
	}
	return " " + cell(gitcli.SafeText(file.Code), 2) + " " + cell(truncatePath(gitcli.SafeText(file.Path), max(1, width-len(delta)-6)), max(1, width-len(delta)-6)) + " " + delta
}
func (m *Model) previewLines(row app.Row) []string {
	if m.loadDetails == nil {
		return []string{fmt.Sprintf(" %d changed · %d untracked · %d conflicts · locally known refs", row.Status.Changes, row.Status.Untracked, row.Status.Conflicts)}
	}
	result, ok := m.detailCache[row.Path]
	if !ok {
		return []string{m.style(" Loading changes…", muted, false)}
	}
	if result.err != nil {
		return []string{m.style(" Preview unavailable · Enter for error details", amber, false)}
	}
	lines := []string{}
	for _, file := range result.data.Files[:min(3, len(result.data.Files))] {
		lines = append(lines, fileLine(file, m.width))
	}
	if len(lines) == 0 {
		lines = append(lines, m.style(" Working tree clean · compared with locally known refs", muted, false))
	}
	return lines
}

func (m *Model) repositoryDetails() []string {
	row := m.highlightedRow()
	if row == nil {
		return m.detailsContent()
	}
	lines := []string{m.style(" repodash / "+gitcli.SafeText(row.Name), accent, true), m.rule(m.width)}
	tabs := []string{"Overview", "Changes", "Commits", "Worktree"}
	labels := append([]string(nil), tabs...)
	labels[m.detailTab] = "[" + labels[m.detailTab] + "]"
	lines = append(lines, " "+strings.Join(labels, "  "), "Path: "+gitcli.SafeText(row.Path), m.detailLine(*row))
	result, loaded := m.detailCache[row.Path]
	if row.Status.Error != "" {
		lines = append(lines, "Error: "+gitcli.SafeText(row.Status.Error))
	}
	if result.err != nil {
		lines = append(lines, "Preview error: "+gitcli.SafeText(result.err.Error()))
	}
	switch m.detailTab {
	case 0:
		lines = append(lines, "", m.guidance(*row), "", " Working tree")
		for _, file := range result.data.Files {
			lines = append(lines, fileLine(file, m.width))
		}
		lines = append(lines, "", " Recent commits")
		for _, commit := range result.data.Commits {
			lines = append(lines, " "+gitcli.SafeText(commit.OID)+"  "+gitcli.SafeText(commit.Subject))
		}
	case 1:
		lines = append(lines, "", " Working tree (staged + unstaged line counts)")
		for _, file := range result.data.Files {
			lines = append(lines, fileLine(file, m.width))
		}
		if loaded && len(result.data.Files) == 0 {
			lines = append(lines, " No changed files")
		}
		if result.data.Diff != "" {
			lines = append(lines, "", " Tracked patch (unstaged, then staged)")
			for _, line := range strings.Split(result.data.Diff, "\n") {
				lines = append(lines, gitcli.SafeText(line))
			}
		}
	case 2:
		for _, commit := range result.data.Commits {
			lines = append(lines, " "+gitcli.SafeText(commit.OID)+"  "+gitcli.SafeText(commit.Subject))
		}
		if loaded && len(result.data.Commits) == 0 {
			lines = append(lines, " No commits")
		}
	case 3:
		lines = append(lines, "", "Common Git directory: "+gitcli.SafeText(row.Status.CommonDir), "Operation: "+gitcli.SafeText(row.Status.Operation))
	}
	if m.loadDetails != nil && !loaded {
		lines = append(lines, " Loading repository context…")
	}
	diagnostics := m.detailsContent()
	// Workspace errors and operation results remain fully accessible.
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

func (m *Model) closeReads() {
	m.closing = true
	if m.loadCancel != nil {
		m.loadCancel()
	}
	if m.detailCancel != nil {
		m.detailCancel()
	}
}
