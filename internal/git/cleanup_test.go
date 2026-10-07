package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoWithRemote(t *testing.T) (repo, remote string) {
	t.Helper()
	repo = disposable(t)
	write(t, filepath.Join(repo, "f"), "x\n")
	commit(t, repo)
	remote = filepath.Join(t.TempDir(), "remote.git")
	gitCmd(t, filepath.Dir(remote), "init", "--bare", "-b", "main", remote)
	gitCmd(t, repo, "remote", "add", "origin", remote)
	gitCmd(t, repo, "push", "-u", "origin", "main")
	return repo, remote
}

func TestRemoteDefaultRef(t *testing.T) {
	repo, _ := repoWithRemote(t)
	r := Runner{}
	if _, err := r.RemoteDefaultRef(context.Background(), repo, "origin"); !errors.Is(err, ErrNoDefaultRef) {
		t.Fatalf("missing origin/HEAD: %v", err)
	}
	gitCmd(t, repo, "remote", "set-head", "origin", "main")
	ref, err := r.RemoteDefaultRef(context.Background(), repo, "origin")
	if err != nil || ref != "refs/remotes/origin/main" {
		t.Fatalf("ref %q, %v", ref, err)
	}
}

func TestResolveCommitAndIsAncestor(t *testing.T) {
	repo, _ := repoWithRemote(t)
	r := Runner{}
	head, err := r.ResolveCommit(context.Background(), repo, "refs/heads/main")
	if err != nil || !objectID(head) {
		t.Fatalf("head %q, %v", head, err)
	}
	if _, err := r.ResolveCommit(context.Background(), repo, "--help"); err == nil {
		t.Fatal("non-ref argument accepted")
	}
	if _, err := r.ResolveCommit(context.Background(), repo, "refs/heads/missing"); err == nil {
		t.Fatal("missing ref resolved")
	}
	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, filepath.Join(repo, "g"), "y\n")
	commit(t, repo)
	feature, _ := r.ResolveCommit(context.Background(), repo, "refs/heads/feature")
	if ok, err := r.IsAncestor(context.Background(), repo, head, "refs/heads/feature"); err != nil || !ok {
		t.Fatalf("main in feature: %v %v", ok, err)
	}
	if ok, err := r.IsAncestor(context.Background(), repo, feature, "refs/remotes/origin/main"); err != nil || ok {
		t.Fatalf("feature in origin/main: %v %v", ok, err)
	}
	if _, err := r.IsAncestor(context.Background(), repo, "zzz", "refs/heads/main"); err == nil {
		t.Fatal("invalid oid accepted")
	}
}

func TestIgnoredFiles(t *testing.T) {
	repo := disposable(t)
	write(t, filepath.Join(repo, ".gitignore"), ".env\nnode_modules/\n")
	commit(t, repo)
	r := Runner{}
	if got, err := r.IgnoredFiles(context.Background(), repo); err != nil || len(got) != 0 {
		t.Fatalf("clean: %v %v", got, err)
	}
	write(t, filepath.Join(repo, ".env"), "SECRET=1\n")
	if err := os.MkdirAll(filepath.Join(repo, "node_modules", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "node_modules", "a", "b"), "1")
	got, err := r.IgnoredFiles(context.Background(), repo)
	if err != nil || strings.Join(got, ",") != ".env,node_modules/" {
		t.Fatalf("ignored: %v %v", got, err)
	}
}

