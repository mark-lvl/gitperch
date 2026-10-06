package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

func TestPushSuccessExcludesUncommittedChanges(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	actionTestWrite(t, filepath.Join(repo, "committed"), "commit to publish\n")
	actionTestCommit(t, repo, "outgoing commit")
	actionTestWrite(t, filepath.Join(repo, "tracked"), "dirty tracked edit\n")
	actionTestWrite(t, filepath.Join(repo, "untracked-secret"), "must not be pushed\n")

	actions, _, preview := actionPrepareSync(t, repo, Push)
	if len(preview.Targets) != 1 || !preview.Targets[0].Eligible || !preview.Targets[0].DirtyExcluded {
		t.Fatalf("dirty push should review committed changes and disclose exclusions: %+v", preview)
	}
	if preview.Targets[0].Commit == "" || preview.Targets[0].Scope != "refs/heads/main -> refs/heads/main" {
		t.Fatalf("push preview lacks exact refs and commit: %+v", preview.Targets[0])
	}
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Succeeded {
		t.Fatalf("push: %+v, %v", results, err)
	}
	remoteTracked := strings.TrimSuffix(string(actionGit(t, remote, "show", "refs/heads/main:tracked")), "\n")
	if remoteTracked != "initial" {
		t.Fatalf("uncommitted tracked edit was included in push: %q", remoteTracked)
	}
	remoteCommitted := strings.TrimSuffix(string(actionGit(t, remote, "show", "refs/heads/main:committed")), "\n")
	if remoteCommitted != "commit to publish" {
		t.Fatalf("reviewed commit was not pushed: %q", remoteCommitted)
	}
	if _, err := execGitResult(remote, "cat-file", "-e", "refs/heads/main:untracked-secret"); err == nil {
		t.Fatal("untracked file was included in push")
	}
}

func TestSyncPreparationUsesReviewedFetchScope(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	actions := NewActions(gitcli.Service{}, 1)
	scope, err := actions.Plan(context.Background(), Fetch, []string{repo})
	if err != nil || len(scope.Targets) != 1 || !scope.Targets[0].Eligible || scope.Targets[0].Remote != "origin" {
		t.Fatalf("fetch scope preview: %+v, %v", scope, err)
	}
	if _, err := actions.PrepareSync(context.Background(), scope.ID, Fetch); err == nil || !strings.Contains(err.Error(), "synchronization action") {
		t.Fatalf("PrepareSync should require push or pull: %v", err)
	}
	final, err := actions.PrepareSync(context.Background(), scope.ID, Push)
	if err != nil || len(final.Targets) != 1 || final.Targets[0].Action != Push || final.Targets[0].Eligible {
		t.Fatalf("push preflight should complete and show no outgoing commits: %+v, %v", final, err)
	}
	if final.ID == scope.ID {
		t.Fatal("final preview should have a distinct review ID")
	}
	if _, err := actions.Execute(context.Background(), scope.ID, nil); err == nil {
		t.Fatal("the fetch-scope ID must no longer be executable after preflight")
	}
	_ = remote
}

func TestPullFastForwardsToReviewedCommit(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	peer := actionTestAdvanceRemote(t, remote, "reviewed update\n")
	reviewed := strings.TrimSpace(string(actionGit(t, peer, "rev-parse", "HEAD")))
	actions, _, preview := actionPrepareSync(t, repo, Pull)
	if len(preview.Targets) != 1 || !preview.Targets[0].Eligible || preview.Targets[0].Commit != reviewed {
		t.Fatalf("pull preview should pin the fetched commit: %+v", preview)
	}
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Succeeded {
		t.Fatalf("pull: %+v, %v", results, err)
	}
	got := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
	if got != reviewed {
		t.Fatalf("pull integrated %s, want reviewed commit %s", got, reviewed)
	}
	content, err := os.ReadFile(filepath.Join(repo, "peer-file"))
	if err != nil || string(content) != "reviewed update\n" {
		t.Fatalf("pulled content = %q, %v", content, err)
	}
}

