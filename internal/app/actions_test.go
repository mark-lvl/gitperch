package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gitcli "github.com/markkaghazgarian/gitperch/internal/git"
)

func TestActionsRequireExplicitSelectionAndPendingPreview(t *testing.T) {
	repo, _ := actionTestRepoWithRemote(t)
	actions := NewActions(gitcli.Service{}, 2)
	if _, err := actions.Plan(context.Background(), Fetch, nil); err == nil || !strings.Contains(err.Error(), "select repositories explicitly") {
		t.Fatalf("empty selection should fail, got %v", err)
	}
	preview, err := actions.Plan(context.Background(), Fetch, []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err == nil {
		t.Fatal("a second plan should not replace an active preview")
	}
	actions.Discard(0)
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err == nil {
		t.Fatal("discarding a different preview ID should leave the pending preview active")
	}
	actions.Discard(preview.ID)
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err != nil {
		t.Fatalf("discard should restore the ability to plan: %v", err)
	}
}

func TestActionsExecutePrivatePlanAfterPreviewMutation(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	worktree := actionTestAdvanceRemote(t, remote, "new from peer\n")
	wrapper := &recordingFetchGit{Service: gitcli.Service{}}
	actions := NewActions(wrapper, 1)
	preview, err := actions.Plan(context.Background(), Fetch, []string{repo})
	if err != nil || len(preview.Targets) != 1 || !preview.Targets[0].Eligible {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	preview.Targets[0].Remote = "attacker-controlled"
	preview.Targets[0].URL = "https://user:password@example.invalid"
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Succeeded {
		t.Fatalf("execution: %+v, %v", results, err)
	}
	if wrapper.target.Remote != "origin" {
		t.Fatalf("mutating the returned preview changed executable target: %+v", wrapper.target)
	}
	want := strings.TrimSpace(string(actionGit(t, worktree, "rev-parse", "HEAD")))
	got := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "refs/remotes/origin/main")))
	if got != want {
		t.Fatalf("fetch did not update tracking ref: got %s want %s", got, want)
	}
	if actions.LastFetch(repo).IsZero() {
		t.Fatal("successful fetch was not recorded")
	}
}

func TestActionsContinueAfterOneFetchFails(t *testing.T) {
	bad, _ := actionTestRepoWithRemote(t)
	good, goodRemote := actionTestRepoWithRemote(t)
	actionTestAdvanceRemote(t, goodRemote, "peer update\n")
	missing := filepath.Join(t.TempDir(), "missing.git")
	actionGit(t, bad, "remote", "set-url", "origin", missing)
	paths := []string{bad, good}
	actions := NewActions(gitcli.Service{}, 2)
	results, events := actionPlanAndExecute(t, actions, paths)
	byPath := actionResultsByPath(results)
	if byPath[bad].State != Failed || byPath[good].State != Succeeded {
		t.Fatalf("one failure should not stop another fetch: %+v", byPath)
	}
	if !hasEvent(events, bad, Failed) || !hasEvent(events, good, Succeeded) {
		t.Fatalf("missing terminal events: %+v", events)
	}
	if actions.LastFetch(good).IsZero() || !actions.LastFetch(bad).IsZero() {
		t.Fatal("fetch history should record only the successful repository")
	}
}

func TestActionsSkipNoOrAmbiguousRemote(t *testing.T) {
	t.Run("no remote", func(t *testing.T) {
		repo := actionTestRepo(t)
		results, events := actionPlanAndExecute(t, NewActions(gitcli.Service{}, 1), []string{repo})
		if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "no upstream remote") {
			t.Fatalf("unexpected result: %+v", results)
		}
		if !hasEvent(events, repo, Skipped) {
			t.Fatalf("missing skipped event: %+v", events)
		}
	})

	t.Run("ambiguous remotes", func(t *testing.T) {
		repo := actionTestRepo(t)
		first := filepath.Join(t.TempDir(), "first.git")
		second := filepath.Join(t.TempDir(), "second.git")
		actionBareRemote(t, first)
		actionBareRemote(t, second)
		actionGit(t, repo, "remote", "add", "one", first)
		actionGit(t, repo, "remote", "add", "two", second)
		actions := NewActions(gitcli.Service{}, 1)
		preview, err := actions.Plan(context.Background(), Fetch, []string{repo})
		if err != nil || len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "sole unambiguous remote") {
			t.Fatalf("ambiguous remotes should be visibly ineligible: %+v, %v", preview, err)
		}
		results, err := actions.Execute(context.Background(), preview.ID, nil)
		if err != nil || len(results) != 1 || results[0].State != Skipped {
			t.Fatalf("ambiguous remotes should remain visibly skipped at execution: %+v, %v", results, err)
		}
	})
}

