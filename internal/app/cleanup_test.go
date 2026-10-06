package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// cleanupRepo returns a repository with origin/HEAD set and a merged, clean
// linked worktree "merged" plus an unmerged one "ahead".
func cleanupRepo(t *testing.T) (repo, merged, ahead string) {
	t.Helper()
	repo, _ = actionTestRepoWithRemote(t)
	// Git 2.48+ would recreate origin/HEAD on fetch; older Git ignores this key.
	actionGit(t, repo, "config", "remote.origin.followRemoteHEAD", "never")
	actionGit(t, repo, "remote", "set-head", "origin", "main")
	base := t.TempDir()
	merged, ahead = filepath.Join(base, "merged"), filepath.Join(base, "ahead")
	actionGit(t, repo, "worktree", "add", "-b", "merged", merged)
	actionGit(t, repo, "worktree", "add", "-b", "ahead", ahead)
	actionTestWrite(t, filepath.Join(ahead, "new"), "work\n")
	actionTestCommit(t, ahead, "unmerged work")
	return repo, merged, ahead
}

func itemFor(t *testing.T, p CleanupPreview, kind CleanupKind, path string) CleanupItem {
	t.Helper()
	for _, item := range p.Items {
		if item.Kind == kind && item.Path == path {
			return item
		}
	}
	t.Fatalf("no %s item for %s in %+v", kind, path, p.Items)
	return CleanupItem{}
}

func TestCleanupPlansMergedWorktreeAndKeepsOthers(t *testing.T) {
	repo, merged, ahead := cleanupRepo(t)
	dirty := filepath.Join(t.TempDir(), "dirty")
	actionGit(t, repo, "worktree", "add", "-b", "dirty", dirty)
	actionTestWrite(t, filepath.Join(dirty, "scratch"), "x")
	ignored := filepath.Join(t.TempDir(), "ignored")
	actionGit(t, repo, "worktree", "add", "-b", "ignored", ignored)
	actionTestWrite(t, filepath.Join(repo, ".git", "info", "exclude"), ".env\n") // shared by all worktrees
	actionTestWrite(t, filepath.Join(ignored, ".env"), "SECRET=1\n")
	locked := filepath.Join(t.TempDir(), "locked")
	actionGit(t, repo, "worktree", "add", "--lock", "--reason", "agent session", "-b", "locked", locked)

	actions := NewActions(gitcli.Service{}, 2)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); !item.Eligible || item.Branch != "merged" || item.BaseName != "origin/main" {
		t.Fatalf("merged: %+v", item)
	}
	for path, reason := range map[string]string{ahead: "not merged into origin/main", dirty: "dirty", ignored: "ignored file", locked: "locked: agent session"} {
		if item := itemFor(t, preview, RemoveWorktree, path); item.Eligible || !strings.Contains(item.Reason, reason) {
			t.Fatalf("%s: %+v", path, item)
		}
	}
	for _, item := range preview.Items {
		if item.Path == repo && item.Kind == RemoveWorktree {
			t.Fatal("main worktree offered for removal")
		}
	}
}

func TestCleanupExecutesOnlyChosenItems(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	stale := filepath.Join(t.TempDir(), "stale")
	actionGit(t, repo, "worktree", "add", "-b", "stale", stale)
	os.RemoveAll(stale)
	actions := NewActions(gitcli.Service{}, 2)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prune := itemFor(t, preview, PruneStale, preview.Items[0].Group)
	if !prune.Eligible || len(prune.Stale) != 1 {
		t.Fatalf("prune: %+v", prune)
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{prune.ID}, nil)
	if err != nil || len(results) != 1 || results[0].State != Succeeded || results[0].Item != prune.ID {
		t.Fatalf("results %+v %v", results, err)
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("unchosen worktree was removed")
	}
	if _, err := actions.ExecuteCleanup(context.Background(), preview.ID, nil, nil); err == nil {
		t.Fatal("a cleanup preview ran twice")
	}
}

