package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"repodash/internal/repository"
)

type actionFake struct {
	metadataCalls atomic.Int32
	resolveCalls  atomic.Int32
	fetchCalls    atomic.Int32
	fetchPath     atomic.Value
	fetchStarted  chan struct{}
	releaseFetch  chan struct{}
	blockFetch    bool
	status        repository.Status
}

func newActionFake(block bool) *actionFake {
	return &actionFake{
		fetchStarted: make(chan struct{}, 8),
		releaseFetch: make(chan struct{}),
		blockFetch:   block,
		status: repository.Status{
			Branch: "main", HeadOID: "0123456789abcdef", ComparisonKnown: true,
		},
	}
}

func (f *actionFake) Metadata(_ context.Context, path string) (gitcli.Metadata, error) {
	f.metadataCalls.Add(1)
	status := f.status
	status.CommonDir = "/common/" + path
	return gitcli.Metadata{Status: status, CommonDir: status.CommonDir, ConfigHash: "fixed-config"}, nil
}

func (f *actionFake) ResolveFetch(context.Context, string, gitcli.Metadata) (gitcli.FetchTarget, error) {
	f.resolveCalls.Add(1)
	return gitcli.FetchTarget{Remote: "origin", URL: "https://alice:secret@example.invalid/project"}, nil
}

func (f *actionFake) Fetch(ctx context.Context, path string, _ gitcli.FetchTarget) error {
	f.fetchCalls.Add(1)
	f.fetchPath.Store(path)
	f.fetchStarted <- struct{}{}
	if !f.blockFetch {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.releaseFetch:
		return nil
	}
}

func (f *actionFake) Inspect(context.Context, string) repository.Status { return f.status }

func actionModel(fake *actionFake, selected ...string) *Model {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: []app.Row{
		{Repository: repository.Repository{Name: "first", Path: "/repos/first"}},
		{Repository: repository.Repository{Name: "second", Path: "/repos/second"}},
	}})
	m.EnableActions(app.NewActions(fake, 1))
	for _, path := range selected {
		m.selected[path] = true
	}
	return m
}

func pressAction(m *Model, text string) tea.Cmd {
	return m.key(tea.KeyPressMsg{Code: []rune(text)[0], Text: text})
}

func TestFetchRequiresExplicitSelection(t *testing.T) {
	fake := newActionFake(false)
	m := actionModel(fake)
	if cmd := pressAction(m, "f"); cmd != nil {
		t.Fatal("fetch without selection returned a command")
	}
	if fake.metadataCalls.Load() != 0 || fake.fetchCalls.Load() != 0 || m.preview != nil {
		t.Fatal("fetch without selection touched repository service")
	}
}

func TestFetchPreviewDoesNotExecuteAndEscapeDiscards(t *testing.T) {
	fake := newActionFake(false)
	m := actionModel(fake, "/repos/first", "/repos/second")
	cmd := pressAction(m, "f")
	if cmd == nil {
		t.Fatal("fetch preview command missing")
	}
	msg, ok := cmd().(previewMsg)
	if !ok {
		t.Fatalf("preview command returned %T", cmd())
	}
	m.Update(msg)
	if m.preview == nil || len(m.preview.Targets) != 2 {
		t.Fatalf("preview targets = %#v", m.preview)
	}
	if fake.fetchCalls.Load() != 0 {
		t.Fatal("planning preview fetched before confirmation")
	}
	view := m.View().Content
	if !strings.Contains(view, "2 targets") || !strings.Contains(view, "Target 1/2") {
		t.Fatalf("preview target count/details missing: %q", view)
	}
	if strings.Contains(view, "alice:secret") || strings.Contains(view, "\x1b") {
		t.Fatal("preview exposed credential or terminal control character")
	}
	pressAction(m, "j")
	if !strings.Contains(m.View().Content, "Target 2/2") {
		t.Fatalf("preview j did not advance target: %q", m.View().Content)
	}
	pressAction(m, "k")
	if !strings.Contains(m.View().Content, "Target 1/2") {
		t.Fatalf("preview k did not move target back: %q", m.View().Content)
	}
	pressAction(m, "esc")
	if m.preview != nil || fake.fetchCalls.Load() != 0 {
		t.Fatal("Escape did not discard preview without fetching")
	}
	if _, err := m.actions.Plan(context.Background(), app.Fetch, []string{"/repos/first"}); err != nil {
		t.Fatalf("discarded preview remained active: %v", err)
	}
}

