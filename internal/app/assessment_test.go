package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

func TestAssessTakesTheMostRestrictiveStatus(t *testing.T) {
	if a := Assess(); a.Allowed() || a.Status != "" {
		t.Fatalf("an empty assessment must allow nothing: %+v", a)
	}
	allowed := Assess(CleanupReason{CleanupClean, "working tree clean"}, CleanupReason{CleanupMerged, "merged into origin/main"})
	if !allowed.Allowed() || allowed.Summary() != "merged into origin/main" {
		t.Fatalf("allowed: %+v %q", allowed, allowed.Summary())
	}
	for _, tc := range []struct {
		reasons []CleanupReason
		want    CleanupStatus
	}{
		{[]CleanupReason{{CleanupClean, ""}, {CleanupNotMerged, ""}}, CleanupNeedsReview},
		{[]CleanupReason{{CleanupNotMerged, ""}, {CleanupCheckFailed, ""}}, CleanupUnknown},
		{[]CleanupReason{{CleanupCheckFailed, ""}, {CleanupUncommitted, ""}, {CleanupNotMerged, ""}}, CleanupBlocked},
		{[]CleanupReason{{CleanupMerged, ""}, {"from_a_newer_version", ""}}, CleanupBlocked},
	} {
		if got := Assess(tc.reasons...).Status; got != tc.want {
			t.Errorf("%v: got %s, want %s", tc.reasons, got, tc.want)
		}
	}
	kept := Assess(CleanupReason{CleanupClean, "working tree clean"}, CleanupReason{CleanupNotMerged, "not merged into origin/main"}, CleanupReason{CleanupUnpushed, "1 commit not pushed to origin/x"})
	if got := kept.Summary(); got != "not merged into origin/main; 1 commit not pushed to origin/x" {
		t.Fatalf("summary lists concerns only: %q", got)
	}
}

func codes(a CleanupAssessment) []CleanupCode {
	var out []CleanupCode
	for _, r := range a.Reasons {
		out = append(out, r.Code)
	}
	return out
}

func TestCleanupClassifiesWorktreesAndListsEveryConcern(t *testing.T) {
	repo, merged, ahead := cleanupRepo(t)
	// pushed: published to its upstream but not merged.
	pushed := filepath.Join(t.TempDir(), "pushed")
	actionGit(t, repo, "worktree", "add", "-b", "pushed", pushed)
	actionTestWrite(t, filepath.Join(pushed, "p"), "p\n")
	actionTestCommit(t, pushed, "published work")
	actionGit(t, pushed, "push", "-u", "origin", "pushed")
	// unpushed: one commit beyond its upstream.
	unpushed := filepath.Join(t.TempDir(), "unpushed")
	actionGit(t, repo, "worktree", "add", "-b", "unpushed", unpushed)
	actionTestWrite(t, filepath.Join(unpushed, "u"), "u\n")
	actionTestCommit(t, unpushed, "published")
	actionGit(t, unpushed, "push", "-u", "origin", "unpushed")
	actionTestWrite(t, filepath.Join(unpushed, "u"), "more\n")
	actionTestCommit(t, unpushed, "local only")
	// gone: upstream deleted after a (squash) merge elsewhere.
	gone := filepath.Join(t.TempDir(), "gone")
	actionGit(t, repo, "worktree", "add", "-b", "gone", gone)
	actionTestWrite(t, filepath.Join(gone, "g"), "g\n")
	actionTestCommit(t, gone, "squashed upstream")
	actionGit(t, gone, "push", "-u", "origin", "gone")
	actionGit(t, repo, "push", "origin", "--delete", "gone")
	// messy: uncommitted and untracked files on unmerged work.
	messy := filepath.Join(t.TempDir(), "messy")
	actionGit(t, repo, "worktree", "add", "-b", "messy", messy, "ahead")
	actionTestWrite(t, filepath.Join(messy, "tracked"), "edited\n")
	actionTestWrite(t, filepath.Join(messy, "scratch"), "x")

	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		status CleanupStatus
		codes  []CleanupCode
	}{
		{merged, CleanupAllowed, []CleanupCode{CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupMerged}},
		{ahead, CleanupNeedsReview, []CleanupCode{CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupNotMerged, CleanupNoUpstream}},
		{pushed, CleanupNeedsReview, []CleanupCode{CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupNotMerged}},
		{gone, CleanupNeedsReview, []CleanupCode{CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupNotMerged, CleanupUpstreamGone}},
		{unpushed, CleanupBlocked, []CleanupCode{CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupNotMerged, CleanupUnpushed}},
		// Dirty worktrees skip the whole-worktree file listings.
		{messy, CleanupBlocked, []CleanupCode{CleanupUncommitted, CleanupUntracked, CleanupNotMerged, CleanupNoUpstream}},
	} {
		item := itemFor(t, preview, RemoveWorktree, tc.path)
		if item.Assessment.Status != tc.status || !slices.Equal(codes(item.Assessment), tc.codes) {
			t.Errorf("%s: got %s %v, want %s %v", filepath.Base(tc.path), item.Assessment.Status, codes(item.Assessment), tc.status, tc.codes)
		}
	}
	if s := itemFor(t, preview, RemoveWorktree, messy).Assessment.Summary(); s != "1 uncommitted file; 1 untracked file; not merged into origin/main; branch has no upstream" {
		t.Errorf("messy summary: %q", s)
	}
	if s := itemFor(t, preview, RemoveWorktree, unpushed).Assessment.Summary(); !strings.Contains(s, "1 commit not pushed to origin/unpushed") {
		t.Errorf("unpushed summary: %q", s)
	}
}

