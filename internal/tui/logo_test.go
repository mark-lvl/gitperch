package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mark-lvl/gitperch/internal/app"
)

// markBranch is the start of the mark's bottom row: a commit and the heavy
// rule the bird perches on.
const markBranch = "●━━━"

func emptyWorkspace(w, h int, icons string) *Model {
	m := New(context.Background(), nil, true)
	m.Configure("~/dev/platform", icons, false)
	m.clock = func() time.Time { return captureNow }
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// lineWith returns the row index and cell column of the first view line
// containing text, or -1, -1.
func lineWith(view, text string) (int, int) {
	for i, line := range strings.Split(view, "\n") {
		if at := strings.Index(line, text); at >= 0 {
			return i, ansi.StringWidth(line[:at])
		}
	}
	return -1, -1
}

func TestScanningCentersMarkInEmptyList(t *testing.T) {
	m := emptyWorkspace(80, 24, "unicode")
	m.loading = true
	view := m.View().Content
	// 80 columns leave a 76-cell list after the frame; rows 4–14 sit between
	// the table header and the six-row preview card.
	row, col := lineWith(view, markBranch)
	if row != 9 || col != 30 {
		t.Fatalf("mark branch at row %d col %d, want row 9 col 30:\n%s", row, col, view)
	}
	if row, col := lineWith(view, "▄█▀█▄"); row != 6 || col != 39 {
		t.Fatalf("bird head at row %d col %d, want row 6 col 39:\n%s", row, col, view)
	}
	if row, col := lineWith(view, "Scanning your workspace…"); row != 11 || col != 28 {
		t.Fatalf("title at row %d col %d, want row 11 col 28:\n%s", row, col, view)
	}
	if row, col := lineWith(view, "Repository status will appear here."); row != 12 || col != 22 {
		t.Fatalf("hint at row %d col %d, want row 12 col 22:\n%s", row, col, view)
	}
}

func TestNoRepositoriesShowsMark(t *testing.T) {
	m := emptyWorkspace(110, 35, "unicode")
	m.applySnapshot(app.Snapshot{})
	view := m.View().Content
	if row, _ := lineWith(view, markBranch); row < 0 || !strings.Contains(view, "No repositories discovered") {
		t.Fatalf("empty workspace should show the mark and its message:\n%s", view)
	}
}

// Once repositories are loaded, an empty list means a search or scope hides
// them; the mark would flash there on every automatic refresh.
func TestMarkStaysOutOfNarrowedLists(t *testing.T) {
	for _, tc := range []struct {
		name    string
		narrow  func(*Model)
		loading bool
		message string
	}{
		{"filter", func(m *Model) { m.filter = "zzz" }, false, "No repositories match this filter"},
		{"filter while refreshing", func(m *Model) { m.filter = "zzz" }, true, "Scanning your workspace…"},
		{"scope", func(m *Model) { m.setScope(3) }, false, "Nothing needs attention in this view"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := emptyWorkspace(110, 35, "unicode")
			rows := dashboardRows()[:1] // clean, so the Ahead scope is empty
			m.applySnapshot(app.Snapshot{Rows: rows})
			tc.narrow(m)
			m.loading = tc.loading
			view := m.View().Content
			if strings.Contains(view, markBranch) || !strings.Contains(view, tc.message) {
				t.Fatalf("want %q without the mark:\n%s", tc.message, view)
			}
		})
	}
}

func TestMarkFallsBackToMessage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		w, h  int
		icons string
	}{
		{"ascii icons", 80, 24, "ascii"},
		{"short terminal", 60, 12, "unicode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := emptyWorkspace(tc.w, tc.h, tc.icons)
			m.loading = true
			view := m.View().Content
			if strings.ContainsAny(view, "▄▀█━") || !strings.Contains(view, "Scanning your workspace") {
				t.Fatalf("want the message alone:\n%s", view)
			}
		})
	}
}
