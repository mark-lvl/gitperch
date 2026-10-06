package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/repository"
)

func TestSpinnerRunsOnlyWhileBusy(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30}); cmd != nil || m.spinning {
		t.Fatal("idle model scheduled the spinner")
	}
	m.running = true
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30}); cmd == nil || !m.spinning {
		t.Fatal("busy model did not schedule the spinner")
	}
	// A second message while a tick is pending must not schedule another.
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30}); cmd != nil {
		t.Fatal("spinner scheduled twice")
	}
	if _, cmd := m.Update(spinnerMsg{}); cmd == nil || m.spinnerFrame != 1 || !m.spinning {
		t.Fatalf("spinner did not advance: frame=%d", m.spinnerFrame)
	}
	m.running = false
	if _, cmd := m.Update(spinnerMsg{}); cmd != nil || m.spinning || m.spinnerFrame != 0 {
		t.Fatal("spinner kept ticking after the work finished")
	}
}

func TestSpinnerFramesFollowIconMode(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.iconMode = "ascii"
	seen := []string{}
	for m.spinnerFrame = 0; m.spinnerFrame < 5; m.spinnerFrame++ {
		seen = append(seen, m.spinner())
	}
	if strings.Join(seen, "") != `|/-\|` {
		t.Fatalf("ascii frames = %q", seen)
	}
	m.iconMode = "unicode"
	m.spinnerFrame = 2
	if m.spinner() != "⠹" {
		t.Fatalf("unicode frame = %q", m.spinner())
	}
}

func TestSpinnerShowsWhereWorkIsRunning(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.width, m.height = 120, 30
	m.applySnapshot(app.Snapshot{Rows: []app.Row{
		{Repository: repository.Repository{Name: "first", Path: "/repos/first"}},
		{Repository: repository.Repository{Name: "second", Path: "/repos/second"}},
	}})
	m.running = true
	m.actionTotal = 2
	m.spinnerFrame = 1
	m.results = map[string]app.Event{"/repos/first": {Path: "/repos/first", State: app.Running}}
	content := m.View().Content
	for _, want := range []string{"⠙ running", "⠙ Batch running · 0/2 finished"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in %q", want, content)
		}
	}
	if strings.Count(content, "⠙ Batch running") != 2 {
		t.Fatalf("header and footer should both spin: %q", content)
	}
	m.running = false
	m.loading = true
	if content := m.View().Content; !strings.Contains(content, "⠙ Refreshing local status…") || !strings.Contains(content, "⠙ refreshing") {
		t.Fatalf("refresh spinner missing: %q", content)
	}
}
