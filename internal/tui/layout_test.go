package tui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/markkaghazgarian/gitperch/internal/app"
	"strings"
	"testing"
	"time"
)

func TestMissionLayout(t *testing.T) {
	for _, tc := range []struct {
		w, h                   int
		split, branch, preview bool
	}{
		{160, 45, true, true, true}, {110, 35, false, false, true}, {78, 28, false, false, true}, {60, 20, false, false, true}, {80, 12, false, false, false},
	} {
		m := New(nil, nil, true)
		m.width, m.height = tc.w, tc.h
		m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
		l := m.layout()
		if l.split != tc.split || (l.bottom > 2) != tc.preview {
			t.Fatalf("%+v: %+v", tc, l)
		}
		if strings.Contains(m.tableHeader(l.listWidth), "BRANCH") != tc.branch {
			t.Fatalf("columns at %d", tc.w)
		}
		if l.slots < 4 {
			t.Fatalf("list too small: %+v", l)
		}
	}
}
func TestTruncationKeepsContext(t *testing.T) {
	for _, s := range []string{"feat/long-authentication-callback", "src/auth/oauth_callback.go", "開発/変更/ファイル.go"} {
		for width := 1; width < 30; width++ {
			for _, v := range []string{truncateMiddle(s, width), truncatePath(s, width), cell(s, width)} {
				if ansi.StringWidth(v) > width {
					t.Fatalf("overflow %d: %s", width, v)
				}
			}
		}
	}
	if !strings.HasSuffix(truncatePath("開発/変更/ファイル.go", 5), ".go") {
		t.Fatal("wide filename suffix lost")
	}
	if !strings.HasSuffix(truncatePath("src/auth/callback.go", 16), "callback.go") {
		t.Fatal("filename lost")
	}
	if !strings.HasPrefix(truncateMiddle("feat/long-callback", 14), "feat/") {
		t.Fatal("branch prefix lost")
	}
}

func TestRefreshPreservesPathAfterReordering(t *testing.T) {
	m := New(nil, nil, true)
	rows := dashboardRows()
	m.applySnapshot(app.Snapshot{Rows: rows})
	m.highlight = 2
	path := m.highlightedRow().Path
	m.selected[path] = true
	m.attentionFirst = true
	// Preserve the same highlight under the explicit sorting change first.
	for i, index := range m.visibleRows() {
		if m.rows[index].Path == path {
			m.highlight = i
			break
		}
	}
	rows[2].Name = "aaa-renamed"
	rows[2].Status.Conflicts = 2
	m.applySnapshot(app.Snapshot{Rows: rows})
	if m.highlightedRow().Path != path || !m.selected[path] {
		t.Fatal("refresh lost stable path identity")
	}
}
func TestASCIIAndNerdFallbackLayouts(t *testing.T) {
	for _, mode := range []string{"ascii", "unicode", "nerd"} {
		m := New(nil, nil, true)
		m.Configure("/workspace", mode, true)
		m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
		m.width, m.height = 60, 20
		content := m.View().Content
		for _, line := range strings.Split(content, "\n") {
			if ansi.StringWidth(line) > 60 {
				t.Fatalf("%s overflow", mode)
			}
		}
		if mode == "ascii" && strings.ContainsAny(content, "◆✓↑↓→─…·") {
			t.Fatal("presentation did not use ASCII fallback")
		}
	}
}

func TestRelativeTimeStaysShort(t *testing.T) {
	now := time.Date(2026, 10, 5, 19, 42, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{{time.Time{}, "—"}, {now.Add(-20 * time.Second), "now"}, {now.Add(-11 * time.Minute), "11m ago"}, {now.Add(-3 * time.Hour), "3h ago"}, {now.Add(-50 * time.Hour), "2d ago"}, {now.AddDate(0, -3, 0), "Jul 5"}, {now.AddDate(-2, 0, 0), "2024-10"}} {
		if got := relativeTime(now, tc.at); got != tc.want || ansi.StringWidth(got) > 9 {
			t.Fatalf("%v: %q, want %q", tc.at, got, tc.want)
		}
	}
}

func TestNumberKeysJumpToVisibleRows(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.key(key("3"))
	if m.highlight != 2 {
		t.Fatalf("3 highlighted %d", m.highlight)
	}
	m.key(key("9")) // Beyond the six rows: ignored rather than clamped.
	if m.highlight != 2 {
		t.Fatalf("9 moved highlight to %d", m.highlight)
	}
	m.key(key("/"))
	m.key(key("1"))
	if m.filter != "1" || m.highlight != 0 {
		t.Fatal("digits must type into search while filtering")
	}
}
