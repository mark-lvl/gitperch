package discovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"repodash/internal/repository"
)

func TestScanFindsGitDirectoryAndFile(t *testing.T) {
	root := t.TempDir()
	gitDirRepo := filepath.Join(root, "dir-repo")
	gitFileRepo := filepath.Join(root, "file-repo")
	makeRepo(t, gitDirRepo, true)
	makeRepo(t, gitFileRepo, false)

	got, err := Scan(context.Background(), Options{Roots: []string{root}, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := []repository.Repository{
		{Path: gitDirRepo, Name: "dir-repo"},
		{Path: gitFileRepo, Name: "file-repo"},
	}
	if !reflect.DeepEqual(got.Repositories, want) {
		t.Fatalf("repositories = %#v, want %#v", got.Repositories, want)
	}
}

func TestScanHonorsDepthIgnoresNestedRepositoriesAndOverlap(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, filepath.Join(root, "root-repo"), true)
	outer := filepath.Join(root, "outer")
	inner := filepath.Join(outer, "inner")
	makeRepo(t, outer, true)
	makeRepo(t, inner, true)
	ignored := filepath.Join(root, "vendor", "hidden")
	makeRepo(t, ignored, true)

	got, err := Scan(context.Background(), Options{
		Roots: []string{root, outer}, MaxDepth: 3, IgnoreDirs: []string{"vendor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 2 || got.Repositories[0].Path != outer || got.Repositories[1].Path != filepath.Join(root, "root-repo") {
		t.Fatalf("repositories = %#v, want root-repo and outer (without nested or ignored repos)", got.Repositories)
	}

	depthZero, err := Scan(context.Background(), Options{Roots: []string{root, outer}, MaxDepth: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(depthZero.Repositories) != 1 || depthZero.Repositories[0].Path != outer {
		t.Fatalf("depth-zero repositories = %#v, want explicit repository root only", depthZero.Repositories)
	}
}

func TestScanMaxDepthBoundaries(t *testing.T) {
	root := t.TempDir()
	depthOne := filepath.Join(root, "one")
	depthTwo := filepath.Join(root, "branch", "two")
	makeRepo(t, depthOne, true)
	makeRepo(t, depthTwo, true)

	for _, tc := range []struct {
		depth int
		want  []string
	}{
		{depth: 0, want: nil},
		{depth: 1, want: []string{depthOne}},
		{depth: 2, want: []string{depthOne, depthTwo}},
	} {
		got, err := Scan(context.Background(), Options{Roots: []string{root}, MaxDepth: tc.depth})
		if err != nil {
			t.Fatalf("depth %d: %v", tc.depth, err)
		}
		var paths []string
		for _, repo := range got.Repositories {
			paths = append(paths, repo.Path)
		}
		if !reflect.DeepEqual(paths, tc.want) {
			t.Errorf("depth %d found %v, want %v", tc.depth, paths, tc.want)
		}
	}
}

func TestScanExplicitRootsRemainEligibleWhenIgnoredOrNested(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "outer")
	inner := filepath.Join(outer, "inner")
	ignoredRoot := filepath.Join(root, "vendor")
	makeRepo(t, outer, true)
	makeRepo(t, inner, true)
	makeRepo(t, ignoredRoot, false)

	for _, tc := range []struct {
		root string
		want string
	}{
		{root: ignoredRoot, want: ignoredRoot},
		{root: inner, want: inner},
	} {
		got, err := Scan(context.Background(), Options{Roots: []string{tc.root}, MaxDepth: 0, IgnoreDirs: []string{"vendor"}})
		if err != nil {
			t.Fatalf("root %q: %v", tc.root, err)
		}
		if len(got.Repositories) != 1 || got.Repositories[0].Path != tc.want {
			t.Errorf("root %q found %#v, want %q", tc.root, got.Repositories, tc.want)
		}
	}
}

func TestScanDoesNotAcceptSymlinkGitMarker(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(repoPath, ".git")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := Scan(context.Background(), Options{Roots: []string{root}, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 0 {
		t.Fatalf("symlink .git marker was accepted: %#v", got.Repositories)
	}
}

func TestScanRejectsNonDirectoryRootAndNegativeDepth(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Scan(context.Background(), Options{Roots: []string{file}, MaxDepth: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 0 || len(got.Warnings) != 1 || got.Warnings[0].Message != "root is not a directory" {
		t.Fatalf("non-directory root result = %#v", got)
	}
	if _, err := Scan(context.Background(), Options{Roots: []string{t.TempDir()}, MaxDepth: -1}); err == nil {
		t.Fatal("negative MaxDepth unexpectedly accepted")
	}
}

func TestScanWarnsForInaccessibleRootWhenPermissionsApply(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permission checks are not meaningful for this user/platform")
	}
	root := filepath.Join(t.TempDir(), "inaccessible")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	got, err := Scan(context.Background(), Options{Roots: []string{root}, MaxDepth: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Path != root {
		t.Fatalf("inaccessible root warnings = %#v", got.Warnings)
	}
}

func TestScanSortsDuplicateNamesAndDeduplicatesOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a", "same")
	b := filepath.Join(root, "b", "same")
	makeRepo(t, a, true)
	makeRepo(t, b, false)

	got, err := Scan(context.Background(), Options{Roots: []string{root, a}, MaxDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 2 {
		t.Fatalf("found %d repositories, want 2: %#v", len(got.Repositories), got.Repositories)
	}
	if got.Repositories[0].Name != "same" || got.Repositories[1].Name != "same" || got.Repositories[0].Path >= got.Repositories[1].Path {
		t.Fatalf("repositories are not sorted by name then path: %#v", got.Repositories)
	}
}

func TestScanSkipsDirectorySymlinksAndExplicitSymlinkRoots(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	target := filepath.Join(parent, "target")
	makeRepo(t, target, true)
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := Scan(context.Background(), Options{Roots: []string{root}, MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 0 {
		t.Fatalf("followed directory symlink: %#v", got.Repositories)
	}

	got, err = Scan(context.Background(), Options{Roots: []string{filepath.Join(root, "linked")}, MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 0 || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0].Message, "symlink") {
		t.Fatalf("explicit symlink root result = %#v", got)
	}
}

func TestScanReturnsPartialDiscoveriesAndWarningsForMissingRoot(t *testing.T) {
	base := t.TempDir()
	repoPath := filepath.Join(base, "repo")
	makeRepo(t, repoPath, true)

	got, err := Scan(context.Background(), Options{Roots: []string{filepath.Join(base, "missing"), base}, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repositories) != 1 || got.Repositories[0].Path != repoPath || len(got.Warnings) != 1 {
		t.Fatalf("result = %#v, want repo and one missing-root warning", got)
	}
	if got.Warnings[0].Path != filepath.Join(base, "missing") {
		t.Fatalf("warning path = %q", got.Warnings[0].Path)
	}
}

func TestScanHonorsCancellationAndReturnsPartialResults(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "repo")
	makeRepo(t, repoPath, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := Scan(ctx, Options{Roots: []string{root}, MaxDepth: 2})
	if err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(got.Repositories) != 0 {
		t.Fatalf("canceled scan unexpectedly traversed filesystem: %#v", got.Repositories)
	}
}

func makeRepo(t *testing.T, path string, gitDirectory bool) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, ".git")
	if gitDirectory {
		if err := os.Mkdir(marker, 0o755); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(marker, []byte("gitdir: ../.git/worktrees/example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
