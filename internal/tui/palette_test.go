package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"strings"
	"testing"
)

func TestPaletteContextAndFuzzyFiltering(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.highlight = 1
	m.Update(key(":"))
	m.Update(key("psh"))
	found := m.paletteCommands()
	if len(found) != 1 || found[0].id != "push" {
		t.Fatalf("fuzzy push: %+v", found)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.palette {
		t.Fatal("palette remained open")
	}
	m.highlight = 4 // mobile inspection error
	for _, c := range m.commands() {
		if c.id == "push" || c.id == "pull" {
			t.Fatal("unsafe unavailable shortcut")
		}
	}
	m.selected[m.highlightedRow().Path] = true
	if !strings.Contains(m.footer(), "selected") {
		t.Fatal("bulk footer absent")
	}
	m.actions = nil
	for _, c := range m.commands() {
		if c.id == "push" || c.id == "pull" || c.id == "fetch" {
			t.Fatal("action without backend")
		}
	}
}
func TestContextFooter(t *testing.T) {
	m := New(nil, nil, true)
	rows := dashboardRows()
	m.applySnapshot(app.Snapshot{Rows: rows})
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	for _, tc := range []struct {
		index           int
		present, absent string
	}{{0, "[↵] Open", "[p] Push"}, {1, "[p] Push", "[l] Pull"}, {2, "[l] Pull", "[p] Push"}, {5, "[d] Diff", "[p] Push"}} {
		m.highlight = tc.index
		footer := m.footer()
		if !strings.Contains(footer, tc.present) || strings.Contains(footer, tc.absent) {
			t.Fatalf("%d: %s", tc.index, footer)
		}
	}
}
func TestDetailLoaderCachesAndRejectsStaleResults(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	m.EnableDetails(func(ctx context.Context, path string) (gitcli.RepoDetails, error) {
		return gitcli.RepoDetails{}, ctx.Err()
	})
	first := m.ensureDetail()
	old := m.detailGeneration
	if first == nil || m.ensureDetail() != nil {
		t.Fatal("duplicate detail read")
	}
	m.highlight = 1
	second := m.ensureDetail()
	if second == nil {
		t.Fatal("navigation did not load")
	}
	m.Update(detailMsg{path: "/one/same", generation: old, result: detailResult{}})
	if len(m.detailCache) != 0 {
		t.Fatal("stale detail accepted")
	}
	m.Update(second())
	if _, ok := m.detailCache["/two/same"]; !ok || m.ensureDetail() != nil {
		t.Fatal("detail not cached")
	}
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	if !m.detailStale["/two/same"] || m.ensureDetail() == nil {
		t.Fatal("refresh did not invalidate detail")
	}
	m.closeReads()
	if m.ensureDetail() != nil {
		t.Fatal("quit started a read")
	}
}
func TestSearchNavigationEscapeAndFocusIdentity(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.Update(key("/"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.highlight != 1 {
		t.Fatal("search blocked navigation")
	}
	m.Update(key("tokens"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.details || m.highlightedRow().Name != "design-system" {
		t.Fatal("search did not open result")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(key("/"))
	m.Update(key("docs"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.filter != "" || m.filtering {
		t.Fatal("Escape did not clear search")
	}
	m.highlight = 1
	path := m.highlightedRow().Path
	m.executeCommand("focus")
	if m.highlightedRow().Path != path {
		t.Fatal("Focus lost identity")
	}
	m.executeCommand("focus")
	if m.highlightedRow().Path != path {
		t.Fatal("All lost identity")
	}
}

// Terminals report a typed capital as a shifted key, and the Kitty protocol
// also reports Caps Lock; both still carry printable text.
func TestSearchAndPaletteAcceptShiftedText(t *testing.T) {
	shifted := []tea.KeyPressMsg{
		{Code: 'd', ShiftedCode: 'D', Text: "D", Mod: tea.ModShift},
		{Code: '-', ShiftedCode: '_', Text: "_", Mod: tea.ModShift},
		{Code: 'o', Text: "O", Mod: tea.ModCapsLock},
	}
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.Update(key("/"))
	for _, msg := range shifted {
		m.Update(msg)
	}
	if m.filter != "D_O" {
		t.Fatalf("search filter = %q, want %q", m.filter, "D_O")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(key(":"))
	for _, msg := range shifted {
		m.Update(msg)
	}
	if m.paletteQuery != "D_O" {
		t.Fatalf("palette query = %q, want %q", m.paletteQuery, "D_O")
	}
}

func TestSearchAndPaletteIgnoreModifiedKeys(t *testing.T) {
	// Ctrl and Alt combinations arrive without text and must not be typed.
	modified := []tea.KeyPressMsg{{Code: 'x', Mod: tea.ModAlt}, {Code: 'x', Mod: tea.ModCtrl}}
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.Update(key("/"))
	for _, msg := range modified {
		m.Update(msg)
	}
	if m.filter != "" {
		t.Fatalf("modified keys typed into search: %q", m.filter)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(key(":"))
	for _, msg := range modified {
		m.Update(msg)
	}
	if m.paletteQuery != "" {
		t.Fatalf("modified keys typed into palette: %q", m.paletteQuery)
	}
}

func TestPaletteAndHelpOverRepositoryDetails(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(key(":"))
	if !strings.Contains(m.View().Content, "Command Palette") {
		t.Fatal("palette hidden behind details")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(key("?"))
	if !strings.Contains(m.View().Content, "Keyboard guide") {
		t.Fatal("help hidden behind details")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.help || !m.details {
		t.Fatal("help did not return to details")
	}
}
func TestOperationFailureNeedsAttentionEvenWhenGitClean(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	row := m.rows[0]
	// The shape of a real rejected push result (see git.Runner.Push).
	message := "[rejected] (fetch first) for refs/heads/main: git push: exit status 1: error: failed to push some refs"
	m.results = map[string]app.Event{row.Path: {State: app.Failed, Message: message}}
	m.scope = 1
	if !m.inScope(row) || !strings.Contains(m.guidance(row), "Remote history") {
		t.Fatal("operation failure invisible")
	}
	m.attentionFirst = true
	if m.rows[m.visibleRows()[0]].Path != row.Path {
		t.Fatal("failed operation not prioritized")
	}
}
