package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	"repodash/internal/repository"
)

func dashboardRows() []app.Row {
	specs := []struct {
		name   string
		status repository.Status
	}{
		{"api-service", repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}},
		{"design-system", repository.Status{Branch: "feat/tokens", Upstream: "origin/feat/tokens", ComparisonKnown: true, Ahead: 3, Changes: 2, Untracked: 1}},
		{"docs", repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true, Behind: 4}},
		{"worker", repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true, Ahead: 2, Behind: 1, Conflicts: 1}},
		{"experiment", repository.Status{Branch: "prototype"}},
		{"mobile", repository.Status{Branch: "main", Error: "Unable to inspect worktree"}},
	}
	var rows []app.Row
	for _, spec := range specs {
		rows = append(rows, app.Row{Repository: repository.Repository{Name: spec.name, Path: "/home/mark/projects/" + spec.name}, Status: spec.status})
	}
	return rows
}

func TestQuickViewsAndAttentionOrder(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.key(key(" "))
	want := []int{5, 2, 2, 2, 3, 6}
	for i, count := range want {
		m.setScope((i + 1) % len(scopes))
		if len(m.visibleRows()) != count || len(m.selected) != 0 {
			t.Fatalf("scope %s: %d rows, selection %v", scopes[m.scope], len(m.visibleRows()), m.selected)
		}
	}
	m.highlight = 2
	path := m.highlightedRow().Path
	m.key(key("s"))
	if m.highlightedRow().Path != path {
		t.Fatal("sorting moved highlight to another repository")
	}
	if m.rows[m.visibleRows()[0]].Name != "mobile" || m.rows[m.visibleRows()[1]].Name != "worker" {
		t.Fatal("errors and conflicts should sort first")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.scope != 1 {
		t.Fatal("Tab should toggle All / Focus")
	}
}

func TestDashboardAndDocumentsFitTerminal(t *testing.T) {
	for _, color := range []bool{false, true} {
		for _, width := range []int{1, 4, 24, 53, 54, 80, 111, 112, 160} {
			for _, height := range []int{1, 4, 8, 10, 16, 24, 40} {
				for _, mode := range []string{"dashboard", "help", "details", "preview"} {
					t.Run(fmt.Sprintf("%v/%d/%d/%s", color, width, height, mode), func(t *testing.T) {
						m := New(context.Background(), nil, !color)
						rows := dashboardRows()
						rows[0].Name = "開発 🧪\x1b[31m"
						rows[0].Path += strings.Repeat("/long", 40)
						rows[0].Status.Error = "https://alice:secret@example.test/repo\nerror"
						m.applySnapshot(app.Snapshot{Rows: rows})
						m.Update(tea.WindowSizeMsg{Width: width, Height: height})
						switch mode {
						case "help":
							m.help = true
						case "details":
							m.details = true
						case "preview":
							m.preview = &app.Preview{Targets: []app.Target{{Path: rows[0].Path, URL: "https://alice:secret@example.test/repo", Action: app.Push, Reason: strings.Repeat("detail ", 80), Commit: "exact-commit", Eligible: true}}}
						}
						content := m.View().Content
						if strings.Contains(content, "secret") {
							t.Fatal("credential leaked")
						}
						if !color && strings.Contains(content, "\x1b") {
							t.Fatal("escape in plain output")
						}
						lines := strings.Split(content, "\n")
						if len(lines) > height {
							t.Fatalf("%d lines in height %d", len(lines), height)
						}
						for _, line := range lines {
							if ansi.StringWidth(line) > width {
								t.Fatalf("width %d exceeded: %q", width, line)
							}
						}
					})
				}
			}
		}
	}
}

func TestPaginationKeepsHighlightInRenderedList(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 24}, {80, 16}, {120, 16}, {60, 12}} {
		m := New(context.Background(), nil, true)
		rows := make([]app.Row, 50)
		for i := range rows {
			rows[i] = app.Row{Repository: repository.Repository{Name: fmt.Sprintf("repo-%02d", i), Path: fmt.Sprintf("/repos/repo-%02d", i)}}
		}
		m.applySnapshot(app.Snapshot{Rows: rows})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, code := range []rune{tea.KeyPgDown, tea.KeyEnd, tea.KeyPgUp, tea.KeyHome} {
			m.key(tea.KeyPressMsg{Code: code})
			before := m.scroll
			content := m.View().Content
			if !highlightedLine(content, m.highlightedRow().Name) {
				t.Fatalf("highlight hidden at %v: %s", size, content)
			}
			if before != m.scroll {
				t.Fatal("rendering changed scroll position")
			}
		}
	}
}

func TestIndependentStatesAndGuidance(t *testing.T) {
	rows := dashboardRows()
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: rows})
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.highlight = 1
	content := m.View().Content
	for _, want := range []string{"attention", "● changed ↑3", "design-system  feat/tokens → origin/feat/tokens", "locally known refs"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q: %s", want, content)
		}
	}
	unknown := rows[4]
	label, _ := syncLabel(unknown)
	if label != "no upstream" || strings.Contains(nextStep(unknown), "up to date") {
		t.Fatal("unknown state reported synchronized")
	}
	dirtyBehind := rows[2]
	dirtyBehind.Status.Changes = 1
	if !strings.Contains(nextStep(dirtyBehind), "Commit or stash") {
		t.Fatal("dirty pull guidance missing")
	}
}

func TestPreviewKeepsConfirmationVisible(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 16})
	m.preview = &app.Preview{Targets: []app.Target{{Path: "/repos/api", Action: app.Push, Eligible: true, Reason: strings.Repeat("reason ", 200), Commit: "EXACT-COMMIT", DirtyExcluded: true}}}
	for range 100 {
		m.previewKey("pgdown")
	}
	content := m.View().Content
	for _, want := range []string{"EXACT-COMMIT", "Uncommitted changes are excluded", "Enter confirms 1 eligible", "Esc cancels"} {
		if !strings.Contains(content, want) {
			t.Fatalf("preview missing %q: %s", want, content)
		}
	}
}

func TestHelpScrollReversesAtEnd(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	m.key(key("?"))
	for range 200 {
		m.key(key("j"))
	}
	before := m.helpOffset
	if before == 0 {
		t.Fatal("help did not scroll")
	}
	m.key(key("k"))
	if m.helpOffset != before-1 {
		t.Fatal("overshoot prevented scrolling back")
	}
	if !strings.Contains(m.View().Content, "Keyboard guide") || !strings.Contains(m.View().Content, "Esc back") {
		t.Fatal("help lost navigation")
	}
}

func TestNoticeDoesNotHideHighlightedRow(t *testing.T) {
	m := New(context.Background(), nil, true)
	rows := make([]app.Row, 30)
	for i := range rows {
		rows[i] = app.Row{Repository: repository.Repository{Name: fmt.Sprintf("repo-%02d", i), Path: fmt.Sprintf("/repo-%02d", i)}}
	}
	m.applySnapshot(app.Snapshot{Rows: rows})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.Update(key("f")) // Unavailable action displays a notice and shrinks the list.
	if !highlightedLine(m.View().Content, "repo-29") {
		t.Fatal("notice hid highlighted repository")
	}
}

// highlightedLine reports whether the row naming repository carries the
// highlight marker; narrow layouts insert a row number before the name.
func highlightedLine(content, repository string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "▱ "+repository+" ") && strings.Contains(line, "▌") {
			return true
		}
	}
	return false
}
