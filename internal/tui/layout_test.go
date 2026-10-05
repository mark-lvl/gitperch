package tui

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestMissionLayout(t *testing.T) {
	for _, tc := range []struct {
		w, h                   int
		split, branch, preview bool
	}{
		{160, 45, true, true, true}, {110, 35, false, true, true}, {78, 28, false, false, true}, {60, 20, false, false, true}, {80, 12, false, false, false},
	} {
		m := New(nil, nil, true)
		m.width, m.height = tc.w, tc.h
		l := m.layout()
		if l.split != tc.split || (l.bottom > 1) != tc.preview {
			t.Fatalf("%+v: %+v", tc, l)
		}
		if strings.Contains(m.tableHeader(l.listWidth), "BRANCH") != tc.branch {
			t.Fatalf("columns at %d", tc.w)
		}
		if l.slots < 5 {
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
	if !strings.HasSuffix(truncatePath("src/auth/callback.go", 16), "callback.go") {
		t.Fatal("filename lost")
	}
	if !strings.HasPrefix(truncateMiddle("feat/long-callback", 14), "feat/") {
		t.Fatal("branch prefix lost")
	}
}
