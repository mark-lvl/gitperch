package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func disposable(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(d, "absent"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitCmd(t, d, "init", "-b", "main")
	gitCmd(t, d, "config", "user.name", "Test")
	gitCmd(t, d, "config", "user.email", "test@example.invalid")
	gitCmd(t, d, "config", "commit.gpgsign", "false")
	return d
}
func gitCmd(t *testing.T, path string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return out
}
func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func commit(t *testing.T, path string) {
	t.Helper()
	gitCmd(t, path, "add", ".")
	gitCmd(t, path, "commit", "-m", "test")
}

func TestRealStatusFixtures(t *testing.T) {
	for _, name := range []string{"unborn", "normal", "staged-unstaged", "renamed", "conflicted", "untracked", "detached", "dirty-submodule", "unusual-filenames"} {
		t.Run(name, func(t *testing.T) {
			d := disposable(t)
			if name != "unborn" {
				write(t, filepath.Join(d, "file"), "base\n")
				commit(t, d)
			}
			changes, untracked, conflicts := 0, 0, 0
			switch name {
			case "staged-unstaged":
				write(t, filepath.Join(d, "file"), "staged\n")
				gitCmd(t, d, "add", "file")
				write(t, filepath.Join(d, "file"), "unstaged\n")
				changes = 1
			case "renamed":
				gitCmd(t, d, "mv", "file", "new\t name\nλ")
				changes = 1
			case "untracked":
				write(t, filepath.Join(d, "new file"), "new")
				untracked = 1
			case "detached":
				gitCmd(t, d, "checkout", "--detach")
			case "conflicted":
				gitCmd(t, d, "checkout", "-b", "other")
				write(t, filepath.Join(d, "file"), "other\n")
				commit(t, d)
				gitCmd(t, d, "checkout", "main")
				write(t, filepath.Join(d, "file"), "main\n")
				commit(t, d)
				if err := exec.Command("git", "-C", d, "merge", "other").Run(); err == nil {
					t.Fatal("expected conflict")
				}
				changes, conflicts = 1, 1
			case "dirty-submodule":
				sub := disposable(t)
				write(t, filepath.Join(sub, "tracked"), "base")
				commit(t, sub)
				gitCmd(t, d, "-c", "protocol.file.allow=always", "submodule", "add", sub, "sub")
				commit(t, d)
				write(t, filepath.Join(d, "sub", "tracked"), "dirty")
				changes = 1
			case "unusual-filenames":
				for _, f := range []string{"space name", "tab\tname", "line\nname", "λ", "\x1b[31m"} {
					write(t, filepath.Join(d, f), "new")
				}
				untracked = 5
			}
			data := gitCmd(t, d, "--no-optional-locks", "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all", "--ignore-submodules=none")
			s, err := ParseStatus(data)
			if err != nil {
				t.Fatal(err)
			}
			if s.Changes != changes || s.Untracked != untracked || s.Conflicts != conflicts || s.Detached != (name == "detached") || s.Unborn != (name == "unborn") || s.Synchronized() {
				t.Fatalf("unexpected status: %+v", s)
			}
			actual := (Runner{}).Inspect(context.Background(), d)
			if actual.Error != "" || actual.Changes != changes || actual.Untracked != untracked || actual.Conflicts != conflicts {
				t.Fatalf("runner: %+v", actual)
			}
			fixture := filepath.Join("..", "..", "testdata", "git-status", name+".nul")
			if os.Getenv("REPODASH_UPDATE_FIXTURES") == "1" {
				if err := os.MkdirAll(filepath.Dir(fixture), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fixture, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			fs, err := ParseStatus(stored)
			if err != nil || fs.Changes != changes || fs.Untracked != untracked || fs.Conflicts != conflicts {
				t.Fatalf("fixture: %+v %v", fs, err)
			}
		})
	}
}

func TestLocallyKnownComparisonAndDeletedUpstream(t *testing.T) {
	d := disposable(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitCmd(t, filepath.Dir(remote), "init", "--bare", remote)
	write(t, filepath.Join(d, "file"), "base")
	commit(t, d)
	gitCmd(t, d, "remote", "add", "origin", remote)
	gitCmd(t, d, "push", "-u", "origin", "main")
	s := (Runner{}).Inspect(context.Background(), d)
	if !s.Synchronized() || s.Upstream != "origin/main" {
		t.Fatalf("%+v", s)
	}
	write(t, filepath.Join(d, "file"), "ahead")
	commit(t, d)
	s = (Runner{}).Inspect(context.Background(), d)
	if !s.ComparisonKnown || s.Ahead != 1 || s.Behind != 0 {
		t.Fatalf("%+v", s)
	}
	gitCmd(t, d, "update-ref", "-d", "refs/remotes/origin/main")
	s = (Runner{}).Inspect(context.Background(), d)
	if s.ComparisonKnown || s.Synchronized() || s.Error != "" {
		t.Fatalf("%+v", s)
	}
}

func TestMalformedRecords(t *testing.T) {
	head := "# branch.oid " + strings.Repeat("a", 40) + "\x00# branch.head main\x00"
	for _, data := range []string{"", head + "? \x00", head + "1 M. N...\x00", head + "# branch.ab +x -0\x00", head + "# branch.ab +1 -0\x00", head + "# branch.head main\x00", head + "bad\x00", strings.TrimSuffix(head, "\x00"), head + "2 R. N... 100644 100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("a", 40) + " R100 name\x00"} {
		if _, err := ParseStatus([]byte(data)); err == nil {
			t.Fatalf("accepted malformed %q", data)
		}
	}
	if _, err := ParseStatus([]byte(head + "# future whatever\x00")); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerIsolationAndLimits(t *testing.T) {
	d := disposable(t)
	t.Setenv("GIT_DIR", "/not-a-repo")
	t.Setenv("GIT_WORK_TREE", "/not-a-tree")
	t.Setenv("GIT_INDEX_FILE", "/not-an-index")
	if s := (Runner{}).Inspect(context.Background(), d); s.Error != "" || !s.Unborn {
		t.Fatalf("%+v", s)
	}
	if _, err := (Runner{OutputLimit: 4}).Run(context.Background(), d, "status", "--porcelain=v2", "--branch", "-z"); err == nil {
		t.Fatal("expected bounded output error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Runner{}).Run(ctx, d, "status"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A disposable Git alias exercises an actually running process deadline.
	start := time.Now()
	_, err := (Runner{Timeout: 30 * time.Millisecond}).Run(context.Background(), d, "-c", "alias.slow=!sleep 3", "slow")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(start))
	}
}

func TestDisplaySafety(t *testing.T) {
	s := SafeText("\x1b[31m\nhttps://user:secret@host/repo?token=secret&x=ok")
	if strings.Contains(s, "secret") || strings.ContainsAny(s, "\x1b\n") || !strings.Contains(s, "[redacted]") {
		t.Fatal(s)
	}
	for _, in := range []string{"https://user:p@ss@host/repo", "fetch https://user:p@ss@host/repo failed"} {
		if s := SafeText(in); strings.Contains(s, "ss@") || !strings.Contains(s, "[redacted]@host/repo") {
			t.Fatalf("%q -> %q", in, s)
		}
	}
	if s := SafeText("https://host/a@b"); s != "https://host/a@b" {
		t.Fatalf("path changed: %q", s)
	}
}

func TestFixtureNULs(t *testing.T) {
	b, err := os.ReadFile("../../testdata/git-status/renamed.nul")
	if err != nil || !bytes.Contains(b, []byte{0}) {
		t.Fatalf("not a real NUL fixture: %v", err)
	}
}
