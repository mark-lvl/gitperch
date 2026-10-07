package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// markLines draws the gitperch logo with text: the songbird in half blocks,
// perched on a heavy rule of four commits that forks up to a fifth beside the
// beak. The bottom row is the branch itself; the belly rests on it and the
// tail slopes down over the first commit.
func (m *Model) markLines() []string {
	bird := func(s string) string { return m.style(s, accent, false) }
	branch := func(s string) string { return m.style(s, branchColor, false) }
	return []string{
		"         " + bird("▄█▀█▄"),
		"      " + bird("▄▄██████▀▀"),
		" " + bird("▄▄██▀█████▀▀") + " " + branch("┏━━●"),
		branch("●━━━━━●━━━━━●━┻━━●"),
	}
}

// showsMark reports whether an empty list shows the mark: only while the
// workspace has no repositories, during the first scan or when none were
// discovered. An empty search or scope over loaded repositories keeps its
// plain message, so refreshes never make the mark come and go. ASCII mode
// has no block glyphs to draw it with.
func (m *Model) showsMark() bool {
	return len(m.rows) == 0 && m.iconMode != "ascii"
}

// markBlock centers the mark above the empty-list message in the rows below
// the table header, or returns nil when they do not fit.
func (m *Model) markBlock(message []string, w, rows int) []string {
	mark := m.markLines()
	markWidth := 0
	for _, line := range mark {
		markWidth = max(markWidth, ansi.StringWidth(line))
	}
	block := append(mark, "")
	block = append(block, message...)
	if len(block) > rows {
		return nil
	}
	lines := make([]string, (rows-len(block))/2, rows)
	for i, line := range block {
		width := markWidth
		if i >= len(mark) {
			width = ansi.StringWidth(line)
		}
		if width > w {
			return nil
		}
		lines = append(lines, strings.Repeat(" ", (w-width)/2)+line)
	}
	return lines
}
