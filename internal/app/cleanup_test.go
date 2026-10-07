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

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"feat/old-login":      "feat/old-login",
		"a.b_c@d+e-f/1":       "a.b_c@d+e-f/1",
		"x;echo${IFS}PWNED":   "'x;echo${IFS}PWNED'",
		"a$(id)b":             "'a$(id)b'",
		"a`id`b":              "'a`id`b'",
		"it's":                `'it'\''s'`,
		"two words":           "'two words'",
		"star*":               "'star*'",
		`back\slash`:          `'back\slash'`,
		"naïve":               "'naïve'",
		"quote\"d":            "'quote\"d'",
		"semi;colon&amp|pipe": "'semi;colon&amp|pipe'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDeletedBranchMessageQuotesShellSignificantNames(t *testing.T) {
	oid := strings.Repeat("a", 40)
	plain := deletedBranchMessage(CleanupItem{Branch: "feat/old-login", OID: oid})
	if !strings.Contains(plain, "restore: git branch feat/old-login "+oid) || strings.Contains(plain, "'") {
		t.Fatalf("plain name: %s", plain)
	}
	hostile := deletedBranchMessage(CleanupItem{Branch: "x;echo${IFS}PWNED", OID: oid})
	if !strings.Contains(hostile, "restore: git branch 'x;echo${IFS}PWNED' "+oid) {
		t.Fatalf("shell-significant name: %s", hostile)
	}
	odd := deletedBranchMessage(CleanupItem{Branch: "bad\x1bname", OID: oid})
	if !strings.Contains(odd, "unprintable") || !strings.Contains(odd, oid) || strings.Contains(odd, "\x1b") {
		t.Fatalf("unprintable name: %q", odd)
	}
}

// staleDetached adds a detached linked worktree with one commit no ref reaches,
// then deletes its directory so only the administrative record remains.
func staleDetached(t *testing.T, repo, dir string) (path, oid string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), dir)
	actionGit(t, repo, "worktree", "add", "--detach", path)
	actionTestWrite(t, filepath.Join(path, "precious"), "only here\n")
	actionTestCommit(t, path, "unreferenced work")
	oid = strings.TrimSpace(string(actionGit(t, path, "rev-parse", "HEAD")))
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	return path, oid
}

func pruneItem(t *testing.T, p CleanupPreview) CleanupItem {
	t.Helper()
	for _, item := range p.Items {
		if item.Kind == PruneStale {
			return item
		}
	}
	t.Fatalf("no prune item in %+v", p.Items)
	return CleanupItem{}
}

func TestCleanupNamesEveryStrandedCommitsRescueCommand(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	_, first := staleDetached(t, repo, "spike-a")
	_, second := staleDetached(t, repo, "spike-b")
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prune := pruneItem(t, preview)
	for _, want := range []string{"git branch rescue/spike-a " + first, "git branch rescue/spike-b " + second} {
		if prune.Eligible || !strings.Contains(prune.Reason, want) {
			t.Fatalf("missing %q in %+v", want, prune)
		}
	}
}

func TestCleanupKeepsStaleRecordHoldingUnreferencedCommit(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	_, oid := staleDetached(t, repo, "spike")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prune := pruneItem(t, preview)
	if prune.Eligible || !strings.Contains(prune.Reason, "stale worktree spike holds unreferenced commit "+oid[:7]) || !strings.Contains(prune.Reason, "create a branch first: git branch rescue/spike "+oid) {
		t.Fatalf("prune: %+v", prune)
	}
	// Choosing it anyway runs nothing.
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{prune.ID}, nil)
	if len(results) != 0 {
		t.Fatalf("ineligible prune ran: %+v", results)
	}
	if out := actionGit(t, repo, "worktree", "list", "--porcelain"); !strings.Contains(string(out), "spike") {
		t.Fatalf("record was pruned: %s", out)
	}
}

