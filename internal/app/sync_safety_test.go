package app

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gitcli "repodash/internal/git"
)

func TestPushExcludesImplicitTagsAndUnrelatedBranches(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	initial := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
	actionGit(t, repo, "branch", "unrelated")
	actionGit(t, repo, "push", "origin", "unrelated")
	actionTestWrite(t, filepath.Join(repo, "outgoing"), "reviewed commit\n")
	actionTestCommit(t, repo, "outgoing")
	actionGit(t, repo, "tag", "-a", "private-tag", "-m", "do not follow")
	actionGit(t, repo, "config", "push.followTags", "true")
	actionGit(t, repo, "config", "push.recurseSubmodules", "on-demand")
	actionGit(t, repo, "config", "--add", "remote.origin.push", "main:main")
	actionGit(t, repo, "config", "--add", "remote.origin.push", "unrelated:unrelated")
	a := NewActions(gitcli.Service{}, 2)
	p, err := a.Plan(context.Background(), Push, []string{repo})
	if err != nil || !p.Targets[0].Eligible {
		t.Fatalf("preview: %+v, %v", p, err)
	}
	result, err := a.Execute(context.Background(), p.ID, nil)
	if err != nil || result[0].State != Succeeded {
		t.Fatalf("push: %+v, %v", result, err)
	}
	if got := strings.TrimSpace(string(actionGit(t, remote, "rev-parse", "refs/heads/main"))); got != p.Targets[0].Commit {
		t.Fatalf("remote HEAD = %s, reviewed %s", got, p.Targets[0].Commit)
	}
	for _, path := range []string{repo, remote} {
		if got := strings.TrimSpace(string(actionGit(t, path, "rev-parse", "refs/heads/unrelated"))); got != initial {
			t.Fatalf("unrelated branch changed in %s", path)
		}
	}
	if err := exec.Command("git", "-C", remote, "show-ref", "--verify", "refs/tags/private-tag").Run(); err == nil {
		t.Fatal("implicit annotated tag was pushed")
	}
}

func TestSyncRefusesOperationsAndConflicts(t *testing.T) {
	for _, action := range []Action{Push, Pull} {
		for _, state := range []string{"operation", "conflict"} {
			t.Run(string(action)+"/"+state, func(t *testing.T) {
				repo, _ := actionTestRepoWithRemote(t)
				if state == "operation" {
					actionTestWrite(t, filepath.Join(repo, ".git", "MERGE_HEAD"), string(actionGit(t, repo, "rev-parse", "HEAD")))
				} else {
					actionGit(t, repo, "checkout", "-b", "conflicting")
					actionTestWrite(t, filepath.Join(repo, "tracked"), "other\n")
					actionTestCommit(t, repo, "other")
					actionGit(t, repo, "checkout", "main")
					actionTestWrite(t, filepath.Join(repo, "tracked"), "main\n")
					actionTestCommit(t, repo, "main")
					cmd := exec.Command("git", "-C", repo, "merge", "conflicting")
					cmd.Env = actionTestGitEnv(t)
					if err := cmd.Run(); err == nil {
						t.Fatal("expected conflict")
					}
				}
				p, err := NewActions(gitcli.Service{}, 1).Plan(context.Background(), action, []string{repo})
				if err != nil || p.Targets[0].Eligible || !strings.Contains(p.Targets[0].Reason, state) {
					t.Fatalf("unsafe state accepted: %+v, %v", p, err)
				}
			})
		}
	}
}

type uncertainPushGit struct {
	gitcli.Service
	calls int
	err   error
}

func (g *uncertainPushGit) Push(context.Context, string, gitcli.PushTarget) error {
	g.calls++
	return g.err
}

func (g *uncertainPushGit) FastForward(context.Context, string, string) error {
	g.calls++
	return g.err
}

