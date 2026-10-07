package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
)

func loadRows(t *testing.T, roots ...string) Snapshot {
	t.Helper()
	snapshot, err := Load(context.Background(), discovery.Options{Roots: roots, MaxDepth: 4}, gitcli.Runner{}, 4)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func rowByPath(t *testing.T, rows []Row, path string) Row {
	t.Helper()
	for _, row := range rows {
		if row.Path == path {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", path, rows)
	return Row{}
}

func TestLoadAddsNestedOutsideAndStaleWorktrees(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	actionGit(t, root, "init", "-b", "main", repo)
	actionGit(t, repo, "config", "user.name", "Test")
	actionGit(t, repo, "config", "user.email", "test@example.invalid")
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	nested := filepath.Join(repo, ".worktrees", "nested")
	outside := filepath.Join(t.TempDir(), "outside")
	stale := filepath.Join(t.TempDir(), "stale")
	actionGit(t, repo, "worktree", "add", "-b", "nested", nested)
	actionGit(t, repo, "worktree", "add", "-b", "outside", outside)
	actionGit(t, repo, "worktree", "add", "--lock", "--reason", "agent session", "-b", "stale", stale)
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}
	snapshot := loadRows(t, root)
	if len(snapshot.Rows) != 4 || len(snapshot.Warnings) != 0 {
		t.Fatalf("rows %+v warnings %+v", snapshot.Rows, snapshot.Warnings)
	}
	main := rowByPath(t, snapshot.Rows, repo)
	if main.Worktree == nil || !main.Worktree.Main || main.Worktree.MainPath != repo {
		t.Fatalf("main: %+v", main.Worktree)
	}
	n := rowByPath(t, snapshot.Rows, nested)
	if !n.Worktree.Linked || n.Worktree.MainPath != repo || n.Worktree.OutsideRoots || n.Status.Branch != "nested" {
		t.Fatalf("nested: %+v %+v", n.Worktree, n.Status)
	}
	o := rowByPath(t, snapshot.Rows, outside)
	if !o.Worktree.OutsideRoots || o.Status.Error != "" {
		t.Fatalf("outside: %+v %+v", o.Worktree, o.Status)
	}
	s := rowByPath(t, snapshot.Rows, stale)
	if !s.Worktree.Locked || !s.Worktree.Prunable || s.Worktree.LockReason != "agent session" || s.Selectable() {
		t.Fatalf("stale locked: %+v", s.Worktree)
	}
}

func TestLoadAddsMainWhenOnlyLinkedWorktreeIsScanned(t *testing.T) {
	repo := actionTestRepo(t)
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	root := t.TempDir()
	linked := filepath.Join(root, "linked")
	actionGit(t, repo, "worktree", "add", "-b", "linked", linked)
	snapshot := loadRows(t, root)
	main := rowByPath(t, snapshot.Rows, repo)
	if !main.Worktree.Main || !main.Worktree.OutsideRoots {
		t.Fatalf("main outside roots: %+v", main.Worktree)
	}
	if rowByPath(t, snapshot.Rows, linked).Worktree.MainPath != repo {
		t.Fatal("linked worktree is not grouped under its main worktree")
	}
}

func TestLoadMatchesSymlinkedWorktreePaths(t *testing.T) {
	real := t.TempDir()
	repo := filepath.Join(real, "repo")
	actionGit(t, real, "init", "-b", "main", repo)
	actionGit(t, repo, "config", "user.name", "Test")
	actionGit(t, repo, "config", "user.email", "test@example.invalid")
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	actionGit(t, repo, "worktree", "add", "-b", "wt", filepath.Join(real, "wt"))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	// Discovery refuses symlink roots, so scan the real root but present
	// rows through the link the way a user-named path would arrive.
	snapshot := loadRows(t, real)
	if len(snapshot.Rows) != 2 {
		t.Fatalf("worktree listed twice or missing: %+v", snapshot.Rows)
	}
	rows, warnings := attachWorktrees(context.Background(), []Row{{Repository: repositoryAt(t, filepath.Join(link, "repo")), Status: snapshot.Rows[0].Status}}, gitcli.Runner{}, 2, []string{link})
	if len(warnings) != 0 || len(rows) != 2 {
		t.Fatalf("symlinked path duplicated: %+v %+v", rows, warnings)
	}
}

func TestLoadListsUninspectableWorktreeOnce(t *testing.T) {
	for _, broken := range []string{"linked", "main"} {
		t.Run(broken, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			actionGit(t, root, "init", "-b", "main", repo)
			actionGit(t, repo, "config", "user.name", "Test")
			actionGit(t, repo, "config", "user.email", "test@example.invalid")
			actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
			actionTestCommit(t, repo, "initial")
			wt := filepath.Join(root, "wt")
			actionGit(t, repo, "worktree", "add", "-b", "wt", wt)
			index, path := filepath.Join(repo, ".git", "worktrees", "wt", "index"), wt
			if broken == "main" {
				index, path = filepath.Join(repo, ".git", "index"), repo
			}
			actionTestWrite(t, index, "not an index")
			snapshot := loadRows(t, root)
			if len(snapshot.Rows) != 2 {
				t.Fatalf("rows %+v", snapshot.Rows)
			}
			row := rowByPath(t, snapshot.Rows, path)
			if row.Status.Error == "" || row.Worktree == nil || row.Worktree.Main != (broken == "main") || row.Worktree.MainPath != repo {
				t.Fatalf("broken %s row: %+v %+v", broken, row.Status, row.Worktree)
			}
		})
	}
}

func TestLoadWarnsWhenWorktreeListFails(t *testing.T) {
	repo := actionTestRepo(t)
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	rows := []Row{{Repository: repositoryAt(t, repo)}}
	rows[0].Status = gitcli.Runner{}.Inspect(context.Background(), repo)
	got, warnings := attachWorktrees(context.Background(), rows, failingLister{}, 1, []string{filepath.Dir(repo)})
	if len(got) != 1 || got[0].Worktree != nil || len(warnings) != 1 {
		t.Fatalf("rows %+v warnings %+v", got, warnings)
	}
}

type oldGit struct{ failingLister }

func (oldGit) SupportsWorktreeInventory(context.Context) bool { return false }

func TestLoadSkipsInventoryQuietlyOnOldGit(t *testing.T) {
	repo := actionTestRepo(t)
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	rows := []Row{{Repository: repositoryAt(t, repo)}}
	rows[0].Status = gitcli.Runner{}.Inspect(context.Background(), repo)
	got, warnings := attachWorktrees(context.Background(), rows, oldGit{}, 1, []string{filepath.Dir(repo)})
	if len(got) != 1 || got[0].Worktree != nil || len(warnings) != 0 {
		t.Fatalf("old Git must disable the inventory silently: rows %+v warnings %+v", got, warnings)
	}
}

type failingLister struct{ gitcli.Runner }

func (failingLister) SupportsWorktreeInventory(context.Context) bool { return true }

func (failingLister) Worktrees(context.Context, string) ([]gitcli.Worktree, error) {
	return nil, errors.New("git worktree: unknown option -z")
}

func repositoryAt(t *testing.T, path string) repository.Repository {
	t.Helper()
	repo, err := repository.New(path)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestWriteTableMarksWorktreeStates(t *testing.T) {
	rows := []Row{
		{Repository: repository.Repository{Name: "stale", Path: "/x/stale"}, Worktree: &WorktreeInfo{Linked: true, Prunable: true, PrunableReason: "directory missing"}},
		{Repository: repository.Repository{Name: "bare", Path: "/x/bare"}, Worktree: &WorktreeInfo{Main: true, Bare: true}},
		{Repository: repository.Repository{Name: "locked", Path: "/x/locked"}, Worktree: &WorktreeInfo{Linked: true, Locked: true}},
		{Repository: repository.Repository{Name: "detached", Path: "/x/detached"}, Status: repository.Status{Detached: true}},
		{Repository: repository.Repository{Name: "unborn", Path: "/x/unborn"}, Status: repository.Status{Branch: "main", Unborn: true}},
		{Repository: repository.Repository{Name: "broken", Path: "/x/broken"}, Status: repository.Status{Error: "index corrupt"}},
	}
	var out strings.Builder
	if err := WriteTable(&out, rows); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"stale worktree: directory missing", "bare repository", "locked worktree", "no upstream"} {
		if !strings.Contains(got, want) {
			t.Errorf("table lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "no upstream") != 1 {
		t.Errorf("only the locked branch row may report no upstream, as Attention does:\n%s", got)
	}
}
