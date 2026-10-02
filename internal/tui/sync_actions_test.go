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

type syncActionFake struct {
	*actionFake
	pushCalls atomic.Int32
	ffCalls   atomic.Int32
	pushPath  atomic.Value
	pushSpec  atomic.Value
	ffPath    atomic.Value
	ffCommit  atomic.Value
}

func newSyncActionFake(action app.Action, blockFetch bool) *syncActionFake {
	f := newActionFake(blockFetch)
	switch action {
	case app.Push:
		f.status.Ahead, f.status.Behind = 2, 0
	case app.Pull:
		f.status.Ahead, f.status.Behind = 0, 2
	}
	return &syncActionFake{actionFake: f}
}

func (f *syncActionFake) ResolveUpstream(context.Context, string, gitcli.Metadata) (gitcli.UpstreamTarget, error) {
	return gitcli.UpstreamTarget{
		Fetch:       gitcli.FetchTarget{Remote: "origin", URL: "https://alice:secret@example.invalid/project"},
		Branch:      "refs/heads/main",
		TrackingRef: "refs/remotes/origin/main",
	}, nil
}

func (f *syncActionFake) ResolvePush(_ context.Context, _ string, m gitcli.Metadata, upstream gitcli.UpstreamTarget) (gitcli.PushTarget, error) {
	return gitcli.PushTarget{
		Remote: upstream.Fetch.Remote, URL: upstream.Fetch.URL,
		SourceRef: "refs/heads/main", DestinationRef: upstream.Branch, HeadOID: m.Status.HeadOID,
	}, nil
}

func (f *syncActionFake) UpstreamCommit(context.Context, string, gitcli.UpstreamTarget) (string, error) {
	return "feedfacefeedfacefeedfacefeedfacefeedface", nil
}

func (f *syncActionFake) Push(_ context.Context, path string, target gitcli.PushTarget) error {
	f.pushCalls.Add(1)
	f.pushPath.Store(path)
	f.pushSpec.Store(target.SourceRef + " -> " + target.DestinationRef)
	return nil
}

func (f *syncActionFake) FastForward(_ context.Context, path, commit string) error {
	f.ffCalls.Add(1)
	f.ffPath.Store(path)
	f.ffCommit.Store(commit)
	return nil
}

func syncActionModel(fake *syncActionFake) *Model {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: []app.Row{
		{Repository: repository.Repository{Name: "first", Path: "/repos/first"}},
		{Repository: repository.Repository{Name: "second", Path: "/repos/second"}},
	}})
	m.EnableActions(app.NewActions(fake, 1))
	m.selected["/repos/first"] = true
	return m
}

func startSyncPreview(t *testing.T, m *Model, actionKey string) {
	t.Helper()
	cmd := pressAction(m, actionKey)
	if cmd == nil {
		t.Fatalf("%s did not prepare the reviewed fetch-scope preview", actionKey)
	}
	result := cmd()
	msg, ok := result.(previewMsg)
	if !ok {
		t.Fatalf("%s preview command returned %T", actionKey, result)
	}
	m.Update(msg)
}

func prepareSyncPreview(t *testing.T, m *Model) {
	t.Helper()
	cmd := pressAction(m, "enter")
	if cmd == nil {
		t.Fatal("first Enter did not start sync preflight")
	}
	result := cmd()
	msg, ok := result.(previewMsg)
	if !ok {
		t.Fatalf("preflight returned %T", result)
	}
	m.Update(msg)
}

func TestPushAndPullRequireFetchScopeAndSecondConfirmation(t *testing.T) {
	for _, tc := range []struct {
		key       string
		action    app.Action
		wantScope string
	}{
		{key: "p", action: app.Push, wantScope: "refs/heads/main -> refs/heads/main"},
		{key: "l", action: app.Pull, wantScope: "refs/heads/main -> refs/heads/main"},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			fake := newSyncActionFake(tc.action, false)
			m := syncActionModel(fake)
			startSyncPreview(t, m, tc.key)
			if m.preview == nil || len(m.preview.Targets) != 1 || m.preview.Targets[0].Action != app.Fetch {
				t.Fatalf("first preview = %#v", m.preview)
			}
			if fake.fetchCalls.Load() != 0 || fake.pushCalls.Load() != 0 || fake.ffCalls.Load() != 0 {
				t.Fatal("p/l preview performed a network mutation")
			}
			if !strings.Contains(m.View().Content, "Fetch scope before "+string(tc.action)) {
				t.Fatalf("fetch-scope review label missing: %q", m.View().Content)
			}
			pressAction(m, "esc")
			if m.preview != nil || fake.fetchCalls.Load() != 0 {
				t.Fatal("Esc on fetch-scope preview did not discard without fetching")
			}
			// Recreate the intent and confirm the reviewed fetch scope.
			startSyncPreview(t, m, tc.key)
			prepareSyncPreview(t, m)
			if fake.fetchCalls.Load() != 1 || fake.pushCalls.Load() != 0 || fake.ffCalls.Load() != 0 {
				t.Fatalf("first Enter calls fetch/push/ff = %d/%d/%d", fake.fetchCalls.Load(), fake.pushCalls.Load(), fake.ffCalls.Load())
			}
			if m.preview == nil || len(m.preview.Targets) != 1 {
				t.Fatalf("final preview missing: %#v", m.preview)
			}
			target := m.preview.Targets[0]
			if target.Action != tc.action || !target.Eligible || target.Path != "/repos/first" {
				t.Fatalf("final target = %#v", target)
			}
			if target.Scope != tc.wantScope {
				t.Fatalf("reviewed exact scope = %q, want %q", target.Scope, tc.wantScope)
			}
			if !strings.Contains(m.View().Content, "Target 1/1") || strings.Contains(m.View().Content, "alice:secret") {
				t.Fatalf("final preview omitted target or exposed secret: %q", m.View().Content)
			}
			pressAction(m, "esc")
			if m.preview != nil || fake.pushCalls.Load() != 0 || fake.ffCalls.Load() != 0 {
				t.Fatal("Esc on final sync preview executed or retained it")
			}
			replacement, err := m.actions.Plan(context.Background(), app.Fetch, []string{"/repos/first"})
			if err != nil {
				t.Fatalf("final preview remained active after Esc: %v", err)
			}
			m.actions.Discard(replacement.ID)
		})
	}
}