func TestPullExecutesExactCommitReviewedBeforeRemoteAdvances(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	peer := actionTestAdvanceRemote(t, remote, "reviewed commit\n")
	reviewed := strings.TrimSpace(string(actionGit(t, peer, "rev-parse", "HEAD")))
	actions, _, preview := actionPrepareSync(t, repo, Pull)
	if len(preview.Targets) != 1 || !preview.Targets[0].Eligible || preview.Targets[0].Commit != reviewed {
		t.Fatalf("preview: %+v", preview)
	}
	secondPeer := actionTestAdvanceFromCurrent(t, peer, "later remote commit\n")
	remoteHead := strings.TrimSpace(string(actionGit(t, secondPeer, "rev-parse", "HEAD")))
	if remoteHead == reviewed {
		t.Fatal("peer update did not advance remote")
	}
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Succeeded {
		t.Fatalf("pull should integrate the reviewed tracking commit: %+v, %v", results, err)
	}
	got := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
	if got != reviewed {
		t.Fatalf("pull followed later remote change: got %s, reviewed %s", got, reviewed)
	}
}

func TestPullRefusesDirtyAndUntrackedWorktrees(t *testing.T) {
	for _, dirty := range []string{"tracked", "untracked"} {
		t.Run(dirty, func(t *testing.T) {
			repo, remote := actionTestRepoWithRemote(t)
			actionTestAdvanceRemote(t, remote, "remote update\n")
			if dirty == "tracked" {
				actionTestWrite(t, filepath.Join(repo, "tracked"), "local edit\n")
			} else {
				actionTestWrite(t, filepath.Join(repo, "untracked"), "local file\n")
			}
			actions, _, preview := actionPrepareSync(t, repo, Pull)
			if len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "clean index and worktree") {
				t.Fatalf("dirty worktree should be refused: %+v", preview)
			}
			before := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
			results, err := actions.Execute(context.Background(), preview.ID, nil)
			if err != nil || len(results) != 1 || results[0].State != Skipped {
				t.Fatalf("dirty pull should be skipped: %+v, %v", results, err)
			}
			after := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
			if after != before {
				t.Fatalf("refused pull changed HEAD from %s to %s", before, after)
			}
		})
	}
}

func TestPushAndPullRefuseUnsafeRepositoryStates(t *testing.T) {
	tests := []struct {
		name, action, expected string
		prepare                func(*testing.T, string, string)
	}{
		{
			name: "push when upstream is ahead", action: "push", expected: "upstream is ahead",
			prepare: func(t *testing.T, repo, remote string) { actionTestAdvanceRemote(t, remote, "peer ahead\n") },
		},
		{
			name: "pull when histories diverged", action: "pull", expected: "histories diverged",
			prepare: func(t *testing.T, repo, remote string) {
				actionTestMakeLocalCommit(t, repo, "local divergent commit")
				actionTestAdvanceRemote(t, remote, "peer divergent commit\n")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, remote := actionTestRepoWithRemote(t)
			tc.prepare(t, repo, remote)
			action := Push
			if tc.action == "pull" {
				action = Pull
			}
			_, _, preview := actionPrepareSync(t, repo, action)
			if len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, tc.expected) {
				t.Fatalf("unsafe sync state should be refused: %+v", preview)
			}
		})
	}

	for _, action := range []Action{Push, Pull} {
		for _, state := range []string{"no upstream", "detached", "unborn"} {
			t.Run(string(action)+"/"+state, func(t *testing.T) {
				var repo string
				var remote string
				switch state {
				case "no upstream":
					repo, remote = actionTestRepoWithRemote(t)
					actionGit(t, repo, "branch", "--unset-upstream")
				case "detached":
					repo, remote = actionTestRepoWithRemote(t)
					actionGit(t, repo, "checkout", "--detach")
				case "unborn":
					repo = actionTestRepo(t)
					remote = filepath.Join(t.TempDir(), "empty.git")
					actionBareRemote(t, remote)
					actionGit(t, repo, "remote", "add", "origin", remote)
				}
				_, _, preview := actionPrepareSync(t, repo, action)
				if len(preview.Targets) != 1 || preview.Targets[0].Eligible {
					t.Fatalf("%s state should be refused for %s: %+v", state, action, preview)
				}
				if state == "no upstream" && !strings.Contains(preview.Targets[0].Reason, "configured upstream") {
					t.Fatalf("no-upstream reason = %q", preview.Targets[0].Reason)
				}
				if (state == "detached" || state == "unborn") && !strings.Contains(preview.Targets[0].Reason, "detached or unborn") {
					t.Fatalf("%s reason = %q", state, preview.Targets[0].Reason)
				}
				_ = remote
			})
		}
	}
}

