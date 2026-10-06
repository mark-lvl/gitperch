package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// loggingGit returns a Git executable that records each invocation's
// arguments, one line per call, in the returned log file.
func loggingGit(t *testing.T) (executable, log string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	executable = filepath.Join(dir, "git")
	write(t, executable, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\nexec '"+real+"' \"$@\"\n")
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatal(err)
	}
	return executable, log
}

func TestInspectResolvesGitDirectoriesWithOneProcess(t *testing.T) {
	for _, linked := range []bool{false, true} {
		d := disposable(t)
		write(t, filepath.Join(d, "file"), "base\n")
		commit(t, d)
		target, gitDir := d, filepath.Join(d, ".git")
		if linked {
			target = filepath.Join(t.TempDir(), "linked")
			gitCmd(t, d, "worktree", "add", "-b", "linked", target)
			gitDir = filepath.Join(d, ".git", "worktrees", "linked")
		}
		write(t, filepath.Join(gitDir, "MERGE_HEAD"), strings.Repeat("a", 40)+"\n")
		executable, log := loggingGit(t)
		s := (Runner{Executable: executable}).Inspect(context.Background(), target)
		common, _ := filepath.EvalSymlinks(filepath.Join(d, ".git"))
		if s.Error != "" || s.CommonDir != common || s.Operation != "MERGE_HEAD" || s.LastActivity.IsZero() {
			t.Fatalf("linked=%v: %+v", linked, s)
		}
		calls, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(calls), " rev-parse "); n != 1 {
			t.Fatalf("linked=%v: %d rev-parse processes per inspection, want 1:\n%s", linked, n, calls)
		}
	}
}

func TestInspectResolvesGitDirectoriesWithNewlinesInPath(t *testing.T) {
	d := filepath.Join(t.TempDir(), "line\nbreak")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitCmd(t, filepath.Dir(d), "init", "-b", "main", d)
	s := (Runner{}).Inspect(context.Background(), d)
	common, _ := filepath.EvalSymlinks(filepath.Join(d, ".git"))
	if s.Error != "" || s.CommonDir != common {
		t.Fatalf("%+v", s)
	}
}
