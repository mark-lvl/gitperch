package app

import (
	"context"
	"os/exec"
	gitcli "repodash/internal/git"
	"strings"
	"testing"
)

func TestFetchPrunesTrackingOnlyAndOverridesTagConfig(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	initial := string(actionGit(t, repo, "rev-parse", "HEAD"))
	actionGit(t, repo, "branch", "unrelated")
	actionGit(t, repo, "tag", "keep-local")
	actionGit(t, repo, "update-ref", "refs/remotes/origin/deleted", strings.TrimSpace(initial))
	peer := actionTestAdvanceRemote(t, remote, "advance\n")
	actionGit(t, peer, "tag", "remote-tag")
	actionGit(t, peer, "push", "origin", "refs/tags/remote-tag")
	actionGit(t, repo, "config", "fetch.pruneTags", "true")
	actionGit(t, repo, "config", "remote.origin.pruneTags", "true")
	actionGit(t, repo, "config", "remote.origin.tagopt", "--tags")
	a := NewActions(gitcli.Service{}, 1)
	p, err := a.Plan(context.Background(), Fetch, []string{repo})
	if err != nil || !p.Targets[0].Eligible {
		t.Fatalf("%+v %v", p, err)
	}
	results, err := a.Execute(context.Background(), p.ID, nil)
	if err != nil || results[0].State != Succeeded {
		t.Fatalf("%+v %v", results, err)
	}
	for _, ref := range []string{"HEAD", "refs/heads/unrelated", "refs/tags/keep-local"} {
		if got := string(actionGit(t, repo, "rev-parse", ref)); got != initial {
			t.Fatalf("fetch changed %s", ref)
		}
	}
	for _, ref := range []string{"refs/remotes/origin/deleted", "refs/tags/remote-tag"} {
		if err := exec.Command("git", "-C", repo, "show-ref", "--verify", ref).Run(); err == nil {
			t.Fatalf("unexpected ref remains: %s", ref)
		}
	}
}