func TestEnterExecutesPrivatePlanForSelectedRowsAndKeepsEvents(t *testing.T) {
	fake := newActionFake(true)
	m := actionModel(fake, "/repos/first")
	previewCmd := pressAction(m, "f")
	preview := previewCmd().(previewMsg)
	m.Update(preview)
	if len(m.preview.Targets) != 1 || m.preview.Targets[0].Path != "/repos/first" {
		t.Fatalf("selection preview = %#v", m.preview.Targets)
	}
	// The display object cannot retarget the stored executable plan.
	m.preview.Targets[0].Path = "/repos/second"
	m.preview.Targets[0].URL = "https://evil.invalid/retargeted"
	batchCmd := pressAction(m, "enter")
	if batchCmd == nil {
		t.Fatal("confirmation returned no execution command")
	}
	batch, ok := batchCmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("confirmation command = %T, batch length %d", batchCmd(), len(batch))
	}
	finished := make(chan tea.Msg, 1)
	go func() { finished <- batch[0]() }()

	var states []app.State
	eventCmd := batch[1]
	for len(states) < 2 { // queued, then running; keep Fetch blocked until reviewed.
		msg := eventCmd()
		progress, ok := msg.(progressMsg)
		if !ok {
			t.Fatalf("expected progress event, got %T", msg)
		}
		states = append(states, progress.event.State)
		_, eventCmd = m.Update(progress)
	}
	if states[0] != app.Queued || states[1] != app.Running {
		t.Fatalf("initial events = %v", states)
	}
	select {
	case <-fake.fetchStarted:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start after Enter")
	}
	if fake.fetchCalls.Load() != 1 {
		t.Fatalf("fetch calls = %d, want one", fake.fetchCalls.Load())
	}
	close(fake.releaseFetch)
	msg := eventCmd()
	progress, ok := msg.(progressMsg)
	if !ok || progress.event.State != app.Succeeded {
		t.Fatalf("completion event = %#v", msg)
	}
	states = append(states, progress.event.State)
	m.Update(progress)
	if states[2] != app.Succeeded {
		t.Fatalf("event sequence = %v", states)
	}
	if got := fake.fetchPath.Load(); got != "/repos/first" {
		t.Fatalf("fetched path = %v, want selected repository", got)
	}
	if m.actions.LastFetch("/repos/first").IsZero() {
		t.Fatal("successful fetch did not record LastFetch")
	}
	var done batchDoneMsg
	select {
	case result := <-finished:
		var ok bool
		done, ok = result.(batchDoneMsg)
		if !ok {
			t.Fatalf("execution result = %T", result)
		}
	case <-time.After(time.Second):
		t.Fatal("batch did not finish")
	}
	m.Update(done)
	if m.running || m.results["/repos/first"].State != app.Succeeded {
		t.Fatalf("batch results not retained: running=%v results=%#v", m.running, m.results)
	}
	pressAction(m, "d")
	if !strings.Contains(m.View().Content, "/repos/first: succeeded") {
		t.Fatalf("success result not visible in details: %q", m.View().Content)
	}
}

func TestQuitDuringFetchRequestsCancellationUntilResultsArrive(t *testing.T) {
	fake := newActionFake(true)
	m := actionModel(fake, "/repos/first")
	preview := pressAction(m, "f")().(previewMsg)
	m.Update(preview)
	batch := pressAction(m, "enter")().(tea.BatchMsg)
	finished := make(chan tea.Msg, 1)
	go func() { finished <- batch[0]() }()
	progress, ok := batch[1]().(progressMsg)
	if !ok || progress.event.State != app.Queued {
		t.Fatalf("first event = %#v", progress)
	}
	_, nextEvent := m.Update(progress)
	progress, ok = nextEvent().(progressMsg)
	if !ok || progress.event.State != app.Running {
		t.Fatalf("second event = %#v", progress)
	}
	m.Update(progress)
	select {
	case <-fake.fetchStarted:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start")
	}
	if cmd := pressAction(m, "q"); cmd != nil {
		t.Fatal("q during running batch must request cancellation, not quit")
	}
	if m.message != "Cancellation requested; waiting for outcomes" {
		t.Fatalf("cancellation message = %q", m.message)
	}
	var done batchDoneMsg
	select {
	case result := <-finished:
		var ok bool
		done, ok = result.(batchDoneMsg)
		if !ok {
			t.Fatalf("execution result = %T", result)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled batch did not finish")
	}
	m.Update(done)
	if m.running || m.results["/repos/first"].State != app.Cancelled {
		t.Fatalf("cancellation result not retained: %#v", m.results)
	}
	if cmd := pressAction(m, "q"); cmd == nil {
		t.Fatal("q after result arrival should quit normally")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q returned a non-quit command after results arrived")
	}
}
