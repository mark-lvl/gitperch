package tui

import (
	"context"
	"strings"
	"testing"

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
