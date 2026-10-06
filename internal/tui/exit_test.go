package tui

import (
	"context"
	"testing"

	"github.com/markkaghazgarian/gitperch/internal/app"
	"github.com/markkaghazgarian/gitperch/internal/repository"
)

func TestDashboardExitCodes(t *testing.T) {
	m := New(context.Background(), func(context.Context) (app.Snapshot, error) { return app.Snapshot{}, nil }, true)
	if m.ExitCode() != 0 {
		t.Fatal("empty successful dashboard must exit zero")
	}
	m.warnings = []string{"missing root"}
	if m.ExitCode() != 1 {
		t.Fatal("root warnings must indicate partial failure")
	}
	m.warnings = nil
	m.rows = []app.Row{{Status: repository.Status{Error: "broken repository"}}}
	if m.ExitCode() != 1 {
		t.Fatal("inspection error must indicate partial failure")
	}
	m.rows = nil
	m.results = map[string]app.Event{}
	m.Update(batchDoneMsg{results: []app.Event{{Path: "/repo", State: app.Failed}}})
	pressAction(m, "esc")
	if m.ExitCode() != 1 {
		t.Fatal("dismissing results must not erase operation failure")
	}
	pressAction(m, "ctrl+c")
	if m.ExitCode() != 130 {
		t.Fatal("Ctrl+C quit must report interruption")
	}
}

func TestCancellationRequestIsNotImmediateInterruption(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.running = true
	pressAction(m, "ctrl+c")
	if m.interrupted {
		t.Fatal("active batch cancellation must not quit dashboard")
	}
}
