package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

func TestCleanupPlansMergedBranchesAfterTheirWorktree(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done") // merged, not checked out
	actionGit(t, repo, "branch", "gone-squash", "ahead")
	actionGit(t, repo, "push", "-u", "origin", "gone-squash")
	actionGit(t, repo, "push", "origin", "--delete", "gone-squash")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	group := preview.Items[0].Group
	branch := func(name string) *CleanupItem {
		for i, item := range preview.Items {
			if item.Kind == DeleteBranch && item.Branch == name {
				return &preview.Items[i]
			}
		}
		return nil
	}
	if b := branch("done"); b == nil || !b.Eligible {
		t.Fatalf("done: %+v", b)
	}
	if b := branch("merged"); b == nil || !b.Eligible {
		t.Fatalf("branch of a removable worktree should be eligible: %+v", b)
	}
	if b := branch("gone-squash"); b == nil || b.Eligible || !strings.Contains(b.Reason, "squash merge?") {
		t.Fatalf("gone-squash: %+v", b)
	}
	if b := branch("ahead"); b != nil {
		t.Fatalf("ordinary unmerged branch listed: %+v", b)
	}
	var ids []string
	for _, item := range preview.Items {
		if item.Eligible {
			ids = append(ids, item.ID)
		}
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.State != Succeeded {
			t.Fatalf("result %+v", r)
		}
	}
	if out := actionGit(t, group, "branch", "--list", "merged", "done"); len(strings.TrimSpace(string(out))) != 0 {
		t.Fatalf("branches remain: %s", out)
	}
	if _, err := os.Stat(merged); !os.IsNotExist(err) {
		t.Fatal("worktree not removed before its branch")
	}
	hinted := false
	for _, r := range results {
		if strings.Contains(r.Item, "done") {
			hinted = true
			if !strings.Contains(r.Message, "restore: git branch done ") {
				t.Fatalf("no recovery hint: %+v", r)
			}
		}
	}
	if !hinted {
		t.Fatal("no result for done")
	}
}

func TestCleanupSkipsBranchStillCheckedOut(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var branchID string
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "merged" {
			branchID = item.ID
		}
	}
	if branchID == "" {
		t.Fatal("no branch item for merged")
	}
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{branchID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "checked out") {
		t.Fatalf("branch under a kept worktree: %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/merged")
}

func TestCleanupNeverDeletesDefaultBranch(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "checkout", "--detach")
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "main" {
			t.Fatalf("default branch offered: %+v", item)
		}
		listed = listed || item.Kind == DeleteBranch
	}
	if !listed {
		t.Fatal("expected other branch items")
	}
}

func TestCleanupSkipsBranchThatMoved(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "done" {
			id = item.ID
		}
	}
	actionGit(t, repo, "branch", "-f", "done", "ahead")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{id}, nil)
	if len(results) != 1 || results[0].State != Skipped {
		t.Fatalf("moved branch: %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/done")
}

func TestCleanupNeverDeletesLocalDefaultWithoutRemote(t *testing.T) {
	repo := actionTestRepo(t)
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "init")
	actionGit(t, repo, "branch", "done")
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var done bool
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "main" {
			t.Fatalf("local default offered: %+v", item)
		}
		done = done || (item.Kind == DeleteBranch && item.Branch == "done" && item.Eligible)
	}
	if !done {
		t.Fatalf("done not offered: %+v", preview.Items)
	}
}

func TestCleanupDeletesBranchOfStaleWorktreeAfterPrune(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	stale := filepath.Join(t.TempDir(), "stale")
	actionGit(t, repo, "worktree", "add", "-b", "stale-br", stale)
	os.RemoveAll(stale)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range preview.Items {
		if item.Kind == PruneStale || (item.Kind == DeleteBranch && item.Branch == "stale-br") {
			if !item.Eligible {
				t.Fatalf("not eligible: %+v", item)
			}
			ids = append(ids, item.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("ids %v in %+v", ids, preview.Items)
	}
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, ids, nil)
	if len(results) != 2 || results[0].State != Succeeded || results[1].State != Succeeded {
		t.Fatalf("results %+v", results)
	}
	if out := actionGit(t, repo, "branch", "--list", "stale-br"); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("branch remains: %s", out)
	}
}

func TestCleanupDefaultBranchSkippedForRemoteNameWithSlash(t *testing.T) {
	repo := actionTestRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	actionBareRemote(t, remote)
	actionTestWrite(t, filepath.Join(repo, "tracked"), "initial\n")
	actionTestCommit(t, repo, "initial")
	actionGit(t, repo, "remote", "add", "up/stream", remote)
	actionGit(t, repo, "push", "up/stream", "main")
	actionGit(t, repo, "config", "remote.up/stream.followRemoteHEAD", "never")
	actionGit(t, repo, "remote", "set-head", "up/stream", "main")
	actionGit(t, repo, "checkout", "--detach")
	actionGit(t, repo, "branch", "done")
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var done bool
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "main" {
			t.Fatalf("default branch offered: %+v", item)
		}
		done = done || (item.Kind == DeleteBranch && item.Branch == "done" && item.Eligible)
	}
	if !done {
		t.Fatalf("done not offered: %+v", preview.Items)
	}
}

func branchItem(p CleanupPreview, name string) *CleanupItem {
	for i, item := range p.Items {
		if item.Kind == DeleteBranch && item.Branch == name {
			return &p.Items[i]
		}
	}
	return nil
}

func TestCleanupKeepsBranchOfLockedWorktreeWithMissingDirectory(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	usb := filepath.Join(t.TempDir(), "usb")
	actionGit(t, repo, "worktree", "add", "--lock", "-b", "onusb", usb)
	os.RemoveAll(usb)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if b := branchItem(preview, "onusb"); b == nil || b.Eligible || !strings.Contains(b.Reason, "checked out") {
		t.Fatalf("branch of locked worktree: %+v", b)
	}
	actions.Discard(preview.ID)
}

