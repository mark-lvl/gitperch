package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oidA = "d4d4e4095f071aea427e7ce86dc1c11eff4fd181"

func TestParseWorktrees(t *testing.T) {
	data := strings.Join([]string{
		"worktree /src/repo", "HEAD " + oidA, "branch refs/heads/main", "",
		"worktree /src/wt one\nline", "HEAD " + oidA, "detached", "locked agent session", "",
		"worktree /src/gone", "HEAD " + oidA, "branch refs/heads/feat/x", "prunable gitdir file points to non-existent location", "",
		"worktree /src/plain-lock", "HEAD " + oidA, "branch refs/heads/y", "locked", "future-attribute value", "",
	}, "\x00") + "\x00"
	got, err := ParseWorktrees([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("records: %+v", got)
	}
	if !got[0].Main || got[0].Branch != "main" || got[0].HeadOID != oidA || got[1].Main {
		t.Fatalf("main record: %+v / %+v", got[0], got[1])
	}
	if got[1].Path != "/src/wt one\nline" || !got[1].Detached || !got[1].Locked || got[1].LockReason != "agent session" {
		t.Fatalf("locked detached record: %+v", got[1])
	}
	if !got[2].Prunable || got[2].PrunableReason == "" || got[2].Branch != "feat/x" {
		t.Fatalf("prunable record: %+v", got[2])
	}
	if !got[3].Locked || got[3].LockReason != "" {
		t.Fatalf("reasonless lock: %+v", got[3])
	}
}

func TestParseWorktreesBare(t *testing.T) {
	got, err := ParseWorktrees([]byte("worktree /src/repo.git\x00bare\x00\x00worktree /src/wt\x00HEAD " + oidA + "\x00branch refs/heads/x\x00\x00"))
	if err != nil || len(got) != 2 || !got[0].Bare || !got[0].Main || got[1].Branch != "x" {
		t.Fatalf("bare: %+v, %v", got, err)
	}
}

func TestParseWorktreesRejectsMalformed(t *testing.T) {
	for name, data := range map[string]string{
		"empty":          "",
		"relative path":  "worktree repo\x00\x00",
		"orphan field":   "HEAD " + oidA + "\x00\x00",
		"bad oid":        "worktree /r\x00HEAD xyz\x00\x00",
		"non-head ref":   "worktree /r\x00branch refs/tags/v1\x00\x00",
		"unterminated":   "worktree /r\x00HEAD " + oidA + "\x00",
		"nested records": "worktree /r\x00worktree /s\x00\x00",
	} {
		if _, err := ParseWorktrees([]byte(data)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRunnerWorktreesListsLinkedAndStale(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "f"), "x\n")
	commit(t, d)
	base := t.TempDir()
	linked, stale := filepath.Join(base, "linked"), filepath.Join(base, "stale")
	gitCmd(t, d, "worktree", "add", "-b", "linked", linked)
	gitCmd(t, d, "worktree", "add", "-b", "stale", stale)
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}
	got, err := (Runner{}).Worktrees(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !got[0].Main || got[1].Branch != "linked" || !got[2].Prunable {
		t.Fatalf("worktrees: %+v", got)
	}
}

func TestParseGitVersion(t *testing.T) {
	for out, want := range map[string][2]int{
		"git version 2.43.0\n":                {2, 43},
		"git version 2.39.3 (Apple Git-146)\n": {2, 39},
		"git version 2.45.1.windows.1\n":      {2, 45},
	} {
		major, minor, ok := parseGitVersion(out)
		if !ok || major != want[0] || minor != want[1] {
			t.Errorf("%q: %d.%d %v", out, major, minor, ok)
		}
	}
	for _, out := range []string{"", "hub version 2.14\n", "git version x.y\n"} {
		if _, _, ok := parseGitVersion(out); ok {
			t.Errorf("%q parsed", out)
		}
	}
}

// fakeGit writes an executable that prints a version and fails otherwise.
func fakeGit(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = version ] && { echo 'git version " + version + "'; exit 0; }; done\nexit 129\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSupportsWorktreeInventory(t *testing.T) {
	ctx := context.Background()
	if (Runner{Executable: fakeGit(t, "2.35.8")}).SupportsWorktreeInventory(ctx) {
		t.Fatal("Git 2.35 should not support the inventory")
	}
	if !(Runner{Executable: fakeGit(t, "2.36.0")}).SupportsWorktreeInventory(ctx) {
		t.Fatal("Git 2.36 should support the inventory")
	}
	if (Runner{Executable: filepath.Join(t.TempDir(), "missing-git")}).SupportsWorktreeInventory(ctx) {
		t.Fatal("an unreadable version must disable the feature")
	}
}
