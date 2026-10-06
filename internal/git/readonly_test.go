package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectReadOnlyWithDirtyTrackedFile(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "tracked"), "committed\n")
	commit(t, d)
	write(t, filepath.Join(d, "tracked"), "working tree edit\n")

	index := filepath.Join(d, ".git", "index")
	beforeBytes, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}

	status := (Runner{}).Inspect(context.Background(), d)
	if status.Error != "" || status.Changes != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	afterBytes, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatal("Inspect changed the Git index contents")
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("Inspect changed the Git index modification time: before %s, after %s", beforeInfo.ModTime(), afterInfo.ModTime())
	}
}

func TestInspectDeletedTrackingRefDoesNotContactRemote(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "tracked"), "committed\n")
	commit(t, d)
	missingRemote := filepath.Join(t.TempDir(), "remote-does-not-exist.git")
	gitCmd(t, d, "remote", "add", "origin", missingRemote)
	gitCmd(t, d, "update-ref", "refs/remotes/origin/main", strings.TrimSpace(string(gitCmd(t, d, "rev-parse", "HEAD"))))
	gitCmd(t, d, "branch", "--set-upstream-to=origin/main", "main")
	gitCmd(t, d, "update-ref", "-d", "refs/remotes/origin/main")

	status := (Runner{}).Inspect(context.Background(), d)
	if status.Error != "" || status.Upstream != "origin/main" || status.ComparisonKnown || status.Synchronized() {
		t.Fatalf("missing local tracking ref should remain unknown without contacting remote: %+v", status)
	}
}

func TestInspectReportsInvalidGitCandidates(t *testing.T) {
	t.Run("malformed git file", func(t *testing.T) {
		d := t.TempDir()
		write(t, filepath.Join(d, ".git"), "this is not a gitdir file\n")
		status := (Runner{}).Inspect(context.Background(), d)
		if status.Error == "" {
			t.Fatalf("malformed .git file was treated as a valid clean repository: %+v", status)
		}
	})

	t.Run("empty git directory", func(t *testing.T) {
		d := t.TempDir()
		if err := os.Mkdir(filepath.Join(d, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		status := (Runner{}).Inspect(context.Background(), d)
		if status.Error == "" {
			t.Fatalf("empty .git directory was treated as a valid clean repository: %+v", status)
		}
	})
}

func TestInvalidNestedGitCandidateDoesNotInheritParentStatus(t *testing.T) {
	parent := disposable(t)
	write(t, filepath.Join(parent, "tracked"), "clean\n")
	commit(t, parent)
	nested := filepath.Join(parent, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(nested, ".git"), "malformed gitdir\n")

	parentStatus := (Runner{}).Inspect(context.Background(), parent)
	nestedStatus := (Runner{}).Inspect(context.Background(), nested)
	if parentStatus.Error != "" || parentStatus.Changes != 0 || parentStatus.Untracked != 0 {
		t.Fatalf("parent should remain clean: %+v", parentStatus)
	}
	if nestedStatus.Error == "" {
		t.Fatalf("nested invalid candidate inherited the parent status: %+v", nestedStatus)
	}
}

func TestInspectFiltersInheritedGitConfigRouting(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "tracked"), "committed\n")
	commit(t, d)
	secretRoutingTarget := filepath.Join(t.TempDir(), "private-routing-target")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", secretRoutingTarget)

	status := (Runner{}).Inspect(context.Background(), d)
	if strings.Contains(status.Error, secretRoutingTarget) {
		t.Fatal("inspection error exposed the inherited config value")
	}
	if status.Error != "" || status.Unborn || status.Changes != 0 {
		t.Fatalf("inherited config redirected Git inspection: error=%q", status.Error)
	}
}

func TestRepositoryConfigCannotRunCommandsDuringInspection(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "tracked"), "committed\n")
	commit(t, d)
	write(t, filepath.Join(d, "tracked"), "edit\n")
	marker := filepath.Join(t.TempDir(), "executed")
	script := filepath.Join(t.TempDir(), "hook.sh")
	write(t, script, "#!/bin/sh\ntouch '"+marker+"'\nexit 1\n")
	if err := os.Chmod(script, 0700); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, d, "config", "core.fsmonitor", script)
	gitCmd(t, d, "config", "log.showSignature", "true")
	gitCmd(t, d, "config", "gpg.program", script)

	r := Runner{}
	if status := r.Inspect(context.Background(), d); status.Error != "" {
		t.Fatalf("inspect: %s", status.Error)
	}
	if _, err := r.Details(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Patch(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("repository configuration executed a command")
	}
}

func TestMetadataRejectsRemoteNamesThatBreakConfigOverrides(t *testing.T) {
	d := disposable(t)
	gitCmd(t, d, "remote", "add", "a=b", t.TempDir())
	if _, err := (Runner{}).Metadata(context.Background(), d); err == nil || !strings.Contains(err.Error(), "unsupported remote name") {
		t.Fatalf("got %v", err)
	}
}

func TestWorktreesReadOnly(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "tracked"), "committed\n")
	commit(t, d)
	linked := filepath.Join(t.TempDir(), "linked")
	gitCmd(t, d, "worktree", "add", "-b", "linked", linked)
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	admin := filepath.Join(d, ".git", "worktrees")
	before, err := os.ReadDir(admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Runner{}).Worktrees(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(admin)
	if err != nil || len(after) != len(before) {
		t.Fatalf("listing worktrees pruned administrative files: before %d, after %d, %v", len(before), len(after), err)
	}
}