func TestCleanupSkipsBranchWhoseStaleWorktreeGotLockedAfterReview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	usb := filepath.Join(t.TempDir(), "usb")
	actionGit(t, repo, "worktree", "add", "-b", "onusb", usb)
	os.RemoveAll(usb)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	b := branchItem(preview, "onusb")
	if b == nil || !b.Eligible {
		t.Fatalf("branch of stale worktree should be eligible: %+v", b)
	}
	actionGit(t, repo, "worktree", "lock", usb)
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{b.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "checked out") {
		t.Fatalf("results %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/onusb")
}

// conflictRebaseSetup returns a linked worktree on branch "rb" (merged into
// origin/main) whose rebase onto "target" will conflict.
func conflictRebaseSetup(t *testing.T, repo string) string {
	t.Helper()
	actionGit(t, repo, "checkout", "-b", "target")
	actionTestWrite(t, filepath.Join(repo, "conflict.txt"), "target\n")
	actionTestCommit(t, repo, "target change")
	actionGit(t, repo, "checkout", "main")
	actionTestWrite(t, filepath.Join(repo, "conflict.txt"), "main\n")
	actionTestCommit(t, repo, "main change")
	actionGit(t, repo, "push", "origin", "main")
	wt := filepath.Join(t.TempDir(), "rb")
	actionGit(t, repo, "worktree", "add", "-b", "rb", wt, "main")
	return wt
}

func startConflictRebase(t *testing.T, wt string) {
	t.Helper()
	cmd := exec.Command("git", "-C", wt, "rebase", "target")
	cmd.Env = actionTestGitEnv(t)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("rebase should conflict: %s", out)
	}
	gitDir := strings.TrimSpace(string(actionGit(t, wt, "rev-parse", "--absolute-git-dir")))
	if _, err := os.Stat(filepath.Join(gitDir, "rebase-merge")); err != nil {
		t.Fatalf("no rebase in progress: %v", err)
	}
}

func TestCleanupKeepsMergedBranchesWhileAWorktreeIsRebasing(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done")
	wt := conflictRebaseSetup(t, repo)
	startConflictRebase(t, wt)
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rb", "done", "merged"} {
		if b := branchItem(preview, name); b == nil || b.Eligible || !strings.Contains(b.Reason, "operation in progress in rb") {
			t.Fatalf("%s: %+v", name, b)
		}
	}
}

func TestCleanupSkipsBranchWhenAWorktreeStartsRebasingAfterReview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done")
	wt := conflictRebaseSetup(t, repo)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	b := branchItem(preview, "done")
	if b == nil || !b.Eligible {
		t.Fatalf("done: %+v", b)
	}
	startConflictRebase(t, wt)
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{b.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "operation in progress") {
		t.Fatalf("results %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/done")
}

func TestCleanupKeepsBranchCheckedOutInASecondWorktree(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	first := filepath.Join(t.TempDir(), "dup1")
	second := filepath.Join(t.TempDir(), "dup2")
	actionGit(t, repo, "worktree", "add", "-b", "dup", first)
	actionGit(t, repo, "worktree", "add", "--force", second, "dup")
	actionTestWrite(t, filepath.Join(second, "scratch"), "x")
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, first); !item.Eligible {
		t.Fatalf("first worktree should be removable: %+v", item)
	}
	if b := branchItem(preview, "dup"); b == nil || b.Eligible || !strings.Contains(b.Reason, "dup2") {
		t.Fatalf("dup: %+v", b)
	}
}

// outsideHeadGit reports a default ref outside refs/remotes/<remote>/.
type outsideHeadGit struct{ gitcli.Service }

func (outsideHeadGit) RemoteDefaultRef(context.Context, string, string) (string, error) {
	return "refs/heads/main", nil
}

func TestCleanupTreatsDefaultRefOutsideRemoteAsUnknown(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done")
	preview, err := NewActions(outsideHeadGit{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible || !strings.Contains(item.Reason, "default branch unknown") {
		t.Fatalf("worktree: %+v", item)
	}
	for _, item := range preview.Items {
		if item.Eligible {
			t.Fatalf("eligible item without a known default: %+v", item)
		}
		if item.Kind == DeleteBranch {
			t.Fatalf("branch item without a known default: %+v", item)
		}
	}
}

func TestCleanupNeverOffersSymbolicBranchesAndKeepsTheirTarget(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "symbolic-ref", "refs/heads/master", "refs/heads/main")
	actionGit(t, repo, "branch", "done")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if b := branchItem(preview, "master"); b != nil {
		t.Fatalf("symbolic branch offered: %+v", b)
	}
	var ids []string
	for _, item := range preview.Items {
		if item.Eligible {
			ids = append(ids, item.ID)
		}
	}
	if _, err := actions.ExecuteCleanup(context.Background(), preview.ID, ids, nil); err != nil {
		t.Fatal(err)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/main")
	if b := strings.TrimSpace(string(actionGit(t, repo, "branch", "--list", "done"))); b != "" {
		t.Fatalf("done not deleted: %q", b)
	}
}

// symrefAfterReview turns the reviewed branch into a symbolic ref between plan
// and execute.
func TestCleanupRevalidationRefusesABranchThatBecameSymbolic(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	b := branchItem(preview, "done")
	if b == nil || !b.Eligible {
		t.Fatalf("done: %+v", b)
	}
	actionGit(t, repo, "symbolic-ref", "refs/heads/done", "refs/heads/main")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{b.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "symbolic") {
		t.Fatalf("results %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/main")
}
