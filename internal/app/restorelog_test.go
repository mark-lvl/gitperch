package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// deleteDone plans and runs the deletion of a merged branch "done".
func deleteDone(t *testing.T, actions *Actions) (Event, string, string) {
	t.Helper()
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done")
	oid := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "done")))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "done" {
			id = item.ID
		}
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{id}, nil)
	if err != nil || len(results) != 1 {
		t.Fatalf("results %+v, %v", results, err)
	}
	return results[0], oid, repo
}

func TestCleanupRecordsRestoreCommandsInTheLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "gitperch")
	log := filepath.Join(dir, "cleanup.log")
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetRestoreLog(log)
	result, oid, repo := deleteDone(t, actions)
	want := "git -C " + repo + " branch done " + oid
	if result.State != Succeeded || result.Restore != want || !result.RestoreLogged {
		t.Fatalf("result: %+v", result)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "\tdone\t"+oid+"\t"+want) {
		t.Fatalf("log: %q", data)
	}
	for path, mode := range map[string]os.FileMode{log: 0o600, dir: 0o700} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: mode %v, %v", path, info.Mode().Perm(), err)
		}
	}
}

func TestCleanupReportsUnwritableRestoreLogWithoutFailingTheDeletion(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetRestoreLog(filepath.Join(blocker, "cleanup.log"))
	result, oid, repo := deleteDone(t, actions)
	if result.State != Succeeded || result.Restore != "git -C "+repo+" branch done "+oid || result.RestoreLogged || !strings.Contains(result.Message, "restore log not written") {
		t.Fatalf("result: %+v", result)
	}
}

func TestDefaultRestoreLogPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	if got, err := DefaultRestoreLogPath(); err != nil || got != "/state/gitperch/cleanup.log" {
		t.Fatalf("XDG_STATE_HOME: %q, %v", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "relative/state") // ignored, as the XDG spec requires
	t.Setenv("HOME", "/home/someone")
	if got, err := DefaultRestoreLogPath(); err != nil || got != "/home/someone/.local/state/gitperch/cleanup.log" {
		t.Fatalf("HOME fallback: %q, %v", got, err)
	}
}