func TestCleanupRevalidatesBeforeRemoving(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	item := itemFor(t, preview, RemoveWorktree, merged)
	actionTestWrite(t, filepath.Join(merged, "late"), "agent wrote this after review\n")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{item.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped {
		t.Fatalf("dirtied worktree not skipped: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(merged, "late")); err != nil {
		t.Fatal("late file lost")
	}
}

func TestCleanupDanglingDefaultRef(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/gone")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible || !strings.Contains(item.Reason, "default branch") {
		t.Fatalf("dangling origin/HEAD: %+v", item)
	}
}

func TestCleanupMissingDefaultRefSuggestsSetHead(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionGit(t, repo, "remote", "set-head", "origin", "--delete")
	actions := NewActions(gitcli.Service{}, 1)
	preview, _ := actions.PlanCleanup(context.Background(), []string{repo})
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible || !strings.Contains(item.Reason, "git remote set-head origin -a") {
		t.Fatalf("missing origin/HEAD: %+v", item)
	}
}

func TestCleanupLocalDefaultWithoutRemote(t *testing.T) {
	repo := actionTestRepo(t)
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	wt := filepath.Join(t.TempDir(), "wt")
	actionGit(t, repo, "worktree", "add", "-b", "wt", wt)
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, wt); !item.Eligible || item.BaseName != "main (local default)" {
		t.Fatalf("local default: %+v", item)
	}
}

type overflowIgnored struct{ gitcli.Service }

func (overflowIgnored) IgnoredFiles(context.Context, string) ([]string, error) {
	return nil, gitcli.ErrOutputLimit
}

func TestCleanupKeepsWorktreeWhenIgnoredListingOverflows(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	preview, err := NewActions(overflowIgnored{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible || !strings.Contains(item.Reason, "too many ignored files") {
		t.Fatalf("overflow: %+v", item)
	}
}

type oldGitService struct{ gitcli.Service }

func (oldGitService) SupportsWorktreeInventory(context.Context) bool { return false }

func TestCleanupRefusedOnOldGit(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actions := NewActions(oldGitService{}, 1)
	if actions.CleanupSupported(context.Background()) {
		t.Fatal("old Git reported as supported")
	}
	if _, err := actions.PlanCleanup(context.Background(), []string{repo}); !errors.Is(err, ErrCleanupUnsupported) {
		t.Fatalf("plan on old Git: %v", err)
	}
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err != nil {
		t.Fatalf("a refused cleanup must not hold the active preview: %v", err)
	}
}

func TestCleanupSharesTheActivePreview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err == nil {
		t.Fatal("fetch planned while a cleanup preview is pending")
	}
	actions.Discard(preview.ID)
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err != nil {
		t.Fatalf("discard did not release the cleanup preview: %v", err)
	}
}

func TestCleanupPreflightFetchFailureIsReported(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionGit(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var failed int
	for _, item := range preview.Items {
		if item.Kind == CleanupGroup && item.Failed && strings.Contains(item.Reason, "preflight fetch failed") {
			failed++
		}
		if item.Kind == RemoveWorktree && item.Eligible {
			t.Fatalf("removal eligible after a failed fetch: %+v", item)
		}
	}
	if failed != 1 {
		t.Fatalf("want one failed group item, got %d in %+v", failed, preview.Items)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible {
		t.Fatalf("merged: %+v", item)
	}
}

type movedHead struct {
	gitcli.Service
	oid string
}

func (m *movedHead) Inspect(ctx context.Context, path string) repository.Status {
	s := m.Service.Inspect(ctx, path)
	if m.oid != "" {
		s.HeadOID = m.oid
	}
	return s
}

func TestCleanupSkipsWhenInspectedHeadDiffersFromReviewed(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	svc := &movedHead{}
	actions := NewActions(svc, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	item := itemFor(t, preview, RemoveWorktree, merged)
	if !item.Eligible {
		t.Fatalf("not eligible: %+v", item)
	}
	svc.oid = strings.Repeat("a", len(item.OID))
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{item.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "HEAD moved") {
		t.Fatalf("moved HEAD not skipped: %+v", results)
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("worktree removed")
	}
}
