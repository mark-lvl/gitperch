package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitcli "github.com/markkaghazgarian/gitperch/internal/git"
)

func TestPushAcceptsOnlySelectedBranchFromShortFullAndWildcardMappings(t *testing.T) {
	cases := []struct {
		name, spec, destination string
	}{
		{"short", "feature/selected:target", "target"},
		{"full", "refs/heads/feature/selected:refs/heads/target", "target"},
		{"wildcard", "refs/heads/feature/*:refs/heads/*", "selected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, remote := actionTestRepoWithRemote(t)
			destinationRef := "refs/heads/" + tc.destination
			actionGit(t, repo, "push", "origin", "main:"+destinationRef)
			actionGit(t, repo, "fetch", "origin")
			actionGit(t, repo, "checkout", "-b", "feature/selected")
			actionGit(t, repo, "branch", "--set-upstream-to=origin/"+tc.destination)
			actionTestMakeLocalCommit(t, repo, "selected outgoing")
			actionGit(t, repo, "config", "--add", "remote.origin.push", tc.spec)
			actionGit(t, repo, "config", "--add", "remote.origin.push", "refs/heads/main:refs/heads/unrelated")

			actions, _, preview := actionPrepareSync(t, repo, Push)
			if len(preview.Targets) != 1 || !preview.Targets[0].Eligible || preview.Targets[0].Scope != "refs/heads/feature/selected -> "+destinationRef {
				t.Fatalf("mapping should resolve only the selected branch: %+v", preview)
			}
			results, err := actions.Execute(context.Background(), preview.ID, nil)
			if err != nil || len(results) != 1 || results[0].State != Succeeded {
				t.Fatalf("push: %+v, %v", results, err)
			}
			localHead := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
			remoteTarget := strings.TrimSpace(string(actionGit(t, remote, "rev-parse", destinationRef)))
			if remoteTarget != localHead {
				t.Fatalf("selected destination got %s, want %s", remoteTarget, localHead)
			}
			if _, err := execGitResult(remote, "show-ref", "--verify", "refs/heads/unrelated"); err == nil {
				t.Fatal("unrelated configured mapping was pushed")
			}
		})
	}
}

func TestPushRejectsDefaultAndConfiguredDestinationTraps(t *testing.T) {
	cases := []struct {
		name, reason string
		configure    func(*testing.T, string)
	}{
		{
			name: "remote push default", reason: "triangular workflows",
			configure: func(t *testing.T, repo string) {
				backup := filepath.Join(t.TempDir(), "backup.git")
				actionBareRemote(t, backup)
				actionGit(t, repo, "remote", "add", "backup", backup)
				actionGit(t, repo, "config", "remote.pushDefault", "backup")
			},
		},
		{
			name: "unsupported push default", reason: "unsupported or ambiguous",
			configure: func(t *testing.T, repo string) {
				actionGit(t, repo, "config", "push.default", "matching")
			},
		},
		{
			name: "different configured destination", reason: "push destination mapping differs",
			configure: func(t *testing.T, repo string) {
				actionGit(t, repo, "config", "--add", "remote.origin.push", "main:refs/heads/other")
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
				t.Fatalf("unsafe push selection should be refused: %+v", preview)
			}
		})
	}
}

func TestMirrorBooleanSpellings(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		eligible    bool
	}{
		{name: "yes", value: "yes"},
		{name: "no", value: "no", eligible: true},
		{name: "empty value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			actionTestMakeLocalCommit(t, repo, "outgoing")
			if tc.name == "empty value" {
				configPath := filepath.Join(repo, ".git", "config")
				file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("\n[remote \"origin\"]\n\tmirror\n"); err != nil {
					file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				actionGit(t, repo, "config", "remote.origin.mirror", tc.value)
			}
			_, _, preview := actionPrepareSync(t, repo, Push)
			if len(preview.Targets) != 1 || preview.Targets[0].Eligible != tc.eligible {
				t.Fatalf("mirror=%q eligibility=%v, want %v: %+v", tc.value, preview.Targets[0].Eligible, tc.eligible, preview)
			}
			if !tc.eligible && !strings.Contains(preview.Targets[0].Reason, "mirror") && !strings.Contains(preview.Targets[0].Reason, "boolean") {
				t.Fatalf("unsafe mirror value was not explained: %+v", preview.Targets[0])
			}
		})
	}
}

func TestPullSkipsEqualAndAheadOnlyBranches(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ahead bool
	}{
		{name: "equal"},
		{name: "ahead only", ahead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			if tc.ahead {
				actionTestMakeLocalCommit(t, repo, "outgoing only")
			}
			_, _, preview := actionPrepareSync(t, repo, Pull)
			if len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "needs no integration") {
				t.Fatalf("pull should skip %s branch: %+v", tc.name, preview)
			}
		})
	}
}

