package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/config"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// githubWorkspace is a root holding one repository on branch feat/x whose
// upstream is origin/feat/x on github.com. Nothing is fetched or pushed: the
// remote is configured and its tracking ref is written directly.
func githubWorkspace(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	repo := filepath.Join(root, "widgets")
	for _, args := range [][]string{
		{"init", "-q", "-b", "feat/x", repo},
		{"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "--allow-empty", "-m", "work"},
		{"-C", repo, "remote", "add", "origin", "https://github.com/acme/widgets.git"},
		{"-C", repo, "config", "branch.feat/x.remote", "origin"},
		{"-C", repo, "config", "branch.feat/x.merge", "refs/heads/feat/x"},
		{"-C", repo, "update-ref", "refs/remotes/origin/feat/x", "HEAD"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return root
}

// fakeGHOnPath puts a gh first on PATH that prints response.
func fakeGHOnPath(t *testing.T, response string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(response), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat '" + filepath.Join(dir, "response.json") + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const widgetsResponse = `{"data":{"repository":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[` +
	`{"number":42,"title":"Add tokens","url":"https://github.com/acme/widgets/pull/42","state":"OPEN","isDraft":false,"baseRefName":"main",` +
	`"headRefName":"feat/x","headRefOid":"1111111111111111111111111111111111111111","headRepository":{"nameWithOwner":"acme/widgets"},` +
	`"baseRepository":{"nameWithOwner":"acme/widgets"},"mergedAt":null,"updatedAt":"2026-10-09T10:00:00Z","reviewDecision":null,` +
	`"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}}]},"parent":null}}}`

func TestStatusGitHubAddsPullRequests(t *testing.T) {
	root := githubWorkspace(t)
	fakeGHOnPath(t, widgetsResponse)
	var out, errOut bytes.Buffer
	if code := run([]string{"status", "--json", "--github", root}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, &errOut)
	}
	var report app.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	row := report.Repositories[0]
	if row.GitHub == nil || row.GitHub.Repository != "acme/widgets" || len(row.GitHub.PullRequests) != 1 || row.GitHub.PullRequests[0].Checks != app.ChecksFailing {
		t.Fatalf("github: %+v", row.GitHub)
	}
	if !strings.Contains(strings.Join(reasonNames(row.Attention.Reasons), ","), "pr_checks_failing") {
		t.Fatalf("attention: %+v", row.Attention)
	}
	out.Reset()
	if code := run([]string{"status", "--github", root}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "pr #42 open, checks failing") {
		t.Fatalf("table: exit %d\n%s", code, &out)
	}
	out.Reset()
	if code := run([]string{"status", "--json", root}, &out, &errOut); code != 0 || strings.Contains(out.String(), `"github"`) {
		t.Fatalf("status without --github looked up GitHub: exit %d\n%s", code, &out)
	}
}

func reasonNames(reasons []app.Reason) []string {
	var names []string
	for _, r := range reasons {
		names = append(names, string(r))
	}
	return names
}

func TestStatusGitHubFailedLookupKeepsExitCode(t *testing.T) {
	root := githubWorkspace(t)
	fakeGHOnPath(t, `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve"}]}`)
	var out, errOut bytes.Buffer
	if code := run([]string{"status", "--json", "--github", root}, &out, &errOut); code != 0 || !strings.Contains(errOut.String(), "1 pull request lookup(s) failed") {
		t.Fatalf("exit %d, stderr %q", code, &errOut)
	}
	if !strings.Contains(out.String(), "not found or not accessible") {
		t.Fatalf("report lacks the error:\n%s", &out)
	}
}

func TestStatusGitHubRefusesWhenUnavailable(t *testing.T) {
	root := githubWorkspace(t)
	var out, errOut bytes.Buffer
	if code := run([]string{"--github", root}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "--github requires status") {
		t.Fatalf("dashboard: exit %d, %s", code, &errOut)
	}
	disabled := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(disabled, []byte("[github]\nenabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := run([]string{"status", "--github", "--config", disabled, root}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "disabled") {
		t.Fatalf("disabled: exit %d, %s", code, &errOut)
	}
	// A PATH holding only git: gh is missing even on machines that have it.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	errOut.Reset()
	if code := run([]string{"status", "--github", root}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "needs the gh CLI") {
		t.Fatalf("missing gh: exit %d, %s", code, &errOut)
	}
}

func TestNewGitHubNeedsGit236(t *testing.T) {
	old := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(old, []byte("#!/bin/sh\necho 'git version 2.35.8'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{GitHub: config.GitHub{Enabled: true}}
	if _, _, err := newGitHub(context.Background(), cfg, gitcli.Runner{Executable: old}); err == nil || !strings.Contains(err.Error(), "Git 2.36") {
		t.Fatalf("old Git: %v", err)
	}
}
