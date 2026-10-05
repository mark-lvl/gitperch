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

func TestFetchRunsWithoutConfirmation(t *testing.T) {
	fake := newActionFake(false)
	m := actionModel(fake, "/repos/first", "/repos/second")
	cmd := pressAction(m, "f")
	if cmd == nil {
		t.Fatal("fetch plan command missing")
	}
	if fake.fetchCalls.Load() != 0 {
		t.Fatal("pressing f fetched before planning")
	}
	_, run := m.Update(cmd().(previewMsg))
	if m.preview != nil || !m.running || run == nil {
		t.Fatalf("fetch asked for confirmation: preview=%#v running=%v", m.preview, m.running)
	}
	batch := run().(tea.BatchMsg)
	done := batch[0]() // events are buffered, so the batch finishes first
	eventCmd := batch[1]
	for {
		progress, ok := eventCmd().(progressMsg)
		if !ok {
			break
		}
		_, eventCmd = m.Update(progress)
	}
	m.Update(done)
	if fake.fetchCalls.Load() != 2 {
		t.Fatalf("fetch calls = %d, want 2", fake.fetchCalls.Load())
	}
	if m.message != "Fetch finished · 2 succeeded" {
		t.Fatalf("message = %q", m.message)
	}
}

func TestEnterExecutesPrivatePlanForSelectedRowsAndKeepsEvents(t *testing.T) {
	fake := newActionFake(true)
	m := actionModel(fake, "/repos/first")
	previewCmd := pressAction(m, "f")
	preview := previewCmd().(previewMsg)
	if len(preview.preview.Targets) != 1 || preview.preview.Targets[0].Path != "/repos/first" {
		t.Fatalf("selection preview = %#v", preview.preview.Targets)
	}
	// The display object cannot retarget the stored executable plan.
	preview.preview.Targets[0].Path = "/repos/second"
	preview.preview.Targets[0].URL = "https://evil.invalid/retargeted"
	_, batchCmd := m.Update(preview)
	if batchCmd == nil {
		t.Fatal("fetch returned no execution command")
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
	_, run := m.Update(preview)
	batch := run().(tea.BatchMsg)
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

func TestImplicitPushTargetDoesNotBecomeSelection(t *testing.T) {
	fake := newActionFake(false)
	m := actionModel(fake)
	push := func(want string) {
		t.Helper()
		cmd := pressAction(m, "p")
		if cmd == nil {
			t.Fatal("push plan command missing")
		}
		if len(m.selected) != 0 {
			t.Fatalf("highlighted row became selected: %v", m.selected)
		}
		scope := cmd().(previewMsg)
		if len(scope.preview.Targets) != 1 || scope.preview.Targets[0].Path != want {
			t.Fatalf("push targeted %#v, want %s", scope.preview.Targets, want)
		}
		// This fake cannot push, so the preflight ends without a popup.
		_, preflight := m.Update(scope)
		m.Update(preflight())
		if m.preview != nil || len(m.selected) != 0 {
			t.Fatalf("preview=%#v selected=%v", m.preview, m.selected)
		}
	}
	push("/repos/first")
	pressAction(m, "j")
	push("/repos/second")
}
