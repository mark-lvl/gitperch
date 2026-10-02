package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	"repodash/internal/discovery"
	"repodash/internal/repository"
	"strings"
	"testing"
	"time"
)

func key(text string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(text)[0], Text: text} }
func testRows() []app.Row {
	return []app.Row{
		{Repository: repository.Repository{Name: "same", Path: "/one/same"}, Status: repository.Status{Branch: "main"}},
		{Repository: repository.Repository{Name: "same", Path: "/two/same"}, Status: repository.Status{Branch: "feature"}},
	}
}

func TestFullDetailsRemainAccessible(t *testing.T) {
	m := New(context.Background(), nil, true)
	rows := testRows()
	rows[0].Status.Error = strings.Repeat("long diagnostic ", 100) + "FINAL ERROR"
	m.applySnapshot(app.Snapshot{Rows: rows, Warnings: []discovery.Warning{{Path: "/missing1", Message: "first"}, {Path: "/missing2", Message: "second"}}})
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	m.Update(key("d"))
	if !strings.Contains(m.View().Content, "Path:") {
		t.Fatal("details lost path")
	}
	for range 200 {
		m.Update(key("j"))
	}
	view := m.View().Content
	if !strings.Contains(view, "/missing2") {
		t.Fatal("last warning inaccessible")
	}
	if len(strings.Split(view, "\n")) > 8 {
		t.Fatal("details exceed terminal height")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.details {
		t.Fatal("details did not close")
	}
}

func TestSelectionFilteringAndPathIdentity(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	m.Update(key("a"))
	if len(m.selected) != 2 {
		t.Fatal("select all")
	}
	m.Update(key("a"))
	if len(m.selected) != 0 {
		t.Fatal("deselect all")
	}
	m.Update(key(" "))
	m.Update(key("j"))
	if !m.selected["/one/same"] || m.highlight != 1 {
		t.Fatal("navigation/selection")
	}
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	if m.highlight != 1 || !m.selected["/one/same"] {
		t.Fatal("refresh lost identity")
	}
	m.Update(key("/"))
	if len(m.selected) != 0 {
		t.Fatal("starting filter retained selection")
	}
	m.Update(key("feature"))
	if len(m.visibleRows()) != 1 || m.highlightedRow().Path != "/two/same" {
		t.Fatal("branch filter")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(key("a"))
	if len(m.selected) != 1 {
		t.Fatal("visible selection")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.filter != "" || len(m.selected) != 0 {
		t.Fatal("clearing filter retained selection")
	}
	m.applySnapshot(app.Snapshot{})
	if len(m.selected) != 0 || m.highlightedRow() != nil {
		t.Fatal("removed rows remained selected")
	}
}

func TestStaleSnapshotsIgnored(t *testing.T) {
	m := New(context.Background(), func(context.Context) (app.Snapshot, error) { return app.Snapshot{}, nil }, true)
	m.Init()
	old := m.generation
	m.refresh()
	latest := m.generation
	m.Update(snapshotMsg{generation: latest, snapshot: app.Snapshot{Rows: testRows()}})
	m.Update(snapshotMsg{generation: old, snapshot: app.Snapshot{}})
	if len(m.rows) != 2 || m.loading {
		t.Fatal("stale refresh replaced current result")
	}
}

func TestRefreshAndQuitCancelWork(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	m := New(context.Background(), func(ctx context.Context) (app.Snapshot, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return app.Snapshot{}, ctx.Err()
	}, true)
	cmd := m.Init()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("load did not start")
	}
	m.Update(key("q"))
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("quit did not cancel load")
	}
	<-done
}

func TestCtrlCAlwaysQuits(t *testing.T) {
	for _, mode := range []string{"normal", "filter", "help"} {
		m := New(context.Background(), nil, true)
		m.filtering = mode == "filter"
		m.help = mode == "help"
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		if cmd == nil {
			t.Fatalf("Ctrl+C ignored in %s mode", mode)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("Ctrl+C did not quit in %s", mode)
		}
	}
	m := New(context.Background(), nil, true)
	m.help = true
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("help swallowed q")
	}
}

func TestEmptyNarrowAndSanitizedView(t *testing.T) {
	m := New(context.Background(), nil, true)
	if !strings.Contains(m.View().Content, "No repositories") {
		t.Fatal("missing empty state")
	}
	rows := testRows()
	rows[0].Name = "bad\x1b[31m"
	rows[0].Status.Error = "https://user:secret@host/repo\nerror"
	rows[0].Status.Branch = strings.Repeat("long", 100)
	m.applySnapshot(app.Snapshot{Rows: rows})
	for _, width := range []int{1, 4, 20, 80} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		view := m.View()
		if strings.Contains(view.Content, "\x1b") || strings.Contains(view.Content, "secret") {
			t.Fatal("unsafe no-color output")
		}
		for _, line := range strings.Split(view.Content, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("line exceeds width %d: %q", width, line)
			}
		}
	}
}

func TestPathVisibleAtNormalWidthAndUnknownNotSynchronized(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	view := m.View().Content
	if !strings.Contains(view, "/one/same") {
		t.Fatal("highlighted path hidden at 80 columns")
	}
	if strings.Contains(view, "synchronized") {
		t.Fatal("unknown comparison marked synchronized")
	}
	if !strings.Contains(view, "local") {
		t.Fatal("missing local-ref comparison label")
	}
}

func TestMissingLazyGitAndChildReturnRefresh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := New(context.Background(), func(context.Context) (app.Snapshot, error) { return app.Snapshot{}, nil }, true)
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	if cmd := m.launchLazyGit(); cmd != nil || !strings.Contains(m.message, "not installed") {
		t.Fatal("missing LazyGit not explained")
	}
	_, cmd := m.Update(childExitedMsg{})
	if cmd == nil || !m.loading {
		t.Fatal("child return did not refresh")
	}
}
