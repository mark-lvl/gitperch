package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"strings"
)

type patchLoader func(context.Context, string) (string, error)
type patchResult struct {
	path  string
	lines []string
	err   error
	// The wrapped patch for wrapWidth. A loaded patch never changes, and
	// measuring every line again on each frame made large patches sluggish.
	wrapWidth int
	wrapped   []string
}

// wrappedLines wraps the patch for a details body width, once per width.
func (p *patchResult) wrappedLines(width int) []string {
	if p.wrapped == nil || p.wrapWidth != width {
		p.wrapped, p.wrapWidth = wrapBody(p.lines, width), width
	}
	return p.wrapped
}

type patchMsg struct {
	generation uint64
	result     patchResult
}

func (m *Model) EnablePatch(load patchLoader) { m.loadPatch = load }
func (m *Model) cancelPatch() {
	if m.patchCancel != nil {
		m.patchCancel()
		m.patchCancel = nil
	}
	m.patchPath = ""
	m.patchGeneration++
}
func (m *Model) ensurePatch() tea.Cmd {
	row := m.highlightedRow()
	if m.closing || m.running || m.preparing || !m.details || m.detailTab != 1 || row == nil {
		if m.patchCancel != nil {
			m.cancelPatch()
		}
		return nil
	}
	if m.loadPatch == nil {
		return nil
	}
	if m.patch != nil && m.patch.path == row.Path && !m.patchStale {
		return nil
	}
	if m.patchPath == row.Path {
		return nil
	}
	m.cancelPatch()
	ctx, cancel := context.WithCancel(m.ctx)
	m.patchCancel = cancel
	path, load, generation, noColor := row.Path, m.loadPatch, m.patchGeneration, m.noColor
	m.patchPath = path
	return func() tea.Msg {
		text, err := load(ctx, path)
		result := patchResult{path: path, err: err}
		if text != "" {
			for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				safe := gitcli.SafeText(line)
				color := ink
				switch {
				case strings.HasPrefix(line, "+"):
					color = success
				case strings.HasPrefix(line, "-"):
					color = danger
				case strings.HasPrefix(line, "@@"):
					color = accent
				case strings.HasPrefix(line, "diff "):
					color = muted
				}
				// Capture only immutable rendering options; asynchronous reads do not touch
				// model state. The expensive patch is sanitized once, outside View.
				view := Model{noColor: noColor}
				result.lines = append(result.lines, view.style(safe, color, false))
			}
		}
		return patchMsg{generation: generation, result: result}
	}
}
