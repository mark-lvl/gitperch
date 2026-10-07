package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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
	if lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "\t"+strconv.Quote(repo)+"\t\"done\"\t"+oid+"\t"+want) {
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

// A ZWNJ is routine in Persian, Arabic and Indic names, but SafeText escapes
// it, so a command built from escaped names would point elsewhere.
const zwnjRepo = "/home/m/می‌خواهم"

func TestRestoreCommandSaysWhenNamesAreShownEscaped(t *testing.T) {
	oid := strings.Repeat("a", 40)
	plain := restoreAnywhere(CleanupItem{Group: "/home/m/repo", Branch: "done", OID: oid})
	if plain != "git -C /home/m/repo branch done "+oid {
		t.Fatalf("plain: %q", plain)
	}
	escaped := restoreAnywhere(CleanupItem{Group: zwnjRepo, Branch: "done", OID: oid})
	if !strings.Contains(escaped, "shown escaped") || !strings.Contains(escaped, "git branch <name> "+oid) {
		t.Fatalf("escaped: %q", escaped)
	}
}

func TestRestoreLogKeepsExactNames(t *testing.T) {
	log := filepath.Join(t.TempDir(), "cleanup.log")
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetRestoreLog(log)
	item := CleanupItem{Group: zwnjRepo, Branch: "feat/a\tb", OID: strings.Repeat("b", 40)}
	if logged, err := actions.recordRestore(item, restoreAnywhere(item)); !logged || err != nil {
		t.Fatalf("logged %v, %v", logged, err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(strings.TrimSuffix(string(data), "\n"), "\t")
	if len(fields) != 5 {
		t.Fatalf("a name split the line: %q", data)
	}
	for i, want := range map[int]string{1: item.Group, 2: item.Branch} {
		if got, err := strconv.Unquote(fields[i]); err != nil || got != want {
			t.Fatalf("column %d = %q (%q, %v), want %q", i, fields[i], got, err, want)
		}
	}
}

func TestRestoreLogIsPrivateEvenWhenItExisted(t *testing.T) {
	log := filepath.Join(t.TempDir(), "cleanup.log")
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetRestoreLog(log)
	if _, err := actions.recordRestore(CleanupItem{Group: "/r", Branch: "b", OID: strings.Repeat("c", 40)}, "git -C /r branch b x"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(log); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", info.Mode().Perm(), err)
	}
}