func TestPullSkipsStateChangesAfterFinalPreview(t *testing.T) {
	for _, change := range []string{"head", "config", "dirty"} {
		t.Run(change, func(t *testing.T) {
			repo, remote := actionTestRepoWithRemote(t)
			actionTestAdvanceRemote(t, remote, "remote update\n")
			actions, _, preview := actionPrepareSync(t, repo, Pull)
			if len(preview.Targets) != 1 || !preview.Targets[0].Eligible {
				t.Fatalf("expected eligible pull preview: %+v", preview)
			}
			switch change {
			case "head":
				actionTestMakeLocalCommit(t, repo, "new local commit")
			case "config":
				actionGit(t, repo, "config", "remote.origin.url", filepath.Join(t.TempDir(), "changed.git"))
			case "dirty":
				actionTestWrite(t, filepath.Join(repo, "new-local-file"), "local change\n")
			}
			results, err := actions.Execute(context.Background(), preview.ID, nil)
			if err != nil || len(results) != 1 || results[0].State != Skipped {
				t.Fatalf("post-preview %s change should skip pull: %+v, %v", change, results, err)
			}
		})
	}
}

func TestPushRefusesRemoteRaceWithoutForce(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	actionTestMakeLocalCommit(t, repo, "local outgoing commit")
	actions, _, preview := actionPrepareSync(t, repo, Push)
	if len(preview.Targets) != 1 || !preview.Targets[0].Eligible {
		t.Fatalf("expected eligible push preview: %+v", preview)
	}
	peer := actionTestAdvanceRemote(t, remote, "racing remote commit\n")
	peerHead := strings.TrimSpace(string(actionGit(t, peer, "rev-parse", "HEAD")))
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Failed {
		t.Fatalf("remote race must fail rather than force: %+v, %v", results, err)
	}
	remoteHead := strings.TrimSpace(string(actionGit(t, remote, "rev-parse", "refs/heads/main")))
	if remoteHead != peerHead {
		t.Fatalf("failed push changed remote ref: got %s, peer has %s", remoteHead, peerHead)
	}
}

// With --porcelain, Git reports why a ref was rejected on stdout; stderr only
// says the push failed (plus optional advice), so the reason must be kept.
func TestPushRejectionReportsGitReason(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	actionGit(t, repo, "config", "advice.pushUpdateRejected", "false")
	actionTestMakeLocalCommit(t, repo, "local outgoing commit")
	actions, _, preview := actionPrepareSync(t, repo, Push)
	actionTestAdvanceRemote(t, remote, "racing remote commit\n")
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Failed {
		t.Fatalf("remote race must fail: %+v, %v", results, err)
	}
	if want := "[rejected] (fetch first) for refs/heads/main"; !strings.Contains(results[0].Message, want) {
		t.Fatalf("message %q lacks Git's reason %q", results[0].Message, want)
	}
}