func TestCleanupBlocksDetachedHeadOnlyWhenItsCommitIsUnreferenced(t *testing.T) {
	repo, _, ahead := cleanupRepo(t)
	lone := filepath.Join(t.TempDir(), "lone")
	actionGit(t, repo, "worktree", "add", "--detach", lone)
	actionTestWrite(t, filepath.Join(lone, "precious"), "only here\n")
	actionTestCommit(t, lone, "unreferenced work")
	oid := strings.TrimSpace(string(actionGit(t, lone, "rev-parse", "HEAD")))
	onBranch := filepath.Join(t.TempDir(), "onbranch")
	actionGit(t, repo, "worktree", "add", "--detach", onBranch, "ahead")
	_ = ahead

	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	item := itemFor(t, preview, RemoveWorktree, lone)
	if item.Assessment.Status != CleanupBlocked || !item.Assessment.Has(CleanupUnreferencedCommit) || !strings.Contains(item.Assessment.Summary(), "git branch rescue/lone "+oid) {
		t.Fatalf("unreferenced detached HEAD: %+v", item.Assessment)
	}
	item = itemFor(t, preview, RemoveWorktree, onBranch)
	if item.Assessment.Status != CleanupNeedsReview || item.Assessment.Has(CleanupUnreferencedCommit) || !item.Assessment.Has(CleanupNotMerged) {
		t.Fatalf("detached HEAD a branch reaches: %+v", item.Assessment)
	}
}

func TestCleanupSkipsWorktreeThatSwitchedBranchAfterReview(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	item := itemFor(t, preview, RemoveWorktree, merged)
	if !item.Eligible() {
		t.Fatalf("not eligible: %+v", item)
	}
	// Same commit, different branch: the reviewed worktree is not what runs.
	actionGit(t, merged, "switch", "-c", "other")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{item.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "switched from branch merged to branch other since review · review again") {
		t.Fatalf("switched worktree not skipped: %+v", results)
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("worktree removed")
	}
}

func TestCleanupRefusesTheMainWorktreeInTheDomain(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	head := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
	// An item naming the main worktree can only come from a forged or stale
	// plan; revalidation must still refuse it.
	item := CleanupItem{Kind: RemoveWorktree, Group: repo, Path: repo, Branch: "main", OID: head, Base: "refs/remotes/origin/main", BaseName: "origin/main"}
	check := revalidateCleanup(context.Background(), gitcli.Service{}, item)
	if check.Status != CleanupBlocked || !check.Has(CleanupMainWorktree) {
		t.Fatalf("main worktree: %+v", check)
	}
}

// The re-create command in a removal result must work as printed.
func TestCleanupRemovalResultRecreatesTheWorktree(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	detached := filepath.Join(t.TempDir(), "detached")
	actionGit(t, repo, "worktree", "add", "--detach", detached, "main")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	branchItem, detachedItem := itemFor(t, preview, RemoveWorktree, merged), itemFor(t, preview, RemoveWorktree, detached)
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{branchItem.ID, detachedItem.ID}, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("results %+v, %v", results, err)
	}
	for _, r := range results {
		_, command, ok := strings.Cut(r.Message, " · re-create: ")
		if r.State != Succeeded || !ok {
			t.Fatalf("result %+v", r)
		}
		cmd := exec.Command("sh", "-c", command)
		cmd.Env = actionTestGitEnv(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", command, err, out)
		}
	}
	if branch := strings.TrimSpace(string(actionGit(t, merged, "branch", "--show-current"))); branch != "merged" {
		t.Fatalf("re-created worktree on %q", branch)
	}
	if head := strings.TrimSpace(string(actionGit(t, detached, "rev-parse", "HEAD"))); head != detachedItem.OID {
		t.Fatalf("re-created detached worktree at %s, want %s", head, detachedItem.OID)
	}
}

func TestRecreateCommandQuotesAndFlagsEscapedNames(t *testing.T) {
	oid := strings.Repeat("a", 40)
	if got := recreateCommand(CleanupItem{Group: "/r", Path: "/w/two words", Branch: "x;id"}); got != "git -C /r worktree add '/w/two words' 'x;id'" {
		t.Fatalf("quoted: %s", got)
	}
	if got := recreateCommand(CleanupItem{Group: "/r", Path: "/w/d", OID: oid}); got != "git -C /r worktree add /w/d --detach "+oid {
		t.Fatalf("detached: %s", got)
	}
	if got := recreateCommand(CleanupItem{Group: "/r", Path: "/w/x", Branch: "bad\x1bname"}); strings.Contains(got, "\x1b") || !strings.Contains(got, "names shown escaped") {
		t.Fatalf("escaped: %q", got)
	}
}
