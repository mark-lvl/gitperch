package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
)

// liveRepo is cleanupRepo plus a linked worktree on branch "squashed",
// pushed to origin, where it stays, as after a squash merge that kept the
// branch: Git sees the branch unmerged, with a live upstream.
func liveRepo(t *testing.T) (repo, wt, tip string) {
	t.Helper()
	repo, _, _ = cleanupRepo(t)
	wt = filepath.Join(t.TempDir(), "squashed")
	actionGit(t, repo, "worktree", "add", "-b", "squashed", wt)
	actionTestWrite(t, filepath.Join(wt, "feature"), "work\n")
	actionTestCommit(t, wt, "squashed work")
	actionGit(t, wt, "push", "-u", "origin", "squashed")
	return repo, wt, strings.TrimSpace(string(actionGit(t, wt, "rev-parse", "HEAD")))
}

// squashRepo is liveRepo with the branch then deleted from origin, as GitHub
// does after a squash merge: Git sees the branch unmerged, with a gone
// upstream.
func squashRepo(t *testing.T) (repo, wt, tip string) {
	t.Helper()
	repo, wt, tip = liveRepo(t)
	actionGit(t, repo, "push", "origin", "--delete", "squashed")
	return repo, wt, tip
}

// squashGitHub answers as GitHub would if origin were acme/widgets, with prs
// as the pull requests of every branch named in heads.
func squashGitHub(err error, prs map[string][]github.PullRequest) *GitHub {
	return NewGitHub(fakeGitHubGit{urls: map[string]string{"origin": "https://github.com/acme/widgets.git"}}, &fakePulls{prs: prs, err: err}, nil)
}

func mergedPR(number int, head string) github.PullRequest {
	return github.PullRequest{Number: number, State: "MERGED", BaseRef: "main", BaseRepository: "acme/widgets", IntoDefault: true, HeadOID: head,
		URL: fmt.Sprintf("https://github.com/acme/widgets/pull/%d", number)}
}

