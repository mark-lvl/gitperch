package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Semantic colors follow the reference's dark navy/cyan hierarchy. Bubble Tea
// converts them to the detected 256/16-color profile; truecolor is optional.
const (
	background  = "#080f16"
	surface     = "#0d1824"
	foreground  = "252"
	ink         = foreground
	muted       = "245"
	accent      = "81"
	branchColor = "141"
	success     = "42"
	working     = "81"
	amber       = "214"
	danger      = "203"
	selection   = "#102638"
	border      = "#284052"
	chipSurface = "#15283a" // keycaps, header badges and file-status chips
)

// identityPalette colors each repository's icon so a repository and its
// worktrees read as one group. The order is part of the design: validated on
// the background above as a cycle (the last color wraps to the first), every
// neighboring pair stays apart under color-vision deficiency (OKLab ΔE ≥ 13.7)
// and normal vision (ΔE ≥ 17.4), each color keeps ≥ 3:1 contrast, and none
// sits within ΔE 15 of the success, amber, danger or accent colors, so an
// identity icon never reads as a status. Red, orange, yellow, magenta and
// green are left out for that reason.
var identityPalette = [...]string{
	"#199e70", // aqua
	"#9085e9", // violet
	"#8f9a2e", // olive
	"#2aa6b8", // teal
	"#6a6fe0", // indigo
	"#b0703a", // brown
	"#b56ad0", // plum
}

// branch is empty outside nerd mode: no fork glyph is common to terminal fonts.
// spinner frames are each one cell wide so the busy labels never shift.
type icons struct {
	clean, changed, ahead, behind, failed, pointer, rule, repo, brand, branch, enter, worktree, stale string
	spinner                                                                                           []string
}

var (
	brailleSpinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	asciiSpinner   = []string{"|", "/", "-", "\\"}
)

func iconSet(mode string) icons {
	i := icons{clean: "✓", changed: "●", ahead: "↑", behind: "↓", failed: "×", pointer: "▌", rule: "─", repo: "▱", brand: "◇", enter: "↵", worktree: "⑂", stale: "◌", spinner: brailleSpinner}
	switch mode {
	case "ascii":
		i = icons{clean: "ok", changed: "*", ahead: "^", behind: "v", failed: "!", pointer: ">", rule: "-", repo: "/", brand: "*", enter: "Enter", worktree: "wt", stale: "~", spinner: asciiSpinner}
	case "nerd":
		i.clean = "\uf00c"
		i.changed = "\uf044"
		i.ahead = "\uf062"
		i.behind = "\uf063"
		i.failed = "\uf071"
		i.repo = "\uf07b"
		i.brand = "\uf1d3"
		i.branch = "\ue0a0"
	}
	return i
}

// relativeTime keeps ages short enough for a narrow column. Zero is unknown.
func relativeTime(now, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	default:
		return t.Format("2006-01")
	}
}

// chip draws a small filled label. Without color it falls back to brackets,
// which also keeps plain-text captures and assertions readable.
func (m *Model) chip(text, color string) string {
	if m.noColor {
		return "[" + text + "]"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Background(lipgloss.Color(chipSurface)).Render(" " + text + " ")
}

// fileChip is the colored one-letter status badge used beside changed files.
func (m *Model) fileChip(code, color string) string {
	if m.noColor {
		return cell(code, 2)
	}
	code = ansi.Truncate(code, 2, "")
	return lipgloss.NewStyle().Foreground(lipgloss.Color(background)).Background(lipgloss.Color(color)).Bold(true).Render(code) + strings.Repeat(" ", 2-ansi.StringWidth(code))
}

// truncateMiddle keeps a branch prefix and meaningful suffix. Path truncation
// keeps rightmost components; repository labels retain their beginning via cell.
func truncateMiddle(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width < 3 {
		return ansi.Truncate(s, max(0, width), "")
	}
	left := (width - 1) / 2
	return ansi.Truncate(ansi.Truncate(s, left, "")+"…"+tailCells(s, width-1-left), width, "")
}
func truncatePath(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width < 2 {
		return ansi.Truncate(s, max(0, width), "")
	}
	return "…" + tailCells(s, width-1)
}
func (m *Model) symbols() icons { return iconSet(m.iconMode) }

// spinner is the current busy frame; captures never tick, so they show frame 0.
func (m *Model) spinner() string {
	frames := m.symbols().spinner
	return frames[m.spinnerFrame%len(frames)]
}

// Cut may include a grapheme crossing its starting cell. Advance that boundary
// instead of trimming the right edge, so a wide filename keeps its extension.
func tailCells(s string, width int) string {
	total := ansi.StringWidth(s)
	for start := max(0, total-width); start <= total; start++ {
		tail := ansi.Cut(s, start, total)
		if ansi.StringWidth(tail) <= width {
			return tail
		}
	}
	return ""
}