func TestStartedIntegrationUncertaintyNeverRetries(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled, gitcli.ErrOutputLimit} {
		t.Run(cause.Error(), func(t *testing.T) {
			repo, remote := actionTestRepoWithRemote(t)
			actionTestAdvanceRemote(t, remote, "reviewed update\n")
			g := &uncertainPushGit{err: cause}
			a := NewActions(g, 1)
			p, err := a.Plan(context.Background(), Pull, []string{repo})
			if err != nil || !p.Targets[0].Eligible {
				t.Fatalf("%+v, %v", p, err)
			}
			results, err := a.Execute(context.Background(), p.ID, nil)
			if err != nil || results[0].State != OutcomeUnknown || g.calls != 1 {
				t.Fatalf("%+v, %v, calls %d", results, err, g.calls)
			}
		})
	}
}

func TestStartedPushUncertaintyNeverRetries(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled, gitcli.ErrOutputLimit} {
		t.Run(cause.Error(), func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			actionTestWrite(t, filepath.Join(repo, "outgoing"), "commit\n")
			actionTestCommit(t, repo, "outgoing")
			g := &uncertainPushGit{err: cause}
			a := NewActions(g, 1)
			p, err := a.Plan(context.Background(), Push, []string{repo})
			if err != nil || !p.Targets[0].Eligible {
				t.Fatalf("%+v, %v", p, err)
			}
			results, err := a.Execute(context.Background(), p.ID, nil)
			if err != nil || results[0].State != OutcomeUnknown || g.calls != 1 {
				t.Fatalf("uncertain push retried or misreported: %+v, %v, calls %d", results, err, g.calls)
			}
			if _, err := a.Execute(context.Background(), p.ID, nil); err == nil || g.calls != 1 {
				t.Fatal("consumed plan retried")
			}
		})
	}
}

func TestCancelledBeforePushDoesNotStart(t *testing.T) {
	repo, _ := actionTestRepoWithRemote(t)
	actionTestWrite(t, filepath.Join(repo, "outgoing"), "commit\n")
	actionTestCommit(t, repo, "outgoing")
	g := &uncertainPushGit{err: errors.New("must not start")}
	a := NewActions(g, 1)
	p, err := a.Plan(context.Background(), Push, []string{repo})
	if err != nil || !p.Targets[0].Eligible {
		t.Fatalf("%+v, %v", p, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := a.Execute(ctx, p.ID, nil)
	if !errors.Is(err, context.Canceled) || results[0].State != Cancelled || g.calls != 0 {
		t.Fatalf("%+v, %v, calls %d", results, err, g.calls)
	}
}

type serialPushGit struct {
	gitcli.Service
	mu                  sync.Mutex
	active, peak, calls int
}

func (g *serialPushGit) Push(ctx context.Context, path string, target gitcli.PushTarget) error {
	g.mu.Lock()
	g.active++
	g.calls++
	g.peak = max(g.peak, g.active)
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
	time.Sleep(30 * time.Millisecond)
	return g.Service.Push(ctx, path, target)
}

func TestLinkedWorktreePushesShareSerialization(t *testing.T) {
	repo, _ := actionTestRepoWithRemote(t)
	actionGit(t, repo, "branch", "other")
	actionGit(t, repo, "push", "origin", "other")
	actionGit(t, repo, "branch", "--set-upstream-to", "origin/other", "other")
	linked := filepath.Join(t.TempDir(), "linked")
	actionGit(t, repo, "worktree", "add", linked, "other")
	for _, path := range []string{repo, linked} {
		actionTestWrite(t, filepath.Join(path, "outgoing"), path+"\n")
		actionTestCommit(t, path, "outgoing")
	}
	g := &serialPushGit{}
	a := NewActions(g, 2)
	p, err := a.Plan(context.Background(), Push, []string{repo, linked})
	if err != nil || !p.Targets[0].Eligible || !p.Targets[1].Eligible {
		t.Fatalf("%+v, %v", p, err)
	}
	results, err := a.Execute(context.Background(), p.ID, nil)
	if err != nil || results[0].State != Succeeded || results[1].State != Succeeded || g.peak != 1 || g.calls != 2 {
		t.Fatalf("%+v, %v, peak %d calls %d", results, err, g.peak, g.calls)
	}
}