func TestSecondEnterExecutesExactPushOrFastForwardTarget(t *testing.T) {
	for _, tc := range []struct {
		key    string
		action app.Action
	}{
		{key: "p", action: app.Push},
		{key: "l", action: app.Pull},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			fake := newSyncActionFake(tc.action, false)
			m := syncActionModel(fake)
			startSyncPreview(t, m, tc.key)
			prepareSyncPreview(t, m)
			if m.preview == nil || m.preview.Targets[0].Action != tc.action {
				t.Fatalf("expected final %s preview, got %#v", tc.action, m.preview)
			}
			// Only the second Enter confirms the final push/integration.
			batch := pressAction(m, "enter")().(tea.BatchMsg)
			done, ok := batch[0]().(batchDoneMsg)
			if !ok {
				t.Fatalf("execution returned %T", done)
			}
			// Drain all already-buffered lifecycle events through the model.
			eventCmd := batch[1]
			for {
				msg := eventCmd()
				progress, ok := msg.(progressMsg)
				if !ok {
					break
				}
				_, eventCmd = m.Update(progress)
			}
			m.Update(done)
			if fake.fetchCalls.Load() != 1 {
				t.Fatalf("fetch count = %d, want one preflight fetch", fake.fetchCalls.Load())
			}
			if tc.action == app.Push {
				if fake.pushCalls.Load() != 1 || fake.ffCalls.Load() != 0 || fake.pushPath.Load() != "/repos/first" || fake.pushSpec.Load() != "refs/heads/main -> refs/heads/main" {
					t.Fatalf("push calls/path/scope = %d/%v/%v", fake.pushCalls.Load(), fake.pushPath.Load(), fake.pushSpec.Load())
				}
			} else if fake.ffCalls.Load() != 1 || fake.pushCalls.Load() != 0 || fake.ffPath.Load() != "/repos/first" || fake.ffCommit.Load() != "feedfacefeedfacefeedfacefeedfacefeedface" {
				t.Fatalf("fast-forward calls/path/commit = %d/%v/%v", fake.ffCalls.Load(), fake.ffPath.Load(), fake.ffCommit.Load())
			}
		})
	}
}

func TestEscCancelsSyncPreparationAndReleasesPlan(t *testing.T) {
	fake := newSyncActionFake(app.Push, true)
	m := syncActionModel(fake)
	startSyncPreview(t, m, "p")
	cmd := pressAction(m, "enter")
	finished := make(chan tea.Msg, 1)
	go func() { finished <- cmd() }()
	select {
	case <-fake.fetchStarted:
	case <-time.After(time.Second):
		t.Fatal("preflight fetch did not start")
	}
	if !m.preparing || m.preview != nil {
		t.Fatalf("model did not enter preparing state: preparing=%v preview=%#v", m.preparing, m.preview)
	}
	if cancelCmd := pressAction(m, "esc"); cancelCmd != nil {
		t.Fatal("Esc during preparation should request cancellation without quitting")
	}
	select {
	case msg := <-finished:
		preview, ok := msg.(previewMsg)
		if !ok {
			t.Fatalf("cancelled preparation returned %T", msg)
		}
		m.Update(preview)
	case <-time.After(time.Second):
		t.Fatal("preparation did not stop after cancellation")
	}
	if m.preparing || m.preview != nil || fake.pushCalls.Load() != 0 || fake.ffCalls.Load() != 0 {
		t.Fatalf("cancelled prep state: preparing=%v preview=%#v", m.preparing, m.preview)
	}
	// Actions must be available again after the cancelled preflight releases its reservation.
	preview, err := m.actions.Plan(context.Background(), app.Fetch, []string{"/repos/first"})
	if err != nil {
		t.Fatalf("cancelled preparation retained action reservation: %v", err)
	}
	m.actions.Discard(preview.ID)
}