func TestCleanupRemovesSquashMergedWorktreeAndBranch(t *testing.T) {
	repo, wt, tip := squashRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"squashed": {mergedPR(42, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	worktree := itemFor(t, preview, RemoveWorktree, wt)
	if !worktree.Eligible() || worktree.PullRequest == nil || worktree.Assessment.Summary() != "merged via PR #42 into main on GitHub" {
		t.Fatalf("worktree: %+v", worktree)
	}
	branch := branchItem(preview, "squashed")
	if branch == nil || !branch.Eligible() || branch.RestoreFrom != "origin" || branch.PullRequest.Number != 42 {
		t.Fatalf("branch: %+v", branch)
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{worktree.ID, branch.ID}, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("results %+v, %v", results, err)
	}
	var restore string
	for _, r := range results {
		if r.State != Succeeded {
			t.Fatalf("result %+v", r)
		}
		if r.Item == branch.ID {
			restore = r.Restore
		}
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still present: %v", err)
	}
	if out, err := execGitResult(repo, "rev-parse", "--verify", "--quiet", "refs/heads/squashed"); err == nil {
		t.Fatalf("branch still exists at %s", out)
	}
	want := "git -C " + repo + " branch squashed " + tip + " || git -C " + repo + " fetch origin 'refs/pull/42/head:refs/heads/squashed'"
	if restore != want {
		t.Fatalf("restore %q, want %q", restore, want)
	}
}

func TestCleanupRemovesSquashMergedWorkWhoseUpstreamIsLive(t *testing.T) {
	repo, wt, tip := liveRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"squashed": {mergedPR(42, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	worktree := itemFor(t, preview, RemoveWorktree, wt)
	if !worktree.Eligible() || !worktree.Assessment.Has(CleanupMergedPullRequest) {
		t.Fatalf("worktree: %+v", worktree.Assessment)
	}
	branch := branchItem(preview, "squashed")
	if branch == nil || !branch.Eligible() || !branch.Assessment.Has(CleanupMergedPullRequest) || branch.RestoreFrom != "origin" {
		t.Fatalf("branch: %+v", branch)
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{worktree.ID, branch.ID}, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("results %+v, %v", results, err)
	}
	want := "git -C " + repo + " branch squashed " + tip + " || git -C " + repo + " fetch origin 'refs/pull/42/head:refs/heads/squashed'"
	for _, r := range results {
		if r.State != Succeeded || r.Item == branch.ID && r.Restore != want {
			t.Fatalf("result %+v, want restore %q", r, want)
		}
	}
}

func TestCleanupListsBranchMergedOnGitHubAtAnotherCommit(t *testing.T) {
	repo, wt, tip := liveRepo(t)
	older := strings.Repeat("1", 40)
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"squashed": {mergedPR(42, older)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	defer actions.Discard(preview.ID)
	if worktree := itemFor(t, preview, RemoveWorktree, wt); worktree.Eligible() {
		t.Fatalf("worktree: %+v", worktree.Assessment)
	}
	note := "PR #42 merged at 1111111; this branch is at " + tip[:7]
	b := branchItem(preview, "squashed")
	if b == nil || b.Eligible() || b.Assessment.Status != CleanupNeedsReview || !b.Assessment.Has(CleanupNotMerged) ||
		!b.Assessment.Has(CleanupPullRequestUnproven) || !strings.Contains(b.Assessment.Summary(), note) {
		t.Fatalf("branch: %+v", b)
	}
}

func TestCleanupKeepsProvenBranchWhileAWorktreeIsRebasing(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "checkout", "-b", "done")
	actionTestWrite(t, filepath.Join(repo, "done"), "work\n")
	actionTestCommit(t, repo, "done work")
	actionGit(t, repo, "push", "-u", "origin", "done")
	actionGit(t, repo, "checkout", "main")
	actionGit(t, repo, "push", "origin", "--delete", "done")
	tip := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "done")))
	startConflictRebase(t, conflictRebaseSetup(t, repo))
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"done": {mergedPR(7, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	defer actions.Discard(preview.ID)
	if b := branchItem(preview, "done"); b == nil || b.Eligible() || !b.Assessment.Has(CleanupOperation) || !strings.Contains(b.Assessment.Summary(), "rebase in progress in rb") {
		t.Fatalf("done: %+v", b)
	}
}

func TestCleanupRestoresForkPullRequestFromTheBaseRepository(t *testing.T) {
	repo, wt, tip := squashRepo(t)
	log := filepath.Join(t.TempDir(), "cleanup.log")
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetRestoreLog(log)
	// origin is the fork me/widgets; the pull request went into acme/widgets.
	fork := NewGitHub(fakeGitHubGit{urls: map[string]string{"origin": "https://github.com/me/widgets.git"}}, &fakePulls{prs: map[string][]github.PullRequest{"squashed": {mergedPR(42, tip)}}}, nil)
	actions.SetGitHub(fork)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	worktree := itemFor(t, preview, RemoveWorktree, wt)
	branch := branchItem(preview, "squashed")
	if branch == nil || !branch.Eligible() || branch.RestoreFrom != "https://github.com/acme/widgets" {
		t.Fatalf("branch: %+v", branch)
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{worktree.ID, branch.ID}, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("results %+v, %v", results, err)
	}
	want := "git -C " + repo + " branch squashed " + tip + " || git -C " + repo + " fetch 'https://github.com/acme/widgets' 'refs/pull/42/head:refs/heads/squashed'"
	for _, r := range results {
		if r.State != Succeeded || r.Item == branch.ID && (r.Restore != want || !r.RestoreLogged) {
			t.Fatalf("result %+v, want restore %q", r, want)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "\t"+strconv.Quote(repo)+"\t\"squashed\"\t"+tip+"\t"+want) {
		t.Fatalf("log: %q", data)
	}
}

func TestCleanupKeepsWorkGitHubCannotProveMerged(t *testing.T) {
	older := strings.Repeat("1", 40)
	release := mergedPR(43, "")
	release.BaseRef, release.IntoDefault = "release", false
	for name, tc := range map[string]struct {
		prs  func(tip string) []github.PullRequest
		err  error
		code CleanupCode
		note string
	}{
		"newer local commits": {func(string) []github.PullRequest { return []github.PullRequest{mergedPR(42, older)} }, nil, CleanupPullRequestUnproven, "PR #42 merged at 1111111; this branch is at "},
		"merged elsewhere": {func(tip string) []github.PullRequest {
			release.HeadOID = tip
			return []github.PullRequest{release}
		}, nil, CleanupPullRequestUnproven, "PR #43 merged into release, not the default branch"},
		"still open":       {func(string) []github.PullRequest { return []github.PullRequest{{Number: 45, State: "OPEN"}} }, nil, CleanupPullRequestUnproven, "PR #45 is still open"},
		"closed":           {func(string) []github.PullRequest { return []github.PullRequest{{Number: 41, State: "CLOSED"}} }, nil, CleanupPullRequestUnproven, "PR #41 was closed without merging"},
		"gh not logged in": {func(string) []github.PullRequest { return nil }, github.ErrNotAuthenticated, CleanupGitHubCheckFailed, "gh is not logged in to github.com — run gh auth login"},
	} {
		t.Run(name, func(t *testing.T) {
			repo, wt, tip := squashRepo(t)
			actions := NewActions(gitcli.Service{}, 1)
			actions.SetGitHub(squashGitHub(tc.err, map[string][]github.PullRequest{"squashed": tc.prs(tip)}))
			preview, err := actions.PlanCleanup(context.Background(), []string{repo})
			if err != nil {
				t.Fatal(err)
			}
			defer actions.Discard(preview.ID)
			worktree := itemFor(t, preview, RemoveWorktree, wt)
			if worktree.Eligible() || worktree.Assessment.Status != CleanupNeedsReview || !worktree.Assessment.Has(tc.code) || !strings.Contains(worktree.Assessment.Summary(), tc.note) {
				t.Fatalf("worktree: %+v", worktree.Assessment)
			}
			if b := branchItem(preview, "squashed"); b == nil || b.Eligible() || !b.Assessment.Has(tc.code) || !strings.Contains(b.Assessment.Summary(), "squash merge?") {
				t.Fatalf("branch: %+v", b)
			}
		})
	}
}

func TestCleanupMergeEvidenceNeverOverridesBlockers(t *testing.T) {
	repo, wt, tip := squashRepo(t)
	actionTestWrite(t, filepath.Join(wt, "scratch"), "agent notes\n")
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"squashed": {mergedPR(42, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	defer actions.Discard(preview.ID)
	worktree := itemFor(t, preview, RemoveWorktree, wt)
	if worktree.Eligible() || worktree.Assessment.Status != CleanupBlocked || !worktree.Assessment.Has(CleanupMergedPullRequest) {
		t.Fatalf("dirty worktree: %+v", worktree.Assessment)
	}
	// The branch stays checked out in the kept worktree.
	if b := branchItem(preview, "squashed"); b == nil || b.Eligible() || !b.Assessment.Has(CleanupCheckedOutElsewhere) {
		t.Fatalf("branch: %+v", b)
	}
}

func TestCleanupSkipsSquashMergedBranchThatMovedAfterReview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "checkout", "-b", "done")
	actionTestWrite(t, filepath.Join(repo, "done"), "work\n")
	actionTestCommit(t, repo, "done work")
	actionGit(t, repo, "push", "-u", "origin", "done")
	actionGit(t, repo, "checkout", "main")
	actionGit(t, repo, "push", "origin", "--delete", "done")
	tip := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "done")))
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"done": {mergedPR(7, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	b := branchItem(preview, "done")
	if b == nil || !b.Eligible() {
		t.Fatalf("branch: %+v", b)
	}
	actionGit(t, repo, "update-ref", "refs/heads/done", strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "main"))))
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{b.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "moved since review") {
		t.Fatalf("results %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/done")
}

func TestMergeEvidenceAndRestoreSource(t *testing.T) {
	git := fakeGitHubGit{urls: map[string]string{"origin": "git@github.com:me/widgets.git", "lab": "https://gitlab.com/me/widgets.git"}}
	fork := github.PullRequest{Number: 7, State: "MERGED", BaseRef: "main", BaseRepository: "acme/widgets", IntoDefault: true, HeadOID: headA, URL: "https://github.com/acme/widgets/pull/7"}
	gh := NewGitHub(git, &fakePulls{prs: map[string][]github.PullRequest{"fix": {fork}}}, nil)
	verdicts := gh.MergeEvidence(context.Background(), "/repo", []gitcli.Branch{
		upstream("fix", "origin", "fix"), upstream("plain", "origin", "plain"), upstream("lab", "lab", "lab"), {Name: "local", OID: headA},
	})
	if v := verdicts["fix"]; len(verdicts) != 1 || !v.Proven || v.RestoreFrom != "https://github.com/acme/widgets" {
		t.Fatalf("verdicts %+v", verdicts)
	}
	failing := NewGitHub(git, &fakePulls{err: github.ErrRateLimited}, nil)
	if v := failing.MergeEvidence(context.Background(), "/repo", []gitcli.Branch{upstream("fix", "origin", "fix")})["fix"]; !v.Failed || v.Note != "GitHub API rate limit reached; gitperch checks again later" {
		t.Fatalf("failed lookup: %+v", v)
	}
	if got := restoreSource("origin", "github.com", "acme/widgets", PullRequest{BaseRepository: "ACME/widgets"}); got != "origin" {
		t.Fatalf("same repository: %q", got)
	}
	// The base repository's URL comes from the host and name, whatever the
	// pull request's URL looks like.
	odd := PullRequest{Number: 7, BaseRepository: "acme/widgets", URL: "https://ghe.example.com/acme/widgets/pull/7/files"}
	if got := restoreSource("origin", "ghe.example.com", "me/widgets", odd); got != "https://ghe.example.com/acme/widgets" {
		t.Fatalf("fork: %q", got)
	}
}
