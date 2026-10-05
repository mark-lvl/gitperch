package tui

import (
	"github.com/charmbracelet/x/ansi"
)

// ANSI colors let the terminal choose its palette; no truecolor is required.
const (
	foreground = "7"
	ink        = foreground
	muted      = "8"
	accent     = "6"
	success    = "2"
	working    = "6"
	amber      = "3"
	danger     = "1"
	selection  = "4"
	border     = muted
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
	return ansi.Truncate(ansi.Truncate(s, left, "")+"…"+ansi.Cut(s, ansi.StringWidth(s)-(width-1-left), ansi.StringWidth(s)), width, "")
}
func truncatePath(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width < 2 {
		return ansi.Truncate(s, max(0, width), "")
	}
	return ansi.Truncate("…"+ansi.Cut(s, ansi.StringWidth(s)-width+1, ansi.StringWidth(s)), width, "")
}
func (m *Model) symbols() icons { return iconSet(m.iconMode) }