func TestActionsSkipUnusualFetchRefspecs(t *testing.T) {
	for _, tc := range []struct{ name, refspec string }{
		{"local branch", "+refs/heads/*:refs/heads/*"},
		{"tag", "+refs/tags/*:refs/tags/*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			actionGit(t, repo, "config", "--unset-all", "remote.origin.fetch")
			actionGit(t, repo, "config", "--add", "remote.origin.fetch", tc.refspec)
			preview, err := NewActions(gitcli.Service{}, 1).Plan(context.Background(), Fetch, []string{repo})
			if err != nil || len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "unsupported fetch refspec") {
				t.Fatalf("unsafe refspec should be skipped: %+v, %v", preview, err)
			}
		})
	}
}

func TestActionsSkipRepositoriesChangedAfterPreview(t *testing.T) {
	for _, change := range []string{"branch", "head", "config"} {
		t.Run(change, func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			actions := NewActions(gitcli.Service{}, 1)
			preview, err := actions.Plan(context.Background(), Fetch, []string{repo})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "branch":
				actionGit(t, repo, "checkout", "-b", "review-branch")
			case "head":
				actionTestWrite(t, filepath.Join(repo, "changed"), "new commit\n")
				actionTestCommit(t, repo, "new head")
			case "config":
				actionGit(t, repo, "config", "remote.origin.url", filepath.Join(t.TempDir(), "changed-remote.git"))
			}
			results, err := actions.Execute(context.Background(), preview.ID, nil)
			if err != nil || len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "changed since preview") {
				t.Fatalf("changed %s should be skipped: %+v, %v", change, results, err)
			}
		})
	}
}