func TestPullRefusesStagedIndexChanges(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	actionTestAdvanceRemote(t, remote, "remote update\n")
	actionTestWrite(t, filepath.Join(repo, "tracked"), "staged local edit\n")
	actionGit(t, repo, "add", "tracked")
	status := (gitcli.Runner{}).Inspect(context.Background(), repo)
	if status.Error != "" || status.Changes != 1 {
		t.Fatalf("expected staged dirty entry: %+v", status)
	}
	_, _, preview := actionPrepareSync(t, repo, Pull)
	if len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "clean index and worktree") {
		t.Fatalf("staged index changes should refuse pull: %+v", preview)
	}
}

func TestPushRefusesDivergedHistory(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	actionTestMakeLocalCommit(t, repo, "local side")
	actionTestAdvanceRemote(t, remote, "remote side\n")
	_, _, preview := actionPrepareSync(t, repo, Push)
	if len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "ahead or histories diverged") {
		t.Fatalf("diverged push should be refused: %+v", preview)
	}
}

func TestSyncRefusesDeletedUpstreamAfterPruningFetch(t *testing.T) {
	for _, action := range []Action{Push, Pull} {
		t.Run(string(action), func(t *testing.T) {
			repo, remote := actionTestRepoWithRemote(t)
			actionGit(t, remote, "update-ref", "-d", "refs/heads/main")
			_, _, preview := actionPrepareSync(t, repo, action)
			if len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "comparison is unknown after fetch") {
				t.Fatalf("deleted upstream comparison should remain unknown: %+v", preview)
			}
		})
	}
}

func TestPullSkipsWhenTrackingRefChangesAfterFinalPreview(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	peer := actionTestAdvanceRemote(t, remote, "reviewed update\n")
	actions, _, preview := actionPrepareSync(t, repo, Pull)
	if len(preview.Targets) != 1 || !preview.Targets[0].Eligible {
		t.Fatalf("expected eligible final pull preview: %+v", preview)
	}
	actionTestAdvanceFromCurrent(t, peer, "newer tracking commit\n")
	actionGit(t, repo, "fetch", "origin")
	results, err := actions.Execute(context.Background(), preview.ID, nil)
	if err != nil || len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "upstream commit changed") {
		t.Fatalf("changed tracking ref should invalidate final pull: %+v, %v", results, err)
	}
}

func TestPushSkipsPostPreviewRepositoryChanges(t *testing.T) {
	for _, change := range []string{"branch", "head", "config", "operation"} {
		t.Run(change, func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			actionTestMakeLocalCommit(t, repo, "outgoing")
			actions, _, preview := actionPrepareSync(t, repo, Push)
			if len(preview.Targets) != 1 || !preview.Targets[0].Eligible {
				t.Fatalf("expected eligible push preview: %+v", preview)
			}
			switch change {
			case "branch":
				actionGit(t, repo, "checkout", "-b", "changed-branch")
			case "head":
				actionTestMakeLocalCommit(t, repo, "post preview commit")
			case "config":
				actionGit(t, repo, "config", "remote.origin.url", filepath.Join(t.TempDir(), "changed.git"))
			case "operation":
				head := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "HEAD")))
				actionTestWrite(t, filepath.Join(repo, ".git", "MERGE_HEAD"), head+"\n")
			}
			results, err := actions.Execute(context.Background(), preview.ID, nil)
			if err != nil || len(results) != 1 || results[0].State != Skipped {
				t.Fatalf("post-preview %s change should skip push: %+v, %v", change, results, err)
			}
			if change == "operation" && !strings.Contains(results[0].Message, "operation in progress") {
				t.Fatalf("operation refusal was not explained: %+v", results[0])
			}
		})
	}
}

func TestPrepareSyncRejectsFetchScopeChangesWithoutFetching(t *testing.T) {
	for _, change := range []string{"head", "config"} {
		t.Run(change, func(t *testing.T) {
			repo, _ := actionTestRepoWithRemote(t)
			service := &syncEdgeCountingGit{Service: gitcli.Service{}}
			actions := NewActions(service, 1)
			scope, err := actions.Plan(context.Background(), Fetch, []string{repo})
			if err != nil || service.fetches != 0 {
				t.Fatalf("scope planning should not fetch: %v, count=%d", err, service.fetches)
			}
			switch change {
			case "head":
				actionTestMakeLocalCommit(t, repo, "changed after scope")
			case "config":
				actionGit(t, repo, "config", "remote.origin.url", filepath.Join(t.TempDir(), "changed.git"))
			}
			preview, err := actions.PrepareSync(context.Background(), scope.ID, Push)
			if err != nil || len(preview.Targets) != 1 || preview.Targets[0].Eligible || !strings.Contains(preview.Targets[0].Reason, "changed since fetch-scope preview") {
				t.Fatalf("stale fetch scope should be refused: %+v, %v", preview, err)
			}
			if service.fetches != 0 {
				t.Fatalf("stale fetch scope initiated %d network fetches", service.fetches)
			}
		})
	}
}

type syncEdgeCountingGit struct {
	gitcli.Service
	fetches int
}

func (g *syncEdgeCountingGit) Fetch(ctx context.Context, path string, target gitcli.FetchTarget) error {
	g.fetches++
	return g.Service.Fetch(ctx, path, target)
}
