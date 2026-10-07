package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/repository"
)

func attentionRows() []app.Row {
	synced := repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}
	ahead, behind, dirty, conflict := synced, synced, synced, synced
	ahead.Ahead = 1
	behind.Behind = 2
	dirty.Changes = 1
	conflict.Changes, conflict.Conflicts, conflict.Operation = 1, 1, "MERGE_HEAD"
	specs := []struct {
		name   string
		status repository.Status
	}{
		{"a-behind", behind},
		{"b-clean", synced},
		{"c-detached", repository.Status{Detached: true}},
		{"d-ahead", ahead},
		{"e-conflict", conflict},
		{"f-dirty", dirty},
		{"g-orphaned", repository.Status{Detached: true, HeadUnreferenced: true}},
	}
	var rows []app.Row
	for _, spec := range specs {
		rows = append(rows, app.Row{Repository: repository.Repository{Name: spec.name, Path: "/repos/" + spec.name}, Status: spec.status})
	}
	return rows
}

func TestAttentionOrderIsLevelThenName(t *testing.T) {
	m := New(context.Background(), nil, true)
	// Snapshot order must not matter: reverse it.
	rows := attentionRows()
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	m.applySnapshot(app.Snapshot{Rows: rows})
	m.attentionFirst = true
	if got := strings.Join(visibleNames(m), ","); got != "e-conflict,d-ahead,f-dirty,g-orphaned,a-behind,b-clean,c-detached" {
		t.Fatalf("attention order: %s", got)
	}
	m.setScope(1)
	if got := strings.Join(visibleNames(m), ","); got != "e-conflict,d-ahead,f-dirty,g-orphaned,a-behind" {
		t.Fatalf("focus keeps clean and referenced detached rows out: %s", got)
	}
	// A failed action is session state the snapshot cannot carry.
	m.setScope(0)
	m.results = map[string]app.Event{"/repos/b-clean": {Path: "/repos/b-clean", State: app.Failed}}
	if got := visibleNames(m)[:2]; got[0] != "b-clean" || got[1] != "e-conflict" {
		t.Fatalf("failed action should rank critical: %v", got)
	}
}

func TestDetachedHeadIsQuietOnlyWhenARefContainsIt(t *testing.T) {
	m := New(context.Background(), nil, true)
	rows := attentionRows()
	if label, color := m.primaryStatus(rows[2]); label != "detached" || color != muted {
		t.Fatalf("referenced detached HEAD: %q %s", label, color)
	}
	if label, color := m.primaryStatus(rows[6]); label != "! detached" || color != amber {
		t.Fatalf("unreferenced detached HEAD: %q %s", label, color)
	}
}

func TestDetailsListAttentionReasons(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.width, m.height = 110, 35
	m.applySnapshot(app.Snapshot{Rows: attentionRows()})
	m.attentionFirst = true
	m.details = true
	m.highlight = 0 // e-conflict
	text := ansi.Strip(m.View().Content)
	for _, want := range []string{"Needs attention · critical", "- 1 conflicted file", "- merge in progress", "Next step"} {
		if !strings.Contains(text, want) {
			t.Fatalf("details missing %q:\n%s", want, text)
		}
	}
	m.highlight = 5 // b-clean
	if text := ansi.Strip(m.View().Content); strings.Contains(text, "Needs attention") || strings.Contains(text, "Next step") {
		t.Fatalf("clean repository shows attention:\n%s", text)
	}
}

func TestLinkedWorktreeLifecycleIsShownAndExplained(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	linked := func(name string, idle time.Duration, merged bool) app.Row {
		s := repository.Status{Branch: name, Upstream: "origin/" + name, ComparisonKnown: true, HeadOID: strings.Repeat("a", 40), InspectedAt: now, LastActivity: now.Add(-idle)}
		return app.Row{Repository: repository.Repository{Name: name, Path: "/repos/main/.worktrees/" + name}, Status: s,
			Worktree: &app.WorktreeInfo{Linked: true, MainPath: "/repos/main", Integration: &app.Integration{Base: "origin/main", Merged: merged}}}
	}
	main := app.Row{Repository: repository.Repository{Name: "main", Path: "/repos/main"}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true, InspectedAt: now, LastActivity: now.Add(-60 * 24 * time.Hour)},
		Worktree: &app.WorktreeInfo{Main: true, MainPath: "/repos/main"}}
	done, idle, busy := linked("done", 6*24*time.Hour, true), linked("idle", 21*24*time.Hour, false), linked("busy", time.Hour, true)
	m := New(context.Background(), nil, true)
	for _, tc := range []struct {
		row          app.Row
		label, color string
	}{
		{main, m.symbols().clean + " clean", success},
		{done, m.symbols().clean + " finished?", success},
		{idle, "idle 21d", amber},
		{busy, m.symbols().clean + " clean", success},
	} {
		if label, color := m.primaryStatus(tc.row); label != tc.label || color != tc.color {
			t.Errorf("%s: %q %s, want %q %s", tc.row.Name, label, color, tc.label, tc.color)
		}
	}

	m.applySnapshot(app.Snapshot{Rows: []app.Row{main, done, idle, busy}})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 45})
	m.attentionFirst = true
	if got := strings.Join(visibleNames(m), ","); got != "main" {
		t.Fatalf("collapsed group: %s", got)
	}
	if badge := strings.Join(m.groupBadges(main), " | "); !strings.Contains(badge, "2 need attention") {
		t.Fatalf("badge should count finished and idle worktrees: %s", badge)
	}
	m.expanded["/repos/main"] = true
	m.highlight = slices.Index(visibleNames(m), "done")
	m.details, m.detailTab = true, 3
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Lifecycle · likely finished", "- clean working tree", "- nothing to push to origin/done", "- merged into origin/main", "- no HEAD activity for 6 days", "A suggestion only: Clean up (c)", "clean · finished?", "clean · idle 21d"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	m.detailTab = 0
	view = ansi.Strip(m.View().Content)
	for _, want := range []string{"Needs attention · medium", "linked worktree looks finished: merged into origin/main, no HEAD activity for 6 days"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	if got := nextStep(done); !strings.HasPrefix(got, "Looks finished. Press c") {
		t.Fatalf("finished next step: %s", got)
	}
	if got := nextStep(idle); !strings.HasPrefix(got, "Idle and clean.") {
		t.Fatalf("idle next step: %s", got)
	}
	m.highlight = slices.Index(visibleNames(m), "main")
	m.detailTab = 3
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "Lifecycle") {
		t.Fatalf("the main worktree has no lifecycle:\n%s", view)
	}
}