func TestPushConfigurationSafetyAndValidMapping(t *testing.T) {
	cases := []struct {
		name, reason string
		configure    func(*testing.T, string)
	}{
		{
			name: "triangular push remote", reason: "triangular workflows",
			configure: func(t *testing.T, repo string) {
				backup := filepath.Join(t.TempDir(), "backup.git")
				actionBareRemote(t, backup)
				actionGit(t, repo, "remote", "add", "backup", backup)
				actionGit(t, repo, "config", "branch.main.pushremote", "backup")
			},
		},
		{
			name: "multiple push URLs", reason: "multiple or missing push URLs",
			configure: func(t *testing.T, repo string) {
				first := filepath.Join(t.TempDir(), "push-one.git")
				second := filepath.Join(t.TempDir(), "push-two.git")
				actionBareRemote(t, first)
				actionBareRemote(t, second)
				actionGit(t, repo, "remote", "set-url", "--push", "--add", "origin", first)
				actionGit(t, repo, "remote", "set-url", "--push", "--add", "origin", second)
			},
		},
		{
			name: "force configured refspec", reason: "force-configured",
			configure: func(t *testing.T, repo string) {
				actionGit(t, repo, "config", "--add", "remote.origin.push", "+refs/heads/main:refs/heads/main")
			},
		},
		{
			name: "mirror mode", reason: "mirror remote",
			configure: func(t *testing.T, repo string) { actionGit(t, repo, "config", "remote.origin.mirror", "true") },
		},
		{
			name: "ambiguous push mapping", reason: "ambiguous",
			configure: func(t *testing.T, repo string) {
				actionGit(t, repo, "config", "--add", "remote.origin.push", "refs/heads/main:refs/heads/main")
				actionGit(t, repo, "config", "--add", "remote.origin.push", "refs/heads/main:refs/heads/other")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			actionTestMakeLocalCommit(t, repo, "outgoing")
			tc.configure(t, repo)
			_, _, preview := actionPrepareSync(t, repo, Push)
			if len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, tc.reason) {
				t.Fatalf("unsafe push config should be refused: %+v", preview)
			}
		})
	}

	t.Run("valid explicit upstream mapping", func(t *testing.T) {
		repo, remote := actionTestRepoWithRemote(t)
		actionTestMakeLocalCommit(t, repo, "mapped outgoing commit")
		actionGit(t, repo, "config", "--add", "remote.origin.push", "refs/heads/main:refs/heads/main")
		actions, _, preview := actionPrepareSync(t, repo, Push)
		if len(preview.Targets) != 1 || !preview.Targets[0].Eligible || preview.Targets[0].Scope != "refs/heads/main -> refs/heads/main" {
			t.Fatalf("conventional explicit mapping should be supported: %+v", preview)
		}
		results, err := actions.Execute(context.Background(), preview.ID, nil)
		if err != nil || len(results) != 1 || results[0].State != Succeeded {
			t.Fatalf("valid explicit push: %+v, %v", results, err)
		}
		remoteHead := strings.TrimSpace(string(actionGit(t, remote, "rev-parse", "refs/heads/main")))
		repoHead := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
		if remoteHead != repoHead {
			t.Fatalf("valid reviewed push did not update expected remote branch")
		}
	})
}

func TestFetchPushPullSelectionAndPrepareSyncMutationProtection(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	actionTestMakeLocalCommit(t, repo, "outgoing")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.Plan(context.Background(), Fetch, []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	copyPreview := preview
	copyPreview.Targets = append([]Target(nil), preview.Targets...)
	copyPreview.Targets[0].Remote = "changed"
	copyPreview.Targets[0].Scope = "refs/heads/evil:refs/heads/main"
	final, err := actions.PrepareSync(context.Background(), preview.ID, Push)
	if err != nil || len(final.Targets) != 1 || !final.Targets[0].Eligible || final.Targets[0].Remote != "origin" {
		t.Fatalf("mutating a copied fetch preview changed the private scope: %+v, %v", final, err)
	}
	results, err := actions.Execute(context.Background(), final.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Succeeded {
		t.Fatalf("prepared push: %+v, %v", results, err)
	}
	if strings.TrimSpace(string(actionGit(t, remote, "rev-parse", "refs/heads/main"))) != strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD"))) {
		t.Fatal("prepared push did not target reviewed branch")
	}
}

func actionPrepareSync(t *testing.T, repo string, action Action) (*Actions, Preview, Preview) {
	t.Helper()
	actions := NewActions(gitcli.Service{}, 1)
	scope, err := actions.Plan(context.Background(), Fetch, []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := actions.PrepareSync(context.Background(), scope.ID, action)
	if err != nil {
		t.Fatal(err)
	}
	return actions, scope, preview
}

func actionTestMakeLocalCommit(t *testing.T, repo, message string) {
	t.Helper()
	actionTestWrite(t, filepath.Join(repo, "local-"+strings.ReplaceAll(message, " ", "-")), message+"\n")
	actionTestCommit(t, repo, message)
}

func actionTestAdvanceFromCurrent(t *testing.T, repo, content string) string {
	t.Helper()
	actionTestWrite(t, filepath.Join(repo, "later-file"), content)
	actionTestCommit(t, repo, "later remote commit")
	actionGit(t, repo, "push", "origin", "main")
	return repo
}

func execGitResult(path string, args ...string) ([]byte, error) {
	return exec.Command("git", append([]string{"--git-dir", path}, args...)...).CombinedOutput()
}
