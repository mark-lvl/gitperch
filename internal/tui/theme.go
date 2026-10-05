package tui

import (
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
)

type icons struct{ clean, changed, ahead, behind, failed, pointer, rule string }

func iconSet(mode string) icons {
	if mode == "ascii" {
		return icons{"ok", "*", "^", "v", "!", ">", "-"}
	}
	if mode == "nerd" {
		return icons{"\uf00c", "\uf044", "\uf062", "\uf063", "\uf071", ">", "─"}
	}
	return icons{"✓", "◆", "↑", "↓", "×", ">", "─"}
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
