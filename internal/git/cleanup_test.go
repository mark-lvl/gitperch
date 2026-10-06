package git

import (
	"context"
	"errors"
	"os"
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