func TestCleanupPrunesStaleRecordWhoseCommitIsReachable(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	_, oid := staleDetached(t, repo, "spike")
	actionGit(t, repo, "branch", "rescue", oid)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prune := pruneItem(t, preview)
	if !prune.Eligible {
		t.Fatalf("prune: %+v", prune)
	}
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{prune.ID}, nil)
	if len(results) != 1 || results[0].State != Succeeded {
		t.Fatalf("results %+v", results)
	}
}

func TestCleanupSkipsPruneWhenItsCommitBecameUnreferencedAfterReview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	_, oid := staleDetached(t, repo, "spike")
	actionGit(t, repo, "branch", "rescue", oid)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prune := pruneItem(t, preview)
	if !prune.Eligible {
		t.Fatalf("prune: %+v", prune)
	}
	actionGit(t, repo, "branch", "-D", "rescue")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{prune.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "unreferenced commit") {
		t.Fatalf("results %+v", results)
	}
	if out := actionGit(t, repo, "worktree", "list", "--porcelain"); !strings.Contains(string(out), "spike") {
		t.Fatalf("record was pruned: %s", out)
	}
}

func TestCleanupQuotesShellSignificantNamesInThePruneHint(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	staleDetached(t, repo, "x;echo$(id)")
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if prune := pruneItem(t, preview); prune.Eligible || !strings.Contains(prune.Reason, "git branch 'rescue/x;echo$(id)' ") {
		t.Fatalf("prune: %+v", prune)
	}
}

type failingReachability struct{ gitcli.Service }

func (failingReachability) ReachableFromRefs(context.Context, string, string) (bool, error) {
	return false, errors.New("boom")
}

func TestCleanupKeepsStaleRecordsWhenReachabilityCannotBeChecked(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	staleDetached(t, repo, "spike")
	preview, err := NewActions(failingReachability{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if prune := pruneItem(t, preview); prune.Eligible || !strings.Contains(prune.Reason, "boom") {
		t.Fatalf("prune: %+v", prune)
	}
}

// Race tests: each change happens between PlanCleanup and ExecuteCleanup.

func TestCleanupSkipsWorktreeLockedAfterReview(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	item := itemFor(t, preview, RemoveWorktree, merged)
	if !item.Eligible {
		t.Fatalf("not eligible: %+v", item)
	}
	actionGit(t, repo, "worktree", "lock", "--reason", "agent resumed", merged)
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{item.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "locked") {
		t.Fatalf("locked worktree not skipped: %+v", results)
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("locked worktree directory was removed")
	}
}

func TestCleanupSkipsPruneWhenAnotherStaleRecordAppearsAfterReview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	first := filepath.Join(t.TempDir(), "first")
	actionGit(t, repo, "worktree", "add", "-b", "first", first)
	os.RemoveAll(first)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prune := pruneItem(t, preview)
	if !prune.Eligible {
		t.Fatalf("prune: %+v", prune)
	}
	second := filepath.Join(t.TempDir(), "second")
	actionGit(t, repo, "worktree", "add", "-b", "second", second)
	os.RemoveAll(second)
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{prune.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "stale worktrees changed since review") {
		t.Fatalf("results %+v", results)
	}
	if out := actionGit(t, repo, "worktree", "list", "--porcelain"); !strings.Contains(string(out), "first") || !strings.Contains(string(out), "second") {
		t.Fatalf("records were pruned: %s", out)
	}
}

func TestCleanupSkipsWorktreeThatGainedAnIgnoredFileAfterReview(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionTestWrite(t, filepath.Join(repo, ".git", "info", "exclude"), ".env\n")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	item := itemFor(t, preview, RemoveWorktree, merged)
	if !item.Eligible {
		t.Fatalf("not eligible: %+v", item)
	}
	actionTestWrite(t, filepath.Join(merged, ".env"), "SECRET=1\n")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{item.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "ignored file") {
		t.Fatalf("ignored file not protected: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(merged, ".env")); err != nil {
		t.Fatal("ignored file lost")
	}
}