func TestResolveFetchStillValidatesRemote(t *testing.T) {
	repo, _ := repoWithRemote(t)
	r := Runner{}
	m, err := r.Metadata(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	a, errA := r.ResolveFetch(context.Background(), repo, m)
	b, errB := r.ResolveRemote(context.Background(), repo, m, "origin")
	if errA != nil || errB != nil || a != b {
		t.Fatalf("fetch %+v %v, remote %+v %v", a, errA, b, errB)
	}
	if _, err := r.ResolveRemote(context.Background(), repo, m, "nope"); err == nil {
		t.Fatal("unknown remote accepted")
	}
}

func TestPruneWorktreesKeepsLockedRecords(t *testing.T) {
	repo := disposable(t)
	write(t, filepath.Join(repo, "f"), "x\n")
	commit(t, repo)
	base := t.TempDir()
	gitCmd(t, repo, "worktree", "add", "-b", "a", filepath.Join(base, "a"))
	gitCmd(t, repo, "worktree", "add", "--lock", "-b", "b", filepath.Join(base, "b"))
	os.RemoveAll(filepath.Join(base, "a"))
	os.RemoveAll(filepath.Join(base, "b"))
	if err := (Runner{}).PruneWorktrees(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	got, _ := (Runner{}).Worktrees(context.Background(), repo)
	if len(got) != 2 || !got[1].Locked {
		t.Fatalf("after prune: %+v", got)
	}
}

func TestRemoveWorktreeRefusesDirtyAndForceIsNeverUsed(t *testing.T) {
	repo := disposable(t)
	write(t, filepath.Join(repo, "f"), "x\n")
	commit(t, repo)
	wt := filepath.Join(t.TempDir(), "wt")
	gitCmd(t, repo, "worktree", "add", "-b", "wt", wt)
	write(t, filepath.Join(wt, "new"), "untracked")
	r := Runner{}
	if err := r.RemoveWorktree(context.Background(), repo, wt); err == nil {
		t.Fatal("dirty worktree removed")
	}
	if _, err := os.Stat(filepath.Join(wt, "new")); err != nil {
		t.Fatal("untracked file lost")
	}
	os.Remove(filepath.Join(wt, "new"))
	if err := r.RemoveWorktree(context.Background(), repo, "relative/wt"); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := r.RemoveWorktree(context.Background(), repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still present: %v", err)
	}
}

func TestLocalBranches(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "branch", "plain")
	gitCmd(t, repo, "branch", "tracked")
	gitCmd(t, repo, "push", "-u", "origin", "tracked")
	gitCmd(t, repo, "update-ref", "-d", "refs/remotes/origin/tracked")
	got, err := (Runner{}).LocalBranches(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Branch{}
	for _, b := range got {
		byName[b.Name] = b
	}
	if len(got) != 3 || !objectID(byName["plain"].OID) || byName["plain"].Upstream != "" || !byName["tracked"].Gone || byName["main"].Gone {
		t.Fatalf("branches: %+v", got)
	}
}

func TestDeleteBranchComparesAndCleansConfig(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "branch", "done")
	gitCmd(t, repo, "config", "branch.done.description", "agent work")
	oid := strings.TrimSpace(string(gitCmd(t, repo, "rev-parse", "done")))
	r := Runner{}
	if err := r.DeleteBranch(context.Background(), repo, "done", strings.Repeat("1", 40)); err == nil {
		t.Fatal("deleted with a stale expected OID")
	}
	if err := r.DeleteBranch(context.Background(), repo, "done", oid); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/done").Output(); len(out) != 0 {
		t.Fatal("branch still exists")
	}
	if out, _ := exec.Command("git", "-C", repo, "config", "--get", "branch.done.description").Output(); len(out) != 0 {
		t.Fatal("branch config left behind")
	}
	gitCmd(t, repo, "branch", "noconfig")
	oid = strings.TrimSpace(string(gitCmd(t, repo, "rev-parse", "noconfig")))
	if err := r.DeleteBranch(context.Background(), repo, "noconfig", oid); err != nil {
		t.Fatalf("missing config section is not an error: %v", err)
	}
}

func TestLocalBranchesExposesSymbolicRefs(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "symbolic-ref", "refs/heads/master", "refs/heads/main")
	got, err := (Runner{}).LocalBranches(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Branch{}
	for _, b := range got {
		byName[b.Name] = b
	}
	if byName["master"].Symref != "refs/heads/main" || byName["main"].Symref != "" {
		t.Fatalf("symrefs: %+v", got)
	}
}

func TestDeleteBranchOnSymbolicRefRemovesOnlyTheAlias(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "symbolic-ref", "refs/heads/master", "refs/heads/main")
	oid := strings.TrimSpace(string(gitCmd(t, repo, "rev-parse", "main")))
	gitCmd(t, repo, "checkout", "--detach")
	if err := (Runner{}).DeleteBranch(context.Background(), repo, "master", oid); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/main").Output(); strings.TrimSpace(string(out)) != oid {
		t.Fatal("deleting the alias deleted its target")
	}
	if out, _ := exec.Command("git", "-C", repo, "symbolic-ref", "--quiet", "refs/heads/master").Output(); len(out) != 0 {
		t.Fatal("alias still exists")
	}
}