func TestActionsSerializeLinkedWorktreesByCommonDirectory(t *testing.T) {
	repo, _ := actionTestRepoWithRemote(t)
	linked := filepath.Join(t.TempDir(), "linked")
	actionGit(t, repo, "worktree", "add", "-b", "linked-branch", linked)
	baseMetadata, err := (gitcli.Runner{}).Metadata(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	linkedMetadata, err := (gitcli.Runner{}).Metadata(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if baseMetadata.CommonDir != linkedMetadata.CommonDir {
		t.Fatalf("linked worktrees have different common dirs: %q, %q", baseMetadata.CommonDir, linkedMetadata.CommonDir)
	}
	wrapper := &serialFetchGit{}
	actions := NewActions(wrapper, 2)
	preview, err := actions.Plan(context.Background(), Fetch, []string{linked, repo})
	if err != nil {
		t.Fatal(err)
	}
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("execution: %+v, %v", results, err)
	}
	for _, result := range results {
		if result.State != Succeeded {
			t.Fatalf("worktree fetch failed: %+v", results)
		}
	}
	if wrapper.peak != 1 {
		t.Fatalf("linked worktree fetches overlapped: peak=%d", wrapper.peak)
	}
}

func TestActionsClassifyCancellationAndTimeout(t *testing.T) {
	repo, _ := actionTestRepoWithRemote(t)
	deadlineActions := NewActions(fetchFailureGit{Service: gitcli.Service{}, err: context.DeadlineExceeded}, 1)
	preview, err := deadlineActions.Plan(context.Background(), Fetch, []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	results, err := deadlineActions.Execute(context.Background(), preview.ID, func(event Event) { events = append(events, event) })
	if err != nil || results[0].State != Cancelled || !hasEvent(events, repo, Queued) || !hasEvent(events, repo, Running) || !hasEvent(events, repo, Cancelled) {
		t.Fatalf("deadline classification/events: %+v, %+v, %v", results, events, err)
	}

	cancelledActions := NewActions(gitcli.Service{}, 1)
	preview, err = cancelledActions.Plan(context.Background(), Fetch, []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err = cancelledActions.Execute(ctx, preview.ID, nil)
	if !errors.Is(err, context.Canceled) || len(results) != 1 || results[0].State != Cancelled {
		t.Fatalf("pre-cancelled batch classification: %+v, %v", results, err)
	}
}

func actionPlanAndExecute(t *testing.T, actions *Actions, paths []string) ([]Event, []Event) {
	t.Helper()
	preview, err := actions.Plan(context.Background(), Fetch, paths)
	if err != nil {
		t.Fatal(err)
	}
	var eventsMu sync.Mutex
	var emitted []Event
	results, err := actions.Execute(context.Background(), preview.ID, func(event Event) {
		eventsMu.Lock()
		emitted = append(emitted, event)
		eventsMu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	return results, emitted
}

func actionResultsByPath(events []Event) map[string]Event {
	byPath := make(map[string]Event, len(events))
	for _, event := range events {
		byPath[event.Path] = event
	}
	return byPath
}

func hasEvent(events []Event, path string, state State) bool {
	for _, event := range events {
		if event.Path == path && event.State == state {
			return true
		}
	}
	return false
}

func actionTestRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "no-global-config"))
	root := t.TempDir()
	actionGit(t, root, "init", "-b", "main")
	actionGit(t, root, "config", "user.name", "Action Test")
	actionGit(t, root, "config", "user.email", "action-test@example.invalid")
	actionGit(t, root, "config", "commit.gpgsign", "false")
	return root
}

func actionTestRepoWithRemote(t *testing.T) (string, string) {
	t.Helper()
	repo := actionTestRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	actionBareRemote(t, remote)
	actionTestWrite(t, filepath.Join(repo, "tracked"), "initial\n")
	actionTestCommit(t, repo, "initial")
	actionGit(t, repo, "remote", "add", "origin", remote)
	actionGit(t, repo, "push", "--set-upstream", "origin", "main")
	return repo, remote
}

func actionTestAdvanceRemote(t *testing.T, remote, content string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "peer")
	cmd := exec.Command("git", "clone", remote, clone)
	cmd.Env = actionTestGitEnv(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v: %s", err, output)
	}
	actionGit(t, clone, "config", "user.name", "Peer Test")
	actionGit(t, clone, "config", "user.email", "peer-test@example.invalid")
	actionTestWrite(t, filepath.Join(clone, "peer-file"), content)
	actionTestCommit(t, clone, "peer update")
	actionGit(t, clone, "push", "origin", "main")
	return clone
}

func actionBareRemote(t *testing.T, remote string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		t.Fatal(err)
	}
	actionGit(t, filepath.Dir(remote), "init", "--bare", remote)
	actionGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
}

func actionTestWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func actionTestCommit(t *testing.T, path, message string) {
	t.Helper()
	actionGit(t, path, "add", "--all")
	actionGit(t, path, "commit", "-m", message)
}

func actionGit(t *testing.T, path string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	cmd.Env = actionTestGitEnv(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return output
}

func actionTestGitEnv(t *testing.T) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "GIT_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "no-global-config"))
}

type recordingFetchGit struct {
	gitcli.Service
	mu     sync.Mutex
	target gitcli.FetchTarget
}

func (g *recordingFetchGit) Fetch(ctx context.Context, path string, target gitcli.FetchTarget) error {
	g.mu.Lock()
	g.target = target
	g.mu.Unlock()
	return g.Service.Fetch(ctx, path, target)
}

type fetchFailureGit struct {
	gitcli.Service
	err error
}

func (g fetchFailureGit) Fetch(context.Context, string, gitcli.FetchTarget) error { return g.err }

type serialFetchGit struct {
	gitcli.Runner
	mu     sync.Mutex
	active int
	peak   int
}

func (g *serialFetchGit) Fetch(ctx context.Context, path string, target gitcli.FetchTarget) error {
	g.mu.Lock()
	g.active++
	if g.active > g.peak {
		g.peak = g.active
	}
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.active--
		g.mu.Unlock()
	}()
	time.Sleep(40 * time.Millisecond)
	return g.Runner.Fetch(ctx, path, target)
}
