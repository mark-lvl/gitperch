# GitHub CLI Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the GitHub CLI (`gh`) is installed and logged in, show each branch's pull request (state, review, CI checks) in the dashboard and in `gitperch status --github`, accept a pull request merged with exactly the local commit as proof of merge in the worktree lifecycle and in Clean up, and open pull requests in the browser.

**Architecture:** A new `internal/github` package is the only code that runs gh: a hardened runner, remote URL parsing, a fixed GraphQL query per repository (`gh api graphql`, names only in `-f` fields, run from the temp directory) and `gh browse`. `internal/app` maps each branch to its upstream's GitHub repository through the Git runner and caches lookups in a `GitHub` service shared by the TUI, `status --github` and Clean up; attention, lifecycle and cleanup evidence derive from it. The TUI only annotates rows and dispatches lookups.

**Tech Stack:** Go 1.27, Bubble Tea v2, Lip Gloss v2, Git 2.36 or newer, GitHub CLI (`gh api` and `gh browse`; development used gh 2.45).

**Spec:** `docs/superpowers/specs/2026-10-10-github-cli-integration-design.md`

## How this plan was checked

Every code block below was applied to a copy of this repository at commit `055738d`, phase by phase. After each phase the affected packages' tests passed, and after phase 4 `make check` (fmt-check, test, race, vet, build) passed on Linux with Go 1.27.1 and Git 2.43. New files are given in full. Edits to existing files are unified diffs against the state the earlier tasks leave; apply them exactly (the Edit tool with the `-` and `+` lines, or `git apply` from a file). Documentation edits are exact replacements, checked to apply in order. If a diff does not apply, the file differs from what this plan expects: stop and compare instead of improvising.

## Environment

- If `go` is not on `PATH` (as in the maintainer's WSL setup): `export PATH=/tmp/gitperch-toolchain/go/bin:$PATH GOPATH=/tmp/gitperch-gopath GOCACHE=/tmp/gitperch-gocache GOTOOLCHAIN=local`.
- PNG render captures need `pillow` and `pyte`, which are not application dependencies: `python3 -m venv /tmp/capture-venv && /tmp/capture-venv/bin/pip install pillow pyte`, then run `scripts/render-captures.py` with that interpreter.
- The full suite takes about two minutes; run single packages while iterating and `make check` at the end of each task.

## Global Constraints

- gh runs only through `internal/github`'s `Runner`: argument arrays, a deadline (the status timeout, default 15 s), closed stdin, a 4 MiB capture per stream, working directory `os.TempDir()`, and the hardened environment from Task 1.
- Owner, repository and branch names travel only as `-f key=value` fields or in `--flag=value` form. `-F` is never used. GraphQL query text never contains a name.
- gitperch only reads from GitHub. The only gh commands are `gh api graphql` (a query) and `gh browse`. It never reads, stores or prints a token.
- The integration runs only when `[github] enabled` is true (the default), Git is 2.36 or newer (`SupportsWorktreeInventory`) and gh is on `PATH`. Otherwise the TUI has no GitHub feature at all and `gitperch status --github` exits 2 naming the cause.
- Without `--github`, `gitperch status` output is unchanged and gh never runs.
- Cadence: a lookup result is reused for 5 minutes (`GitHubTTL`); forced rechecks replace results older than 30 seconds (`GitHubMinGap`); at most two gh processes run at once. Automatic refresh never forces.
- GitHub errors never raise attention, never add workspace warnings and never change an exit code.
- Merge evidence: a pull request that is merged, into its repository's default branch, whose head repository and branch are the branch's upstream and whose head commit is exactly the local commit, while no pull request of the branch is open. Clean up asks GitHub fresh and lets evidence replace only "not merged" and "upstream gone"; every other check still applies.
- JSON schema version stays 1; new fields are additive.
- Text from GitHub (titles, URLs, branch names, errors) passes through `gitcli.SafeText` before display.
- Tests never run the real gh or contact GitHub: CI runners have gh installed. Use fake gh executables or in-memory fakes. Tests touching Git use temporary repositories and local bare remotes.
- After TUI changes, update render captures per `docs/ui.md#render-captures-and-validation`.
- Each phase adds its `Unreleased` CHANGELOG entry. Commits use Conventional Commit messages ending with the attribution trailer your harness requires.
- `make check` passes at the end of every task.

## Review Focus

These failure modes are what a person using gitperch is most likely to meet. Each is pinned by a test in the task named.

1. **Branch names that look like gh syntax** (`@etc/passwd`, `{owner}`, a leading `-`): passed verbatim, never read as a file, placeholder or flag. Task 3 (`TestPullRequestsKeepsOnlyThisRepositorysBranch`), Task 15 (`TestBrowseCommand`).
2. **The same branch name in someone else's fork, or a deleted head repository**: never matched. Task 3.
3. **A fork of a private repository**: GitHub answers for the fork, errors for the parent, and gh exits non-zero; the fork's pull requests still show. Task 3 (`TestPullRequestsKeepsDataDespiteAnInaccessibleParent`).
4. **Small terminals**: the preview's pull request line must not push out the only changed-file row. Task 9 (`TestPreviewShowsPullRequestLine`, card heights 6 and 7).
5. **A squash-merged branch that moves after review**: skipped with "moved since review" and kept; the compare-and-delete pins the commit. Task 13 (`TestCleanupSkipsSquashMergedBranchThatMovedAfterReview`).

---

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/github/runner.go` (new) | `Runner`: hardened gh execution, `Available`, error classification |
| `internal/github/remote.go` (new) | `Repo`, `ParseRemote`: GitHub repositories in remote URLs |
| `internal/github/pulls.go` (new) | `Client.PullRequests`: the GraphQL query and its parsing |
| `internal/github/browse.go` (new) | `Runner.BrowseCommand` for `gh browse` |
| `internal/git/cleanup.go`, `metadata.go`, `service.go` | `Branch.Remote`/`RemoteRef`, `RemoteURL` |
| `internal/config/config.go` | `[github]` section |
| `internal/app/github.go` (new) | `GitHubInfo`, `PullRequest`, the `GitHub` service, merge verdicts, `MergeEvidence`, `BrowseTarget` |
| `internal/app/inspect.go` | `Row.GitHub` |
| `internal/app/attention.go`, `lifecycle.go` | `pr_*` reasons, `merged_pull_request` signal |
| `internal/app/format.go` | Table markers and footer for `--github` |
| `internal/app/assessment.go`, `cleanup.go`, `actions.go` | Cleanup codes, evidence in planning and revalidation, restore fallback, `SetGitHub` |
| `cmd/gitperch/github.go` (new) | `newGitHub`: the gate and construction shared by `status` and the TUI |
| `cmd/gitperch/main.go`, `tui.go` | `--github`; wiring |
| `internal/tui/github.go` (new) | Lookup dispatch, pull request rendering, `b` |
| `internal/tui/model.go`, `view.go`, `preview.go`, `detail.go`, `palette.go`, `actions.go` | Hooks for lookups, labels, preview line, details section, palette, hints |
| docs, captures, CHANGELOG | Per phase |

---

# Phase 1 — Foundation and read-only pull request status

### Task 1: Hardened gh runner

**Files:**
- Create: `internal/github/runner.go`
- Create: `internal/github/tmpdir_test.go`
- Test: `internal/github/runner_test.go`

**Interfaces:**
- Consumes: `gitcli.ChildEnvironment`, `gitcli.SafeText` (`internal/git`).
- Produces:
  ```go
  const DefaultOutputLimit = 4 << 20
  var ErrNotInstalled, ErrNotAuthenticated, ErrOutputLimit error
  type Runner struct { Executable string; Timeout time.Duration; OutputLimit int }
  type Output struct{ Stdout, Stderr []byte }
  func (r Runner) Available() bool
  func (r Runner) Run(ctx context.Context, args ...string) (Output, error)
  func (r Runner) executable() (string, error)   // LookPath of Executable, default "gh"
  func environment(env []string) []string        // the hardened environment
  // test helpers: fakeGH(t, stdout, code) (executable, dir string), recorded(t, dir, name), recordedArgs(t, dir)
  ```

- [ ] **Step 1: Write the failing tests**

`internal/github/tmpdir_test.go`:
```go
package github

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain canonicalises TMPDIR so t.TempDir() paths match what Git reports.
// On macOS the default /var/folders/... is behind the /var -> /private/var
// symlink, which discovery rejects as a symlink ancestor.
func TestMain(m *testing.M) {
	if dir, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		os.Setenv("TMPDIR", dir)
	}
	os.Exit(m.Run())
}
```

`internal/github/runner_test.go`:
```go
package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeGH writes a gh executable that records its arguments (NUL-separated),
// working directory and environment in dir, prints stdout and exits with
// code. Tests never run the real gh: CI runners have one installed.
func fakeGH(t *testing.T, stdout string, code int) (executable, dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stdout"), []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	executable = filepath.Join(dir, "gh")
	script := "#!/bin/sh\n" +
		"printf '%s\\0' \"$@\" > '" + dir + "/args'\n" +
		"pwd -P > '" + dir + "/pwd'\n" +
		"env > '" + dir + "/env'\n" +
		"cat '" + dir + "/stdout'\n" +
		"echo 'first problem' >&2\n" +
		"exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable, dir
}

// recorded reads what fakeGH saved under name; args are split at NUL.
func recorded(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func recordedArgs(t *testing.T, dir string) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(recorded(t, dir, "args"), "\x00"), "\x00")
}

func TestRunUsesNeutralDirectoryAndHardenedEnvironment(t *testing.T) {
	gh, dir := fakeGH(t, "ok\n", 0)
	t.Setenv("GH_FORCE_TTY", "1")
	t.Setenv("GH_REPO", "someone/else")
	t.Setenv("GH_DEBUG", "api")
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	t.Setenv("GH_TOKEN", "kept-token")
	t.Chdir(t.TempDir())
	out, err := (Runner{Executable: gh}).Run(context.Background(), "api", "graphql")
	if err != nil || string(out.Stdout) != "ok\n" {
		t.Fatalf("out %q, %v", out.Stdout, err)
	}
	if got := recordedArgs(t, dir); strings.Join(got, " ") != "api graphql" {
		t.Fatalf("args %q", got)
	}
	want, _ := filepath.EvalSymlinks(os.TempDir())
	if got := strings.TrimSpace(recorded(t, dir, "pwd")); got != want {
		t.Fatalf("gh ran in %q, want %q", got, want)
	}
	env := "\n" + recorded(t, dir, "env")
	for _, absent := range []string{"GH_FORCE_TTY=", "GH_REPO=", "GH_DEBUG=", "GIT_DIR="} {
		if strings.Contains(env, "\n"+absent) {
			t.Errorf("environment kept %s", absent)
		}
	}
	for _, present := range []string{"GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "CLICOLOR=0", "GH_TOKEN=kept-token"} {
		if !strings.Contains(env, "\n"+present+"\n") {
			t.Errorf("environment lacks %s", present)
		}
	}
}

func TestRunClassifiesFailures(t *testing.T) {
	gh, _ := fakeGH(t, "", 4)
	if _, err := (Runner{Executable: gh}).Run(context.Background(), "api"); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("exit 4: %v", err)
	}
	gh, _ = fakeGH(t, "", 1)
	if _, err := (Runner{Executable: gh}).Run(context.Background(), "api"); err == nil || !strings.Contains(err.Error(), "gh api: exit status 1: first problem") {
		t.Fatalf("exit 1: %v", err)
	}
	gh, _ = fakeGH(t, strings.Repeat("x", 64), 0)
	if _, err := (Runner{Executable: gh, OutputLimit: 16}).Run(context.Background(), "api"); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("limit: %v", err)
	}
	slow := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nexec sleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := (Runner{Executable: slow, Timeout: 50 * time.Millisecond}).Run(context.Background(), "api"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	missing := Runner{Executable: filepath.Join(t.TempDir(), "missing-gh")}
	if _, err := missing.Run(context.Background(), "api"); !errors.Is(err, ErrNotInstalled) || missing.Available() {
		t.Fatalf("missing gh: %v", err)
	}
	if !(Runner{Executable: gh}).Available() {
		t.Fatal("fake gh not available")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/github -run TestRun -v`
Expected: the build fails with `undefined: Runner`.

- [ ] **Step 3: Implement the runner**

`internal/github/runner.go`:
```go
// Package github runs the GitHub CLI (gh) for read-only pull request lookups
// and for opening pages in the browser. It is the only package that starts gh.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// DefaultOutputLimit caps each of gh's output streams.
const DefaultOutputLimit = 4 << 20

var (
	// ErrNotInstalled reports that no gh executable was found.
	ErrNotInstalled = errors.New("gh is not installed")
	// ErrNotAuthenticated reports gh's exit status 4, "authentication required".
	ErrNotAuthenticated = errors.New("gh is not logged in")
	// ErrOutputLimit reports output beyond the capture limit.
	ErrOutputLimit = errors.New("gh output exceeded capture limit")
)

// Runner starts gh with argument arrays, a deadline, closed input, bounded
// output, a neutral working directory and a hardened environment.
type Runner struct {
	Executable  string        // "" finds gh on PATH
	Timeout     time.Duration // 0 means 15 seconds
	OutputLimit int           // 0 means DefaultOutputLimit
}

type Output struct{ Stdout, Stderr []byte }

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil // Drain pipes even after reaching the capture limit.
}

func (r Runner) executable() (string, error) {
	name := r.Executable
	if name == "" {
		name = "gh"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNotInstalled
	}
	return path, nil
}

// Available reports whether gh can be started.
func (r Runner) Available() bool {
	_, err := r.executable()
	return err == nil
}

// Run starts gh outside any repository, so gh never reads a scanned
// repository's Git configuration; callers name the repository explicitly.
func (r Runner) Run(ctx context.Context, args ...string) (Output, error) {
	if len(args) == 0 {
		return Output{}, errors.New("gh command is required")
	}
	bin, err := r.executable()
	if err != nil {
		return Output{}, err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = environment(os.Environ())
	cmd.WaitDelay = time.Second
	limit := r.OutputLimit
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	stdout, stderr := &limitedBuffer{limit: limit}, &limitedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	out := Output{Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return out, ErrOutputLimit
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 4 {
		return out, ErrNotAuthenticated
	}
	if err != nil {
		return out, fmt.Errorf("gh %s: %w%s", args[0], err, firstLine(out.Stderr))
	}
	return out, nil
}

// firstLine is ": " and the first non-empty line of stderr, made safe for
// display, or "" when stderr is empty.
func firstLine(stderr []byte) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return ": " + gitcli.SafeText(line)
		}
	}
	return ""
}

// blockedEnvironment changes what gh prints, where it pages, which repository
// it targets or whether it prompts; environment sets its own values instead.
var blockedEnvironment = map[string]bool{
	"GH_FORCE_TTY": true, "CLICOLOR_FORCE": true, "GH_DEBUG": true, "DEBUG": true,
	"GH_REPO": true, "GH_HOST": true, "GH_PAGER": true, "PAGER": true,
	"GH_PROMPT_DISABLED": true, "GH_NO_UPDATE_NOTIFIER": true, "GH_NO_EXTENSION_UPDATE_NOTIFIER": true,
	"GH_SPINNER_DISABLED": true, "NO_COLOR": true, "CLICOLOR": true,
}

// environment keeps gh's credentials, configuration directory, proxies and
// browser while removing Git routing and anything that would make output
// interactive, colored or debug-annotated.
func environment(env []string) []string {
	env = gitcli.ChildEnvironment(env)
	result := make([]string, 0, len(env)+6)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !blockedEnvironment[key] {
			result = append(result, item)
		}
	}
	return append(result, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1", "GH_SPINNER_DISABLED=1", "NO_COLOR=1", "CLICOLOR=0")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/github -run TestRun -v`
Expected: PASS for `TestRunUsesNeutralDirectoryAndHardenedEnvironment` and `TestRunClassifiesFailures`.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/github
git commit -m "feat(github): add a hardened gh runner"
```

---

### Task 2: Recognize GitHub remotes

**Files:**
- Create: `internal/github/remote.go`
- Test: `internal/github/remote_test.go`

**Interfaces:**
- Produces:
  ```go
  type Repo struct{ Host, Owner, Name string }
  func (r Repo) String() string   // "github.com/acme/widgets", for --repo
  func (r Repo) FullName() string // "acme/widgets"
  func ParseRemote(raw string, hosts []string) (Repo, bool)
  ```

- [ ] **Step 1: Write the failing test**

`internal/github/remote_test.go`:
```go
package github

import "testing"

func TestParseRemote(t *testing.T) {
	enterprise := []string{"ghe.example.com"}
	for raw, want := range map[string]Repo{
		"https://github.com/acme/widgets.git":           {"github.com", "acme", "widgets"},
		"https://github.com/acme/widgets":               {"github.com", "acme", "widgets"},
		"https://github.com/acme/widgets/":              {"github.com", "acme", "widgets"},
		"https://token@github.com/acme/widgets.git":     {"github.com", "acme", "widgets"},
		"http://www.github.com/acme/widgets":            {"github.com", "acme", "widgets"},
		"ssh://git@github.com/acme/widgets.git":         {"github.com", "acme", "widgets"},
		"ssh://git@ssh.github.com:443/acme/widgets.git": {"github.com", "acme", "widgets"},
		"git://github.com/acme/widgets.git":             {"github.com", "acme", "widgets"},
		"git@github.com:acme/widgets.git":               {"github.com", "acme", "widgets"},
		"git@GitHub.com:Acme/my.repo_v2-x":              {"github.com", "Acme", "my.repo_v2-x"},
		"https://ghe.example.com/team/service.git":      {"ghe.example.com", "team", "service"},
		"git@ghe.example.com:team/service.git":          {"ghe.example.com", "team", "service"},
		"https://ghe.example.com:8443/team/service.git": {"ghe.example.com", "team", "service"},
	} {
		got, ok := ParseRemote(raw, enterprise)
		if !ok || got != want {
			t.Errorf("%q: %+v %v, want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"",
		"/srv/git/widgets.git",
		"../remote.git",
		"./a:b/c",
		"file:///srv/git/acme/widgets.git",
		"https://gitlab.com/acme/widgets.git",
		"git@github-work:acme/widgets.git", // SSH host alias: not resolved
		"https://github.com/acme",
		"https://github.com/acme/widgets/tree/main",
		"https://github.com/../widgets",
		"https://github.com/acme/wid gets",
		"git@github.com:acme/",
		"ftp://github.com/acme/widgets",
	} {
		if got, ok := ParseRemote(raw, enterprise); ok {
			t.Errorf("%q accepted as %+v", raw, got)
		}
	}
	if _, ok := ParseRemote("https://ghe.example.com/team/service.git", nil); ok {
		t.Error("an unconfigured Enterprise host was accepted")
	}
	r := Repo{"github.com", "acme", "widgets"}
	if r.String() != "github.com/acme/widgets" || r.FullName() != "acme/widgets" {
		t.Fatalf("names: %q %q", r.String(), r.FullName())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/github -run TestParseRemote -v`
Expected: the build fails with `undefined: Repo` and `undefined: ParseRemote`.

- [ ] **Step 3: Implement**

`internal/github/remote.go`:
```go
package github

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Repo names a repository on a GitHub host.
type Repo struct{ Host, Owner, Name string }

// String is the HOST/OWNER/REPO form gh accepts for --repo.
func (r Repo) String() string { return r.Host + "/" + r.Owner + "/" + r.Name }

// FullName is OWNER/REPO.
func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

var repoPart = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ParseRemote recognizes a GitHub repository in a Git remote URL: https://,
// http://, ssh:// and git:// URLs, and scp-like [user@]host:owner/name. Only
// github.com (www.github.com and ssh.github.com included) and the given
// Enterprise hosts count. User information and ports are ignored, and the
// path must be exactly owner/name, with an optional .git suffix.
func ParseRemote(raw string, hosts []string) (Repo, bool) {
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Repo{}, false
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git":
		default:
			return Repo{}, false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// Git reads host:path as scp-like syntax only when no slash comes
		// before the first colon; anything else is a local path.
		colon := strings.Index(raw, ":")
		if colon <= 0 || strings.Contains(raw[:colon], "/") {
			return Repo{}, false
		}
		host, path = raw[:colon], raw[colon+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	switch host {
	case "www.github.com", "ssh.github.com":
		host = "github.com"
	}
	if host != "github.com" && !slices.Contains(hosts, host) {
		return Repo{}, false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok || strings.Contains(name, "/") {
		return Repo{}, false
	}
	for _, part := range []string{owner, name} {
		if !repoPart.MatchString(part) || part == "." || part == ".." {
			return Repo{}, false
		}
	}
	return Repo{Host: host, Owner: owner, Name: name}, true
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/github -run TestParseRemote -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/github
git commit -m "feat(github): recognize GitHub remotes"
```

---

### Task 3: Pull request query

**Files:**
- Create: `internal/github/pulls.go`
- Test: `internal/github/pulls_test.go`

**Interfaces:**
- Consumes: `Runner.Run`, `Repo` (Tasks 1–2), `gitcli.SafeText`.
- Produces:
  ```go
  const MaxHeads = 20
  var ErrRepositoryNotFound, ErrRateLimited error
  type PullRequest struct {
      Number int; Title, URL string
      State string            // OPEN, MERGED or CLOSED
      Draft bool
      BaseRepository string   // owner/name
      BaseRef, HeadRef, HeadOID string
      IntoDefault bool        // BaseRef is the base repository's default branch
      Review string           // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
      Checks string           // SUCCESS, FAILURE, ERROR, PENDING, EXPECTED or ""
      MergedAt, UpdatedAt time.Time
  }
  type Lookup struct{ Repository string; Heads map[string][]PullRequest }
  type Client struct{ Runner Runner }
  func (c Client) PullRequests(ctx context.Context, repo Repo, heads []string) (Lookup, error)
  ```

- [ ] **Step 1: Write the failing tests**

`internal/github/pulls_test.go`:
```go
package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// prJSON is one pull request node as the query returns it.
func prJSON(number int, state, base, head, headRepo, oid, updated string) string {
	headRepository := "null"
	if headRepo != "" {
		headRepository = `{"nameWithOwner":"` + headRepo + `"}`
	}
	merged := "null"
	if state == "MERGED" {
		merged = `"` + updated + `"`
	}
	return fmt.Sprintf(`{"number":%d,"title":"PR %d","url":"https://github.com/acme/widgets/pull/%d","state":%q,"isDraft":false,`+
		`"baseRefName":%q,"headRefName":%q,"headRefOid":%q,"headRepository":%s,"baseRepository":{"nameWithOwner":"acme/widgets"},`+
		`"mergedAt":%s,"updatedAt":%q,"reviewDecision":null,"commits":{"nodes":[]}}`,
		number, number, number, state, base, head, oid, headRepository, merged, updated)
}

const oid1, oid2 = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"

var sameRepoResponse = `{"data":{"repository":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[` +
	prJSON(40, "MERGED", "main", "feat/x", "acme/widgets", oid1, "2026-10-01T10:00:00Z") + `,` +
	`{"number":42,"title":"Add tokens","url":"https://github.com/acme/widgets/pull/42","state":"OPEN","isDraft":true,"baseRefName":"main",` +
	`"headRefName":"feat/x","headRefOid":"` + oid2 + `","headRepository":{"nameWithOwner":"ACME/widgets"},"baseRepository":{"nameWithOwner":"acme/widgets"},` +
	`"mergedAt":null,"updatedAt":"2026-09-30T10:00:00Z","reviewDecision":"CHANGES_REQUESTED","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}},` +
	prJSON(41, "OPEN", "main", "feat/x", "someone/widgets", oid1, "2026-10-02T10:00:00Z") + `,` + // same branch name, another fork
	prJSON(39, "CLOSED", "main", "feat/x", "", oid1, "2026-10-03T10:00:00Z") + // head repository deleted
	`]},"h1":{"nodes":[]},"parent":null}}}`

func TestPullRequestsKeepsOnlyThisRepositorysBranch(t *testing.T) {
	gh, dir := fakeGH(t, sameRepoResponse, 0)
	heads := []string{"feat/x", "@etc/passwd"}
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "acme", "widgets"}, heads)
	if err != nil {
		t.Fatal(err)
	}
	prs := got.Heads["feat/x"]
	if got.Repository != "acme/widgets" || len(prs) != 2 || len(got.Heads["@etc/passwd"]) != 0 {
		t.Fatalf("lookup: %+v", got)
	}
	open, merged := prs[0], prs[1]
	if open.Number != 42 || open.State != "OPEN" || !open.Draft || open.Review != "CHANGES_REQUESTED" || open.Checks != "FAILURE" || !open.IntoDefault || open.HeadOID != oid2 || !open.MergedAt.IsZero() || open.BaseRepository != "acme/widgets" {
		t.Fatalf("open: %+v", open)
	}
	if merged.Number != 40 || merged.State != "MERGED" || merged.MergedAt.IsZero() || merged.Review != "" || merged.Checks != "" {
		t.Fatalf("merged: %+v", merged)
	}
	args := recordedArgs(t, dir)
	if strings.Join(args[:3], " ") != "api graphql --hostname=github.com" || slices.Contains(args, "-F") {
		t.Fatalf("args %q", args)
	}
	query, ok := strings.CutPrefix(args[4], "query=")
	if args[3] != "-f" || !ok {
		t.Fatalf("query argument %q", args[3:5])
	}
	for _, value := range []string{"acme", "widgets", "feat/x", "@etc"} {
		if strings.Contains(query, value) {
			t.Errorf("query text contains %q", value)
		}
	}
	if want := []string{"-f", "owner=acme", "-f", "name=widgets", "-f", "h0=feat/x", "-f", "h1=@etc/passwd"}; !slices.Equal(args[5:], want) {
		t.Fatalf("fields %q, want %q", args[5:], want)
	}
}

func TestPullRequestsFindsForkPullRequestsInTheParent(t *testing.T) {
	seven := prJSON(7, "MERGED", "trunk", "fix", "me/widgets", oid1, "2026-10-04T10:00:00Z")
	response := `{"data":{"repository":{"nameWithOwner":"me/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[]},` +
		`"parent":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"trunk"},"h0":{"nodes":[` + seven + `,` +
		prJSON(8, "MERGED", "release", "fix", "me/widgets", oid1, "2026-10-05T10:00:00Z") + `,` + seven + `]}}}}}`
	gh, _ := fakeGH(t, response, 0)
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "me", "widgets"}, []string{"fix"})
	if err != nil {
		t.Fatal(err)
	}
	prs := got.Heads["fix"]
	if len(prs) != 2 || prs[0].Number != 8 || prs[0].IntoDefault || prs[1].Number != 7 || !prs[1].IntoDefault {
		t.Fatalf("fork pull requests: %+v", prs)
	}
}

func TestPullRequestsKeepsDataDespiteAnInaccessibleParent(t *testing.T) {
	// A fork of a private repository: GitHub answers for the fork, reports an
	// error for its parent, and gh exits non-zero.
	response := `{"data":{"repository":{"nameWithOwner":"me/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[` +
		prJSON(3, "OPEN", "main", "fix", "me/widgets", oid1, "2026-10-04T10:00:00Z") + `]},"parent":null}},` +
		`"errors":[{"type":"FORBIDDEN","message":"Resource not accessible","path":["repository","parent"]}]}`
	gh, _ := fakeGH(t, response, 1)
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "me", "widgets"}, []string{"fix"})
	if err != nil || len(got.Heads["fix"]) != 1 || got.Heads["fix"][0].Number != 3 {
		t.Fatalf("partial answer: %+v, %v", got, err)
	}
}

func TestPullRequestsOrdersOpenFirstAndKeepsFive(t *testing.T) {
	var nodes []string
	for i := 1; i <= 7; i++ {
		nodes = append(nodes, prJSON(i, "CLOSED", "main", "b", "acme/widgets", oid1, time.Date(2026, 10, i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)))
	}
	nodes = append(nodes, prJSON(99, "OPEN", "main", "b", "acme/widgets", oid1, "2020-01-01T00:00:00Z"))
	response := `{"data":{"repository":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[` + strings.Join(nodes, ",") + `]},"parent":null}}}`
	gh, _ := fakeGH(t, response, 0)
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "acme", "widgets"}, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, pr := range got.Heads["b"] {
		numbers = append(numbers, pr.Number)
	}
	if !slices.Equal(numbers, []int{99, 7, 6, 5, 4}) {
		t.Fatalf("order %v", numbers)
	}
}

func TestPullRequestsClassifiesErrors(t *testing.T) {
	repo := Repo{"github.com", "acme", "widgets"}
	for name, tc := range map[string]struct {
		stdout string
		code   int
		want   error
		text   string
	}{
		"not found":    {`{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve"}]}`, 1, ErrRepositoryNotFound, ""},
		"rate limited": {`{"data":{"repository":null},"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, 1, ErrRateLimited, ""},
		"other":        {`{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","message":"Resource protected\u001b[31m"}]}`, 1, nil, `Resource protected\x1b[31m`},
		"not logged":   {"", 4, ErrNotAuthenticated, ""},
		"garbage":      {"<html>", 1, nil, "gh api: exit status 1: first problem"},
		"empty answer": {`{"data":{}}`, 0, nil, "unexpected gh api output"},
	} {
		gh, _ := fakeGH(t, tc.stdout, tc.code)
		_, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), repo, []string{"b"})
		if tc.want != nil && !errors.Is(err, tc.want) || tc.want == nil && (err == nil || err.Error() != tc.text) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tooMany := make([]string, MaxHeads+1)
	missing := Client{Runner{Executable: "/nonexistent/gh"}}
	if _, err := missing.PullRequests(context.Background(), repo, tooMany); err == nil || errors.Is(err, ErrNotInstalled) {
		t.Fatalf("more than %d heads: %v", MaxHeads, err)
	}
}

func TestPullRequestQueryNamesOnlyVariables(t *testing.T) {
	query := pullRequestQuery(3)
	if strings.Count(query, "pullRequests(") != 6 || !strings.Contains(query, "$h2: String!") || !strings.Contains(query, "h2: pullRequests(headRefName: $h2,") || !strings.Contains(query, "parent {") {
		t.Fatalf("query %s", query)
	}
	if strings.Contains(pullRequestQuery(1), "$h1") {
		t.Fatal("one head declares a second variable")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/github -run 'TestPullRequest' -v`
Expected: the build fails with `undefined: Client`.

- [ ] **Step 3: Implement**

`internal/github/pulls.go`:
```go
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// MaxHeads is the most branch names one PullRequests query asks about.
const MaxHeads = 20

// maxPerHead is how many pull requests are kept for one branch.
const maxPerHead = 5

var (
	// ErrRepositoryNotFound reports a repository GitHub does not show to gh's
	// login, because it does not exist or the login lacks access.
	ErrRepositoryNotFound = errors.New("repository not found or not accessible")
	// ErrRateLimited reports an exhausted GitHub API rate limit.
	ErrRateLimited = errors.New("GitHub API rate limit reached")
)

// PullRequest is one pull request as the GraphQL API reports it. State,
// Review and Checks keep GitHub's enum values.
type PullRequest struct {
	Number         int
	Title, URL     string
	State          string // OPEN, MERGED or CLOSED
	Draft          bool
	BaseRepository string // owner/name
	BaseRef        string
	HeadRef        string
	HeadOID        string
	IntoDefault    bool      // BaseRef is the base repository's default branch
	Review         string    // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	Checks         string    // the head commit's rollup: SUCCESS, FAILURE, ERROR, PENDING, EXPECTED or ""
	MergedAt       time.Time // zero unless merged
	UpdatedAt      time.Time
}

// Lookup answers one repository: its name as GitHub reports it, and for each
// asked-for branch the pull requests whose head is that branch of that
// repository, open ones first, then the most recently updated.
type Lookup struct {
	Repository string
	Heads      map[string][]PullRequest
}

// Client asks GitHub through gh.
type Client struct{ Runner Runner }

// PullRequests asks once for the pull requests of up to MaxHeads branches of
// repo, including those in repo's parent, where a fork's pull requests live.
func (c Client) PullRequests(ctx context.Context, repo Repo, heads []string) (Lookup, error) {
	if len(heads) == 0 || len(heads) > MaxHeads {
		return Lookup{}, fmt.Errorf("pull request lookups take 1 to %d branches, not %d", MaxHeads, len(heads))
	}
	// Values travel only in -f raw fields. -F would read a file for @value and
	// expand {owner}-style placeholders, both of which a branch name can hold.
	args := []string{"api", "graphql", "--hostname=" + repo.Host, "-f", "query=" + pullRequestQuery(len(heads)), "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name}
	for i, head := range heads {
		args = append(args, "-f", fmt.Sprintf("h%d=%s", i, head))
	}
	out, runErr := c.Runner.Run(ctx, args...)
	var response struct {
		Data struct {
			Repository *rawRepository `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	// gh prints GraphQL error bodies on stdout and still exits non-zero.
	if err := json.Unmarshal(out.Stdout, &response); err != nil || response.Data.Repository == nil && len(response.Errors) == 0 {
		if runErr != nil {
			return Lookup{}, runErr
		}
		return Lookup{}, errors.New("unexpected gh api output")
	}
	if response.Data.Repository == nil {
		for _, e := range response.Errors {
			switch e.Type {
			case "NOT_FOUND":
				return Lookup{}, ErrRepositoryNotFound
			case "RATE_LIMITED":
				return Lookup{}, ErrRateLimited
			}
		}
		return Lookup{}, errors.New(gitcli.SafeText(response.Errors[0].Message))
	}
	return collect(*response.Data.Repository, heads), nil
}

// pullRequestFields are read for every pull request; see PullRequest.
const pullRequestFields = "number title url state isDraft baseRefName headRefName headRefOid" +
	" headRepository { nameWithOwner } baseRepository { nameWithOwner } mergedAt updatedAt reviewDecision" +
	" commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }"

// pullRequestQuery asks for n branches' pull requests on a repository and on
// its parent. The text depends on n alone: owner, name and branch names are
// variables and never part of the query.
func pullRequestQuery(n int) string {
	var vars, heads strings.Builder
	for i := range n {
		fmt.Fprintf(&vars, ", $h%d: String!", i)
		fmt.Fprintf(&heads, " h%d: pullRequests(headRefName: $h%d, first: 10, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { ...pr } }", i, i)
	}
	return "query($owner: String!, $name: String!" + vars.String() + ") {" +
		" repository(owner: $owner, name: $name) { nameWithOwner defaultBranchRef { name }" + heads.String() +
		" parent { nameWithOwner defaultBranchRef { name }" + heads.String() + " } } }" +
		" fragment pr on PullRequest { " + pullRequestFields + " }"
}

type rawPullRequest struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	BaseRefName    string `json:"baseRefName"`
	HeadRefName    string `json:"headRefName"`
	HeadRefOid     string `json:"headRefOid"`
	HeadRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	BaseRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"baseRepository"`
	MergedAt       *time.Time `json:"mergedAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	ReviewDecision *string    `json:"reviewDecision"`
	Commits        struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// rawRepository is a repository object of the query, whose pull request
// connections are aliased h0…hN.
type rawRepository struct {
	NameWithOwner string
	DefaultBranch string
	Parent        *rawRepository
	Heads         map[string][]rawPullRequest // by alias
}

func (r *rawRepository) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	r.Heads = map[string][]rawPullRequest{}
	for key, value := range fields {
		var err error
		switch {
		case key == "nameWithOwner":
			err = json.Unmarshal(value, &r.NameWithOwner)
		case key == "defaultBranchRef":
			var ref *struct {
				Name string `json:"name"`
			}
			if err = json.Unmarshal(value, &ref); err == nil && ref != nil {
				r.DefaultBranch = ref.Name
			}
		case key == "parent":
			err = json.Unmarshal(value, &r.Parent)
		case strings.HasPrefix(key, "h"):
			var connection *struct {
				Nodes []rawPullRequest `json:"nodes"`
			}
			if err = json.Unmarshal(value, &connection); err == nil && connection != nil {
				r.Heads[key] = connection.Nodes
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// collect keeps, per head, the pull requests whose head is that branch of the
// queried repository: the same branch name in someone else's fork never
// counts, nor does a pull request whose head repository was deleted.
func collect(repo rawRepository, heads []string) Lookup {
	lookup := Lookup{Repository: repo.NameWithOwner, Heads: map[string][]PullRequest{}}
	for i, head := range heads {
		alias := fmt.Sprintf("h%d", i)
		var prs []PullRequest
		seen := map[string]bool{}
		add := func(nodes []rawPullRequest, defaultBranch string) {
			for _, n := range nodes {
				if n.HeadRefName != head || n.HeadRepository == nil || !strings.EqualFold(n.HeadRepository.NameWithOwner, repo.NameWithOwner) {
					continue
				}
				pr := PullRequest{Number: n.Number, Title: n.Title, URL: n.URL, State: n.State, Draft: n.IsDraft, BaseRef: n.BaseRefName, HeadRef: n.HeadRefName,
					HeadOID: n.HeadRefOid, IntoDefault: defaultBranch != "" && n.BaseRefName == defaultBranch, UpdatedAt: n.UpdatedAt}
				if n.BaseRepository != nil {
					pr.BaseRepository = n.BaseRepository.NameWithOwner
				}
				key := pr.BaseRepository + "#" + strconv.Itoa(pr.Number)
				if seen[key] {
					continue
				}
				seen[key] = true
				if n.MergedAt != nil {
					pr.MergedAt = *n.MergedAt
				}
				if n.ReviewDecision != nil {
					pr.Review = *n.ReviewDecision
				}
				if commits := n.Commits.Nodes; len(commits) > 0 && commits[0].Commit.StatusCheckRollup != nil {
					pr.Checks = commits[0].Commit.StatusCheckRollup.State
				}
				prs = append(prs, pr)
			}
		}
		add(repo.Heads[alias], repo.DefaultBranch)
		if repo.Parent != nil {
			add(repo.Parent.Heads[alias], repo.Parent.DefaultBranch)
		}
		sort.SliceStable(prs, func(a, b int) bool {
			if open := prs[a].State == "OPEN"; open != (prs[b].State == "OPEN") {
				return open
			}
			if !prs[a].UpdatedAt.Equal(prs[b].UpdatedAt) {
				return prs[a].UpdatedAt.After(prs[b].UpdatedAt)
			}
			return prs[a].Number > prs[b].Number
		})
		lookup.Heads[head] = prs[:min(len(prs), maxPerHead)]
	}
	return lookup
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/github -v`
Expected: PASS for all tests in the package.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/github
git commit -m "feat(github): query pull requests for branches"
```

---

### Task 4: Upstream remotes and remote URLs from Git

**Files:**
- Modify: `internal/git/cleanup.go` (`Branch` fields, `LocalBranches` format)
- Modify: `internal/git/metadata.go` (`RemoteURL`)
- Modify: `internal/git/service.go` (passthrough)
- Test: `internal/git/cleanup_test.go`, `internal/git/readonly_test.go`

**Interfaces:**
- Produces:
  ```go
  type Branch struct { /* existing fields */; Remote, RemoteRef string } // e.g. "origin", "refs/heads/x"; from configuration, so they survive a gone upstream
  func (r Runner) RemoteURL(ctx context.Context, path, remote string) (string, error) // `git remote get-url`, applies insteadOf
  func (s Service) RemoteURL(ctx context.Context, path, remote string) (string, error)
  ```

- [ ] **Step 1: Write the failing tests**

`internal/git/cleanup_test.go`:
```diff
--- a/internal/git/cleanup_test.go
+++ b/internal/git/cleanup_test.go
@@ -160,6 +160,29 @@ func TestLocalBranches(t *testing.T) {
 	if len(got) != 3 || !objectID(byName["plain"].OID) || byName["plain"].Upstream != "" || !byName["tracked"].Gone || byName["main"].Gone {
 		t.Fatalf("branches: %+v", got)
 	}
+	// The upstream's remote and branch come from configuration, so a gone
+	// upstream still names them.
+	if b := byName["tracked"]; b.Remote != "origin" || b.RemoteRef != "refs/heads/tracked" {
+		t.Fatalf("gone upstream: %+v", b)
+	}
+	if b := byName["plain"]; b.Remote != "" || b.RemoteRef != "" {
+		t.Fatalf("no upstream: %+v", b)
+	}
+}
+
+func TestRemoteURLAppliesInsteadOf(t *testing.T) {
+	repo := disposable(t)
+	gitCmd(t, repo, "config", "url.https://github.com/.insteadOf", "gh:")
+	gitCmd(t, repo, "remote", "add", "origin", "gh:acme/widgets.git")
+	r := Runner{}
+	if url, err := r.RemoteURL(context.Background(), repo, "origin"); err != nil || url != "https://github.com/acme/widgets.git" {
+		t.Fatalf("url %q, %v", url, err)
+	}
+	for _, remote := range []string{"", "--upload-pack=x", "two words", "missing"} {
+		if _, err := r.RemoteURL(context.Background(), repo, remote); err == nil {
+			t.Errorf("remote %q accepted", remote)
+		}
+	}
 }
 
 func TestDeleteBranchComparesAndCleansConfig(t *testing.T) {
```

`internal/git/readonly_test.go`:
```diff
--- a/internal/git/readonly_test.go
+++ b/internal/git/readonly_test.go
@@ -181,3 +181,27 @@ func TestWorktreesReadOnly(t *testing.T) {
 		t.Fatalf("listing worktrees pruned administrative files: before %d, after %d, %v", len(before), len(after), err)
 	}
 }
+
+func TestBranchAndRemoteReadsLeaveConfigUnchanged(t *testing.T) {
+	d := disposable(t)
+	write(t, filepath.Join(d, "tracked"), "committed\n")
+	commit(t, d)
+	gitCmd(t, d, "remote", "add", "origin", "https://github.com/acme/widgets.git")
+	gitCmd(t, d, "config", "branch.main.remote", "origin")
+	gitCmd(t, d, "config", "branch.main.merge", "refs/heads/main")
+	config := filepath.Join(d, ".git", "config")
+	before, err := os.ReadFile(config)
+	if err != nil {
+		t.Fatal(err)
+	}
+	r := Runner{}
+	if _, err := r.LocalBranches(context.Background(), d); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := r.RemoteURL(context.Background(), d, "origin"); err != nil {
+		t.Fatal(err)
+	}
+	if after, err := os.ReadFile(config); err != nil || !bytes.Equal(before, after) {
+		t.Fatalf("configuration changed: %v", err)
+	}
+}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/git -run 'TestLocalBranches$|TestRemoteURL|TestBranchAndRemoteReads' -v`
Expected: the build fails with `b.Remote undefined` and `r.RemoteURL undefined`.

- [ ] **Step 3: Implement**

`internal/git/cleanup.go`:
```diff
--- a/internal/git/cleanup.go
+++ b/internal/git/cleanup.go
@@ -134,10 +134,14 @@ type Branch struct {
 	Upstream string // e.g. refs/remotes/origin/x; "" when none
 	Gone     bool   // upstream configured but its tracking ref is missing
 	Symref   string // target of a symbolic ref such as refs/heads/master -> refs/heads/main; "" for a normal branch
+	// Remote and RemoteRef name the upstream's remote and its branch there,
+	// such as origin and refs/heads/x. Both come from the branch's
+	// configuration, so they survive a deleted remote branch; "" without one.
+	Remote, RemoteRef string
 }
 
 func (r Runner) LocalBranches(ctx context.Context, path string) ([]Branch, error) {
-	out, err := r.Run(ctx, path, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(upstream)%00%(upstream:track)%00%(symref)", "refs/heads")
+	out, err := r.Run(ctx, path, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(upstream)%00%(upstream:track)%00%(symref)%00%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads")
 	if err != nil {
 		return nil, err
 	}
@@ -148,10 +152,10 @@ func (r Runner) LocalBranches(ctx context.Context, path string) ([]Branch, error
 		}
 		fields := strings.Split(line, "\x00")
 		name, ok := strings.CutPrefix(fields[0], "refs/heads/")
-		if len(fields) != 5 || !ok || name == "" || !objectID(fields[1]) {
+		if len(fields) != 7 || !ok || name == "" || !objectID(fields[1]) {
 			return nil, fmt.Errorf("malformed branch list")
 		}
-		branches = append(branches, Branch{Name: name, OID: fields[1], Upstream: fields[2], Gone: fields[3] == "[gone]", Symref: fields[4]})
+		branches = append(branches, Branch{Name: name, OID: fields[1], Upstream: fields[2], Gone: fields[3] == "[gone]", Symref: fields[4], Remote: fields[5], RemoteRef: fields[6]})
 	}
 	return branches, nil
 }
```

`internal/git/metadata.go`:
```diff
--- a/internal/git/metadata.go
+++ b/internal/git/metadata.go
@@ -211,6 +211,23 @@ func (r Runner) ResolveRemote(ctx context.Context, path string, m Metadata, remo
 	return FetchTarget{Remote: remote, URL: url}, nil
 }
 
+// RemoteURL is a remote's URL with insteadOf rewrites applied. It reads
+// configuration only and never contacts the remote.
+func (r Runner) RemoteURL(ctx context.Context, path, remote string) (string, error) {
+	if remote == "" || strings.HasPrefix(remote, "-") || strings.ContainsAny(remote, " \t\r\n\x00") {
+		return "", fmt.Errorf("invalid remote")
+	}
+	out, err := r.Run(ctx, path, "remote", "get-url", remote)
+	if err != nil {
+		return "", err
+	}
+	url := strings.TrimSuffix(string(out.Stdout), "\n")
+	if url == "" || strings.Contains(url, "\n") {
+		return "", fmt.Errorf("missing remote URL")
+	}
+	return url, nil
+}
+
 func (r Runner) configBool(ctx context.Context, path string, m Metadata, key string) (bool, error) {
 	if len(m.Values(key)) == 0 {
 		return false, nil
```

`internal/git/service.go`:
```diff
--- a/internal/git/service.go
+++ b/internal/git/service.go
@@ -78,6 +78,9 @@ func (s Service) RemoveWorktree(ctx context.Context, path, worktree string) erro
 func (s Service) LocalBranches(ctx context.Context, path string) ([]Branch, error) {
 	return s.Read.LocalBranches(ctx, path)
 }
+func (s Service) RemoteURL(ctx context.Context, path, remote string) (string, error) {
+	return s.Read.RemoteURL(ctx, path, remote)
+}
 func (s Service) DeleteBranch(ctx context.Context, path, name, oid string) error {
 	return s.Write.DeleteBranch(ctx, path, name, oid)
 }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/git`
Expected: PASS, including the existing `LocalBranches` and cleanup tests.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/git
git commit -m "feat(git): read upstream remotes and remote URLs"
```

---

### Task 5: `[github]` configuration

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `type GitHub struct{ Enabled bool; Hosts []string }` and `Config.GitHub` (default `Enabled: true`; hosts lower-cased bare host names).

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:
```diff
--- a/internal/config/config_test.go
+++ b/internal/config/config_test.go
@@ -208,3 +208,25 @@ func TestUIRefreshSeconds(t *testing.T) {
 		}
 	}
 }
+
+func TestLoadGitHubSettings(t *testing.T) {
+	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"), false)
+	if err != nil || !cfg.GitHub.Enabled || len(cfg.GitHub.Hosts) != 0 {
+		t.Fatalf("defaults: %+v, %v", cfg.GitHub, err)
+	}
+	cfg, err = Load(writeConfig(t, "[github]\nenabled = false\nhosts = [\"GHE.Example.com\", \"git.corp.example.\"]\n"), true)
+	if err != nil || cfg.GitHub.Enabled || strings.Join(cfg.GitHub.Hosts, ",") != "ghe.example.com,git.corp.example" {
+		t.Fatalf("explicit: %+v, %v", cfg.GitHub, err)
+	}
+	if cfg, err := Load(writeConfig(t, "[github]\nhosts = []\n"), true); err != nil || !cfg.GitHub.Enabled {
+		t.Fatalf("enabled stays the default: %+v, %v", cfg.GitHub, err)
+	}
+	for _, host := range []string{"https://ghe.example.com", "ghe.example.com/team", "ghe.example.com:8443", " ", "", "-ghe.example.com", "ghe..example.com", "ghe example.com"} {
+		if _, err := Load(writeConfig(t, "[github]\nhosts = [\""+host+"\"]\n"), true); err == nil || !strings.Contains(err.Error(), "github.hosts entry") {
+			t.Errorf("host %q: %v", host, err)
+		}
+	}
+	if _, err := Load(writeConfig(t, "[github]\ntoken = \"x\"\n"), true); err == nil {
+		t.Error("unknown github key accepted")
+	}
+}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/config -run TestLoadGitHubSettings -v`
Expected: the build fails with `cfg.GitHub undefined`.

- [ ] **Step 3: Implement**

`internal/config/config.go`:
```diff
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -7,6 +7,7 @@ import (
 	"math"
 	"os"
 	"path/filepath"
+	"regexp"
 	"strconv"
 	"strings"
 	"time"
@@ -36,6 +37,7 @@ type Config struct {
 	StatusTimeoutSeconds int
 	ActionTimeoutSeconds int
 	UI                   UI
+	GitHub               GitHub
 	Workspaces           []Workspace
 	configDir            string
 }
@@ -48,6 +50,18 @@ type UI struct {
 	RefreshSeconds int
 }
 
+// GitHub configures the optional GitHub CLI integration.
+type GitHub struct {
+	// Enabled allows gh lookups; true unless [github] enabled = false.
+	Enabled bool
+	// Hosts are GitHub Enterprise Server host names besides github.com,
+	// lower-cased.
+	Hosts []string
+}
+
+// hostName is a bare DNS name: no scheme, port, path or whitespace.
+var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
+
 // Workspace defines discovery settings for a group of roots.
 type Workspace struct {
 	Name       string
@@ -119,6 +133,18 @@ func Load(path string, explicit bool) (Config, error) {
 			cfg.UI.RefreshSeconds = int(*seconds)
 		}
 	}
+	if raw.GitHub != nil {
+		if raw.GitHub.Enabled != nil {
+			cfg.GitHub.Enabled = *raw.GitHub.Enabled
+		}
+		for _, host := range raw.GitHub.Hosts {
+			name := strings.ToLower(strings.TrimSuffix(host, "."))
+			if len(name) > 253 || !hostName.MatchString(name) {
+				return Config{}, fmt.Errorf("github.hosts entry %q must be a host name such as github.example.com", host)
+			}
+			cfg.GitHub.Hosts = append(cfg.GitHub.Hosts, name)
+		}
+	}
 	if raw.DefaultWorkspace != nil {
 		cfg.DefaultWorkspace = *raw.DefaultWorkspace
 	}
@@ -246,7 +272,7 @@ func (c Config) Resolve(workspace string, roots []string, cwd string) (Workspace
 }
 
 func defaults(configDir string) Config {
-	return Config{UI: UI{Icons: "unicode", RefreshSeconds: DefaultRefreshSeconds}, StatusWorkers: DefaultStatusWorkers, ActionWorkers: DefaultActionWorkers,
+	return Config{UI: UI{Icons: "unicode", RefreshSeconds: DefaultRefreshSeconds}, GitHub: GitHub{Enabled: true}, StatusWorkers: DefaultStatusWorkers, ActionWorkers: DefaultActionWorkers,
 		StatusTimeoutSeconds: DefaultStatusTimeoutSeconds, ActionTimeoutSeconds: DefaultActionTimeoutSeconds,
 		configDir: configDir}
 }
@@ -322,6 +348,7 @@ func cloneWorkspace(w Workspace) Workspace {
 
 type rawConfig struct {
 	UI                   *rawUI         `toml:"ui"`
+	GitHub               *rawGitHub     `toml:"github"`
 	DefaultWorkspace     *string        `toml:"default_workspace"`
 	StatusWorkers        *int64         `toml:"status_workers"`
 	ActionWorkers        *int64         `toml:"action_workers"`
@@ -336,6 +363,11 @@ type rawUI struct {
 	RefreshSeconds *int64 `toml:"refresh_seconds"`
 }
 
+type rawGitHub struct {
+	Enabled *bool    `toml:"enabled"`
+	Hosts   []string `toml:"hosts"`
+}
+
 type rawWorkspace struct {
 	Name       string   `toml:"name"`
 	Paths      []string `toml:"paths"`
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/config
git commit -m "feat(config): add the [github] section"
```

---

### Task 6: GitHub service in the app layer

**Files:**
- Create: `internal/app/github.go`
- Modify: `internal/app/inspect.go` (`Row.GitHub`)
- Test: `internal/app/github_test.go`

**Interfaces:**
- Consumes: `github.Client`, `github.Repo`, `github.ParseRemote`, `github.MaxHeads`, the `github` errors (Tasks 1–3); `gitcli.Branch.Remote`/`RemoteRef`, `RemoteURL` (Task 4).
- Produces:
  ```go
  type GitHubInfo struct { Host, Repository, Branch string; PullRequests []PullRequest; CheckedAt time.Time; Error string } // JSON "github"
  type PullRequest struct { Number int; Title, URL, State string; Draft bool; BaseRepository, Base string; IntoDefaultBranch bool; HeadOID, Review, Checks string; MergedAt, UpdatedAt time.Time }
  const PullRequestOpen, PullRequestMerged, PullRequestClosed = "open", "merged", "closed"
  const ReviewApproved, ReviewChangesRequested, ReviewRequired = "approved", "changes_requested", "review_required"
  const ChecksPassing, ChecksFailing, ChecksPending = "passing", "failing", "pending"
  // Row gains: GitHub *GitHubInfo `json:"github,omitempty"`
  func (r Row) CurrentPullRequest() *PullRequest
  const GitHubTTL = 5 * time.Minute; const GitHubMinGap = 30 * time.Second
  type GitHubGit interface { LocalBranches(...); RemoteURL(...) }
  type PullRequestClient interface { PullRequests(context.Context, github.Repo, []string) (github.Lookup, error) }
  func NewGitHub(git GitHubGit, client PullRequestClient, hosts []string) *GitHub
  func (g *GitHub) Due(rows []Row, force bool) [][]Row
  func (g *GitHub) Lookup(ctx context.Context, group []Row)
  func (g *GitHub) LookupAll(ctx context.Context, rows []Row)
  func (g *GitHub) Annotate(rows []Row)
  // unexported, used by later tasks: (g *GitHub) targets(ctx, path, branches) map[string]githubTarget,
  // (g *GitHub) query(ctx, repo, heads) (string, map[string][]PullRequest, error), githubError(repo, err) string,
  // pullRequestFrom(github.PullRequest) PullRequest; field g.slots (chan struct{}, capacity 2), g.now
  // test helpers: fakeGitHubGit, fakePulls, ghRow(path, branch), upstream(name, remote, head), headA, githubOrigin
  ```

- [ ] **Step 1: Write the failing tests**

`internal/app/github_test.go`:
```go
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// fakeGitHubGit serves branch upstreams and remote URLs from memory.
type fakeGitHubGit struct {
	branches []gitcli.Branch
	urls     map[string]string // remote name to URL
	err      error
}

func (f fakeGitHubGit) LocalBranches(context.Context, string) ([]gitcli.Branch, error) {
	return f.branches, f.err
}

func (f fakeGitHubGit) RemoteURL(_ context.Context, _ string, remote string) (string, error) {
	if url, ok := f.urls[remote]; ok {
		return url, nil
	}
	return "", errors.New("no such remote")
}

// fakePulls answers pull request queries from memory and records each
// query's heads.
type fakePulls struct {
	mu    sync.Mutex
	prs   map[string][]github.PullRequest // by head
	err   error
	calls [][]string
}

func (f *fakePulls) PullRequests(_ context.Context, repo github.Repo, heads []string) (github.Lookup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(heads))
	if f.err != nil {
		return github.Lookup{}, f.err
	}
	lookup := github.Lookup{Repository: repo.FullName(), Heads: map[string][]github.PullRequest{}}
	for _, head := range heads {
		lookup.Heads[head] = f.prs[head]
	}
	return lookup, nil
}

func (f *fakePulls) queries() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

const headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// ghRow is an inspected worktree of /repo on branch, tracking origin.
func ghRow(path, branch string) Row {
	return Row{Repository: repository.Repository{Name: path[strings.LastIndex(path, "/")+1:], Path: path},
		Status: repository.Status{Branch: branch, Upstream: "origin/" + branch, ComparisonKnown: true, CommonDir: "/repo/.git", HeadOID: headA}}
}

// upstream is a local branch whose upstream is remote's branch head.
func upstream(name, remote, head string) gitcli.Branch {
	return gitcli.Branch{Name: name, OID: headA, Remote: remote, RemoteRef: "refs/heads/" + head}
}

var githubOrigin = map[string]string{"origin": "git@github.com:acme/widgets.git", "gitlab": "https://gitlab.com/acme/widgets.git"}

func TestGitHubLookupAnnotatesRows(t *testing.T) {
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a"), upstream("feat/b", "origin", "topic-b"), upstream("lab", "gitlab", "lab")}, urls: githubOrigin}
	pulls := &fakePulls{prs: map[string][]github.PullRequest{"feat/a": {{Number: 42, State: "OPEN", Review: "CHANGES_REQUESTED", Checks: "FAILURE", BaseRef: "main", IntoDefault: true}}}}
	gh := NewGitHub(git, pulls, nil)
	noUpstream := Row{Repository: repository.Repository{Name: "local", Path: "/repo-local"}, Status: repository.Status{Branch: "local", CommonDir: "/repo/.git"}}
	rows := []Row{ghRow("/repo", "feat/a"), ghRow("/repo-b", "feat/b"), ghRow("/repo-lab", "lab"), noUpstream}
	due := gh.Due(rows, false)
	if len(due) != 1 || len(due[0]) != 3 {
		t.Fatalf("due: %+v", due)
	}
	gh.Lookup(context.Background(), due[0])
	if calls := pulls.queries(); len(calls) != 1 || !slices.Equal(calls[0], []string{"feat/a", "topic-b"}) {
		t.Fatalf("queries: %q", calls)
	}
	gh.Annotate(rows)
	a := rows[0].GitHub
	if a == nil || a.Host != "github.com" || a.Repository != "acme/widgets" || a.Branch != "feat/a" || a.CheckedAt.IsZero() || a.Error != "" {
		t.Fatalf("feat/a: %+v", a)
	}
	if pr := rows[0].CurrentPullRequest(); pr == nil || pr.Number != 42 || pr.State != PullRequestOpen || pr.Review != ReviewChangesRequested || pr.Checks != ChecksFailing || !pr.IntoDefaultBranch {
		t.Fatalf("current pull request: %+v", pr)
	}
	if b := rows[1].GitHub; b == nil || b.Branch != "topic-b" || b.PullRequests == nil || len(b.PullRequests) != 0 || rows[1].CurrentPullRequest() != nil {
		t.Fatalf("feat/b: %+v", b)
	}
	if rows[2].GitHub != nil || rows[3].GitHub != nil {
		t.Fatalf("non-GitHub rows annotated: %+v / %+v", rows[2].GitHub, rows[3].GitHub)
	}
}

func TestGitHubDueHonoursTTLForceAndPending(t *testing.T) {
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a")}, urls: githubOrigin}
	rows := []Row{ghRow("/repo", "feat/a")}
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		after time.Duration
		force bool
		due   bool
	}{
		{time.Minute, false, false},
		{GitHubTTL, false, true},
		{10 * time.Second, true, false},
		{GitHubMinGap, true, true},
	} {
		now := start
		gh := NewGitHub(git, &fakePulls{}, nil)
		gh.now = func() time.Time { return now }
		first := gh.Due(rows, false)
		if len(first) != 1 || len(gh.Due(rows, true)) != 0 {
			t.Fatal("a pending group was returned twice")
		}
		gh.Lookup(context.Background(), first[0])
		now = start.Add(tc.after)
		if got := gh.Due(rows, tc.force); (len(got) == 1) != tc.due {
			t.Errorf("after %s, force %v: due %v, want %v", tc.after, tc.force, len(got) == 1, tc.due)
		}
	}
}

func TestGitHubLookupChunksHeadsAndForgetsVanishedRows(t *testing.T) {
	var branches []gitcli.Branch
	var rows []Row
	for i := range 45 {
		name := fmt.Sprintf("b%02d", i)
		branches = append(branches, upstream(name, "origin", name))
		rows = append(rows, ghRow("/repo/"+name, name))
	}
	pulls := &fakePulls{}
	gh := NewGitHub(fakeGitHubGit{branches: branches, urls: githubOrigin}, pulls, nil)
	gh.LookupAll(context.Background(), rows)
	var sizes []int
	for _, heads := range pulls.queries() {
		sizes = append(sizes, len(heads))
	}
	if !slices.Equal(sizes, []int{20, 20, 5}) {
		t.Fatalf("query sizes %v", sizes)
	}
	gh.Due(nil, false)
	if len(gh.entries) != 0 {
		t.Fatalf("entries kept for vanished rows: %d", len(gh.entries))
	}
}

func TestGitHubFailedRecheckKeepsEarlierPullRequests(t *testing.T) {
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a")}, urls: githubOrigin}
	pulls := &fakePulls{prs: map[string][]github.PullRequest{"feat/a": {{Number: 7, State: "OPEN"}}}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	first := now
	gh := NewGitHub(git, pulls, nil)
	gh.now = func() time.Time { return now }
	rows := []Row{ghRow("/repo", "feat/a")}
	gh.LookupAll(context.Background(), rows)
	pulls.err = github.ErrNotAuthenticated
	now = now.Add(GitHubTTL)
	gh.LookupAll(context.Background(), rows)
	gh.Annotate(rows)
	info := rows[0].GitHub
	if info == nil || info.Error != "gh is not logged in to github.com — run gh auth login" || len(info.PullRequests) != 1 || !info.CheckedAt.Equal(first) {
		t.Fatalf("after a failed recheck: %+v", info)
	}
}

func TestGitHubLookupFailuresAndCancellation(t *testing.T) {
	rows := []Row{ghRow("/repo", "feat/a")}
	gh := NewGitHub(fakeGitHubGit{err: errors.New("broken refs")}, &fakePulls{}, nil)
	gh.LookupAll(context.Background(), rows)
	gh.Annotate(rows)
	if info := rows[0].GitHub; info == nil || !strings.Contains(info.Error, "could not read branch upstreams: broken refs") {
		t.Fatalf("branch list failure: %+v", info)
	}
	pulls := &fakePulls{}
	gh = NewGitHub(fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a")}, urls: githubOrigin}, pulls, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gh.Lookup(ctx, gh.Due(rows, false)[0])
	if len(gh.entries) != 0 || len(gh.Due(rows, false)) != 1 {
		t.Fatal("a cancelled lookup recorded a result or stayed pending")
	}
}

func TestGitHubErrorMessages(t *testing.T) {
	repo := github.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}
	for err, want := range map[error]string{
		github.ErrNotAuthenticated:   "gh is not logged in to github.com — run gh auth login",
		github.ErrRepositoryNotFound: "GitHub repository acme/widgets not found or not accessible with gh's login",
		github.ErrRateLimited:        "GitHub API rate limit reached; gitperch checks again later",
		errors.New("timeout\x1b[2J"): `GitHub lookup failed: timeout\x1b[2J`,
	} {
		if got := githubError(repo, err); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}

func TestPullRequestFromMapsGitHubValues(t *testing.T) {
	for _, tc := range []struct {
		in                    github.PullRequest
		state, review, checks string
	}{
		{github.PullRequest{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS"}, PullRequestOpen, ReviewApproved, ChecksPassing},
		{github.PullRequest{State: "OPEN", Review: "REVIEW_REQUIRED", Checks: "ERROR"}, PullRequestOpen, ReviewRequired, ChecksFailing},
		{github.PullRequest{State: "MERGED", Checks: "EXPECTED"}, PullRequestMerged, "", ChecksPending},
		{github.PullRequest{State: "CLOSED", Checks: "PENDING"}, PullRequestClosed, "", ChecksPending},
		{github.PullRequest{State: "SOMETHING_NEW"}, PullRequestClosed, "", ""},
	} {
		got := pullRequestFrom(tc.in)
		if got.State != tc.state || got.Review != tc.review || got.Checks != tc.checks {
			t.Errorf("%+v: %+v", tc.in, got)
		}
	}
}

func TestReportIncludesGitHubOnlyWhenAnnotated(t *testing.T) {
	row := ghRow("/repo", "feat/a")
	var out bytes.Buffer
	if err := WriteJSON(&out, []Row{row}, nil); err != nil || strings.Contains(out.String(), `"github"`) {
		t.Fatalf("unannotated report: %v\n%s", err, out.String())
	}
	row.GitHub = &GitHubInfo{Host: "github.com", Repository: "acme/widgets", Branch: "feat/a", PullRequests: []PullRequest{{Number: 42, State: PullRequestOpen, Checks: ChecksFailing}}}
	out.Reset()
	if err := WriteJSON(&out, []Row{row}, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"github": {`, `"repository": "acme/widgets"`, `"number": 42`, `"state": "open"`, `"checks": "failing"`, `"into_default_branch": false`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %s:\n%s", want, out.String())
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app -run 'GitHub|PullRequestFrom|ReportIncludes' -v`
Expected: the build fails with `undefined: NewGitHub` (and the other new names).

- [ ] **Step 3: Implement**

`internal/app/inspect.go`:
```diff
--- a/internal/app/inspect.go
+++ b/internal/app/inspect.go
@@ -22,6 +22,8 @@ type Row struct {
 	Status    repository.Status `json:"status"`
 	LastFetch time.Time         `json:"last_successful_fetch,omitzero"`
 	Worktree  *WorktreeInfo     `json:"worktree,omitempty"`
+	// GitHub is the branch's pull request state, set only by GitHub.Annotate.
+	GitHub *GitHubInfo `json:"github,omitempty"`
 }
 
 // Selectable reports whether fetch, push and pull can target the row: stale
```

`internal/app/github.go`:
```go
package app

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
)

// GitHubInfo is what gh reported for a row's branch: the upstream's GitHub
// repository and the pull requests whose head is that branch.
type GitHubInfo struct {
	Host         string        `json:"host"`
	Repository   string        `json:"repository"`    // owner/name as GitHub reports it
	Branch       string        `json:"branch"`        // the upstream branch on GitHub
	PullRequests []PullRequest `json:"pull_requests"` // most relevant first; [] when none
	CheckedAt    time.Time     `json:"checked_at,omitzero"`
	// Error says why the latest lookup failed; PullRequests are then those
	// read at CheckedAt.
	Error string `json:"error,omitempty"`
}

// PullRequest is one pull request of a row's branch.
type PullRequest struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	URL               string    `json:"url"`
	State             string    `json:"state"` // PullRequestOpen, PullRequestMerged or PullRequestClosed
	Draft             bool      `json:"draft,omitempty"`
	BaseRepository    string    `json:"base_repository"`
	Base              string    `json:"base"`
	IntoDefaultBranch bool      `json:"into_default_branch"`
	HeadOID           string    `json:"head_oid"`
	Review            string    `json:"review,omitempty"` // ReviewApproved, ReviewChangesRequested or ReviewRequired
	Checks            string    `json:"checks,omitempty"` // ChecksPassing, ChecksFailing or ChecksPending
	MergedAt          time.Time `json:"merged_at,omitzero"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Pull request facts as JSON and the interface name them.
const (
	PullRequestOpen   = "open"
	PullRequestMerged = "merged"
	PullRequestClosed = "closed"

	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewRequired         = "review_required"

	ChecksPassing = "passing"
	ChecksFailing = "failing"
	ChecksPending = "pending"
)

// pullRequestFrom maps GitHub's enum values to gitperch's. An unknown state
// reads as closed, so it never counts as open work or as a merge.
func pullRequestFrom(p github.PullRequest) PullRequest {
	pr := PullRequest{Number: p.Number, Title: p.Title, URL: p.URL, State: PullRequestClosed, Draft: p.Draft, BaseRepository: p.BaseRepository,
		Base: p.BaseRef, IntoDefaultBranch: p.IntoDefault, HeadOID: p.HeadOID, MergedAt: p.MergedAt, UpdatedAt: p.UpdatedAt}
	switch p.State {
	case "OPEN":
		pr.State = PullRequestOpen
	case "MERGED":
		pr.State = PullRequestMerged
	}
	switch p.Review {
	case "APPROVED":
		pr.Review = ReviewApproved
	case "CHANGES_REQUESTED":
		pr.Review = ReviewChangesRequested
	case "REVIEW_REQUIRED":
		pr.Review = ReviewRequired
	}
	switch p.Checks {
	case "SUCCESS":
		pr.Checks = ChecksPassing
	case "FAILURE", "ERROR":
		pr.Checks = ChecksFailing
	case "PENDING", "EXPECTED":
		pr.Checks = ChecksPending
	}
	return pr
}

// CurrentPullRequest is the first listed pull request, an open one when any
// is open; nil without GitHub data or pull requests.
func (r Row) CurrentPullRequest() *PullRequest {
	if r.GitHub == nil || len(r.GitHub.PullRequests) == 0 {
		return nil
	}
	return &r.GitHub.PullRequests[0]
}

// GitHub lookup cadence; see GitHub.Due.
const (
	// GitHubTTL is how long a lookup's result is reused.
	GitHubTTL = 5 * time.Minute
	// GitHubMinGap is the youngest result a forced recheck replaces.
	GitHubMinGap = 30 * time.Second
	// githubSlots bounds concurrent gh processes.
	githubSlots = 2
)

// GitHubGit is the local Git surface pull request lookups read.
type GitHubGit interface {
	LocalBranches(context.Context, string) ([]gitcli.Branch, error)
	RemoteURL(context.Context, string, string) (string, error)
}

// PullRequestClient answers pull request queries; github.Client is one.
type PullRequestClient interface {
	PullRequests(context.Context, github.Repo, []string) (github.Lookup, error)
}

type githubEntry struct {
	info    *GitHubInfo // nil: the branch has no GitHub upstream
	checked time.Time   // when the latest lookup finished
}

// GitHub looks up the pull requests of rows' branches through gh and caches
// them per row path and branch. It is safe for concurrent use.
type GitHub struct {
	git    GitHubGit
	client PullRequestClient
	hosts  []string
	slots  chan struct{}
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]githubEntry
	pending map[string]bool // groups being looked up
}

// NewGitHub returns a service that asks client about branches whose upstream
// is on github.com or one of hosts.
func NewGitHub(git GitHubGit, client PullRequestClient, hosts []string) *GitHub {
	return &GitHub{git: git, client: client, hosts: slices.Clone(hosts), slots: make(chan struct{}, githubSlots), now: time.Now,
		entries: map[string]githubEntry{}, pending: map[string]bool{}}
}

func githubKey(r Row) string { return r.Path + "\x00" + r.Status.Branch }

// githubGroup shares one branch listing per repository.
func githubGroup(r Row) string {
	if r.Status.CommonDir != "" {
		return r.Status.CommonDir
	}
	return r.Path
}

// lookupCandidate reports whether a row's branch can have pull requests: an
// inspected worktree on a branch with an upstream.
func lookupCandidate(r Row) bool {
	s := r.Status
	return r.Selectable() && s.Error == "" && !s.Detached && !s.Unborn && s.Branch != "" && s.Upstream != ""
}

// Due returns the candidate rows whose lookup is missing or older than
// GitHubTTL, or, when forced, older than GitHubMinGap, grouped by repository.
// Returned groups stay pending until Lookup finishes them, so no group is
// looked up twice at once. Entries of rows no longer present are dropped.
func (g *GitHub) Due(rows []Row, force bool) [][]Row {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	present := map[string]bool{}
	groups := map[string][]Row{}
	var order []string
	for _, row := range rows {
		if !lookupCandidate(row) {
			continue
		}
		key, group := githubKey(row), githubGroup(row)
		present[key] = true
		if g.pending[group] {
			continue
		}
		if entry, ok := g.entries[key]; ok {
			age := now.Sub(entry.checked)
			if age < GitHubTTL && !(force && age >= GitHubMinGap) {
				continue
			}
		}
		if _, seen := groups[group]; !seen {
			order = append(order, group)
		}
		groups[group] = append(groups[group], row)
	}
	for key := range g.entries {
		if !present[key] {
			delete(g.entries, key)
		}
	}
	due := make([][]Row, 0, len(order))
	for _, group := range order {
		g.pending[group] = true
		due = append(due, groups[group])
	}
	return due
}

// Lookup refreshes the entries of one group returned by Due. Failures are
// recorded on the rows' entries, never returned; a cancelled lookup records
// nothing.
func (g *GitHub) Lookup(ctx context.Context, group []Row) {
	if len(group) == 0 {
		return
	}
	defer func() {
		g.mu.Lock()
		delete(g.pending, githubGroup(group[0]))
		g.mu.Unlock()
	}()
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	results := g.lookup(ctx, group)
	<-g.slots
	if ctx.Err() != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, row := range group {
		key := githubKey(row)
		info := results[key]
		if old := g.entries[key].info; info != nil && info.Error != "" && old != nil && old.Repository == info.Repository && old.Branch == info.Branch {
			info.PullRequests, info.CheckedAt = old.PullRequests, old.CheckedAt
		}
		g.entries[key] = githubEntry{info: info, checked: now}
	}
}

// LookupAll looks up every candidate row now, for gitperch status --github.
func (g *GitHub) LookupAll(ctx context.Context, rows []Row) {
	var wg sync.WaitGroup
	for _, group := range g.Due(rows, true) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Lookup(ctx, group)
		}()
	}
	wg.Wait()
}

// Annotate sets each row's GitHub field from the cache, without I/O.
func (g *GitHub) Annotate(rows []Row) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range rows {
		rows[i].GitHub = nil
		if !lookupCandidate(rows[i]) {
			continue
		}
		if entry, ok := g.entries[githubKey(rows[i])]; ok && entry.info != nil {
			info := *entry.info
			info.PullRequests = slices.Clone(info.PullRequests)
			rows[i].GitHub = &info
		}
	}
}

// githubTarget is a branch's upstream on GitHub.
type githubTarget struct {
	repo   github.Repo
	remote string // the upstream's remote name
	head   string // the upstream branch on GitHub
}

// targets maps branches to their upstream's GitHub repository and branch.
// Branches without an upstream, or whose remote is not on a GitHub host, are
// left out.
func (g *GitHub) targets(ctx context.Context, path string, branches []gitcli.Branch) map[string]githubTarget {
	targets := map[string]githubTarget{}
	urls := map[string]string{}
	for _, b := range branches {
		head, ok := strings.CutPrefix(b.RemoteRef, "refs/heads/")
		if b.Remote == "" || b.Remote == "." || !ok || head == "" {
			continue
		}
		url, seen := urls[b.Remote]
		if !seen {
			url, _ = g.git.RemoteURL(ctx, path, b.Remote) // unreadable: not GitHub
			urls[b.Remote] = url
		}
		if repo, ok := github.ParseRemote(url, g.hosts); ok {
			targets[b.Name] = githubTarget{repo: repo, remote: b.Remote, head: head}
		}
	}
	return targets
}

// query asks GitHub about heads of repo, github.MaxHeads at a time. It
// returns the repository's name as GitHub reports it and the pull requests by
// head. Callers hold one of the service's slots.
func (g *GitHub) query(ctx context.Context, repo github.Repo, heads []string) (string, map[string][]PullRequest, error) {
	heads = slices.Clone(heads)
	sort.Strings(heads)
	heads = slices.Compact(heads)
	name, prs := repo.FullName(), map[string][]PullRequest{}
	for start := 0; start < len(heads); start += github.MaxHeads {
		chunk := heads[start:min(len(heads), start+github.MaxHeads)]
		lookup, err := g.client.PullRequests(ctx, repo, chunk)
		if err != nil {
			return name, nil, err
		}
		if lookup.Repository != "" {
			name = lookup.Repository
		}
		for _, head := range chunk {
			list := []PullRequest{}
			for _, p := range lookup.Heads[head] {
				list = append(list, pullRequestFrom(p))
			}
			prs[head] = list
		}
	}
	return name, prs, nil
}

// lookup answers one group: it reads the repository's branches once, maps
// rows to GitHub, and asks once per GitHub repository.
func (g *GitHub) lookup(ctx context.Context, group []Row) map[string]*GitHubInfo {
	results := map[string]*GitHubInfo{}
	branches, err := g.git.LocalBranches(ctx, group[0].Path)
	if err != nil {
		for _, row := range group {
			results[githubKey(row)] = &GitHubInfo{PullRequests: []PullRequest{}, Error: "GitHub lookup failed: could not read branch upstreams: " + gitcli.SafeText(err.Error())}
		}
		return results
	}
	targets := g.targets(ctx, group[0].Path, branches)
	heads := map[github.Repo][]string{}
	for _, row := range group {
		if t, ok := targets[row.Status.Branch]; ok {
			heads[t.repo] = append(heads[t.repo], t.head)
		}
	}
	type answer struct {
		name string
		prs  map[string][]PullRequest
		err  error
	}
	answers := map[github.Repo]answer{}
	for repo, list := range heads {
		name, prs, err := g.query(ctx, repo, list)
		answers[repo] = answer{name, prs, err}
	}
	for _, row := range group {
		t, ok := targets[row.Status.Branch]
		if !ok {
			continue // no GitHub upstream: a nil entry
		}
		a := answers[t.repo]
		info := &GitHubInfo{Host: t.repo.Host, Repository: a.name, Branch: t.head, PullRequests: []PullRequest{}}
		if a.err != nil {
			info.Error = githubError(t.repo, a.err)
		} else {
			info.PullRequests, info.CheckedAt = a.prs[t.head], g.now()
		}
		results[githubKey(row)] = info
	}
	return results
}

// githubError states a failed lookup for display.
func githubError(repo github.Repo, err error) string {
	host, name := gitcli.SafeText(repo.Host), gitcli.SafeText(repo.FullName())
	switch {
	case errors.Is(err, github.ErrNotAuthenticated):
		return "gh is not logged in to " + host + " — run gh auth login"
	case errors.Is(err, github.ErrRepositoryNotFound):
		return "GitHub repository " + name + " not found or not accessible with gh's login"
	case errors.Is(err, github.ErrRateLimited):
		return "GitHub API rate limit reached; gitperch checks again later"
	}
	return "GitHub lookup failed: " + gitcli.SafeText(err.Error())
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/app -run 'GitHub|PullRequestFrom|ReportIncludes' -v`, then `go test ./internal/app`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/app
git commit -m "feat(app): look up and cache branch pull requests"
```

---

### Task 7: Attention from open pull requests

**Files:**
- Modify: `internal/app/attention.go`
- Test: `internal/app/attention_test.go`

**Interfaces:**
- Consumes: `Row.CurrentPullRequest`, the state, review and checks constants (Task 6).
- Produces: `ReasonPRChecksFailing = "pr_checks_failing"`, `ReasonPRChangesRequested = "pr_changes_requested"` (medium, after `behind_upstream`), `pullRequestName(*PullRequest) string`.

- [ ] **Step 1: Write the failing test**

`internal/app/attention_test.go`:
```diff
--- a/internal/app/attention_test.go
+++ b/internal/app/attention_test.go
@@ -207,3 +207,41 @@ func TestAttentionFromRealGitState(t *testing.T) {
 		}
 	}
 }
+
+func TestAttentionFromOpenPullRequest(t *testing.T) {
+	withPR := func(prs ...PullRequest) Row {
+		return Row{Status: tracking(0, 0), GitHub: &GitHubInfo{Host: "github.com", Repository: "acme/widgets", Branch: "main", PullRequests: prs}}
+	}
+	cases := []struct {
+		name     string
+		row      Row
+		level    Level
+		reasons  []Reason
+		describe []string
+	}{
+		{"failing checks", withPR(PullRequest{Number: 42, State: PullRequestOpen, Checks: ChecksFailing}), Medium,
+			[]Reason{ReasonPRChecksFailing}, []string{"PR #42 checks failing"}},
+		{"changes requested", withPR(PullRequest{Number: 42, State: PullRequestOpen, Review: ReviewChangesRequested, Checks: ChecksPassing}), Medium,
+			[]Reason{ReasonPRChangesRequested}, []string{"PR #42: changes requested"}},
+		{"both, after behind", func() Row {
+			r := withPR(PullRequest{Number: 7, State: PullRequestOpen, Draft: true, Review: ReviewChangesRequested, Checks: ChecksFailing})
+			r.Status.Behind = 2
+			return r
+		}(), Medium, []Reason{ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested}, []string{1: "PR #7 checks failing", 2: "PR #7: changes requested"}},
+		{"waiting for review", withPR(PullRequest{Number: 42, State: PullRequestOpen, Review: ReviewRequired, Checks: ChecksPending}), Low, nil, nil},
+		{"failing checks on a closed pull request", withPR(PullRequest{Number: 42, State: PullRequestClosed, Checks: ChecksFailing}), Low, nil, nil},
+		{"without GitHub data", Row{Status: tracking(0, 0)}, Low, nil, nil},
+	}
+	for _, tc := range cases {
+		a := tc.row.Attention()
+		if a.Level != tc.level || !slices.Equal(a.Reasons, tc.reasons) && (len(a.Reasons) != 0 || len(tc.reasons) != 0) {
+			t.Errorf("%s: %+v, want %s %v", tc.name, a, tc.level, tc.reasons)
+			continue
+		}
+		for i, want := range tc.describe {
+			if want != "" && tc.row.Describe(a.Reasons[i]) != want {
+				t.Errorf("%s: describe %q, want %q", tc.name, tc.row.Describe(a.Reasons[i]), want)
+			}
+		}
+	}
+}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/app -run TestAttentionFromOpenPullRequest -v`
Expected: the build fails with `undefined: ReasonPRChecksFailing`.

- [ ] **Step 3: Implement**

`internal/app/attention.go`:
```diff
--- a/internal/app/attention.go
+++ b/internal/app/attention.go
@@ -77,6 +77,10 @@ const (
 	// worktree's Lifecycle: worth a review, nothing at risk.
 	ReasonWorktreeFinished Reason = "worktree_finished"
 	ReasonWorktreeIdle     Reason = "worktree_idle"
+	// ReasonPRChecksFailing and ReasonPRChangesRequested come from the
+	// branch's open pull request on GitHub (Row.GitHub): someone has to act.
+	ReasonPRChecksFailing    Reason = "pr_checks_failing"
+	ReasonPRChangesRequested Reason = "pr_changes_requested"
 )
 
 // reasonOrder lists reasons from most to least severe; Attention.Reasons
@@ -84,7 +88,7 @@ const (
 var reasonOrder = []Reason{
 	ReasonActionFailed, ReasonInspectionFailed, ReasonConflicts, ReasonOperation,
 	ReasonDiverged, ReasonUncommitted, ReasonUntracked, ReasonUnpushed, ReasonDetachedCommits,
-	ReasonBehind, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree,
+	ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree,
 	ReasonWorktreeFinished, ReasonWorktreeIdle,
 }
 
@@ -95,7 +99,7 @@ func (r Reason) Level() Level {
 		return Critical
 	case ReasonDiverged, ReasonUncommitted, ReasonUntracked, ReasonUnpushed, ReasonDetachedCommits:
 		return High
-	case ReasonBehind, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree, ReasonWorktreeFinished, ReasonWorktreeIdle:
+	case ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree, ReasonWorktreeFinished, ReasonWorktreeIdle:
 		return Medium
 	}
 	return Low
@@ -173,6 +177,14 @@ func (r Row) Attention() Attention {
 	case s.Behind > 0:
 		a = a.With(ReasonBehind)
 	}
+	if pr := r.CurrentPullRequest(); pr != nil && pr.State == PullRequestOpen {
+		if pr.Checks == ChecksFailing {
+			a = a.With(ReasonPRChecksFailing)
+		}
+		if pr.Review == ReviewChangesRequested {
+			a = a.With(ReasonPRChangesRequested)
+		}
+	}
 	// Lifecycle reads the same facts, never attention, so the two cannot loop.
 	if l := r.Lifecycle(); l != nil {
 		switch l.State {
@@ -228,10 +240,22 @@ func (r Row) Describe(reason Reason) string {
 	case ReasonWorktreeIdle:
 		idle, _ := r.Inactivity()
 		return "linked worktree idle: no HEAD activity for " + span(idle)
+	case ReasonPRChecksFailing:
+		return pullRequestName(r.CurrentPullRequest()) + " checks failing"
+	case ReasonPRChangesRequested:
+		return pullRequestName(r.CurrentPullRequest()) + ": changes requested"
 	}
 	return gitcli.SafeText(string(reason))
 }
 
+// pullRequestName is "PR #42", or "the pull request" without one.
+func pullRequestName(pr *PullRequest) string {
+	if pr == nil {
+		return "the pull request"
+	}
+	return fmt.Sprintf("PR #%d", pr.Number)
+}
+
 func count(n int, noun string) string {
 	if n == 1 {
 		return "1 " + noun
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app`
Expected: PASS, including every existing attention test.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/app
git commit -m "feat(app): raise attention for failing checks and requested changes"
```

---

### Task 8: `gitperch status --github`

**Files:**
- Modify: `internal/app/format.go` (STATE markers, footer)
- Create: `cmd/gitperch/github.go` (`newGitHub`)
- Modify: `cmd/gitperch/main.go` (`--github`)
- Test: `internal/app/format_test.go`, `cmd/gitperch/github_test.go`

**Interfaces:**
- Consumes: `app.NewGitHub`, `LookupAll`, `Annotate` (Task 6); `config.GitHub` (Task 5); `github.Runner`, `github.Client` (Tasks 1, 3); `gitcli.Runner.SupportsWorktreeInventory`.
- Produces:
  ```go
  // cmd/gitperch: the gate and construction shared with the TUI (Tasks 10, 14, 16)
  func newGitHub(ctx context.Context, cfg config.Config, read gitcli.Runner) (*app.GitHub, github.Runner, error)
  // internal/app
  func githubMarkers(row Row) []string
  ```

- [ ] **Step 1: Write the failing tests**

`internal/app/format_test.go`:
```diff
--- a/internal/app/format_test.go
+++ b/internal/app/format_test.go
@@ -47,3 +47,25 @@ func TestReadOnlyReport(t *testing.T) {
 		t.Fatalf("%+v", report)
 	}
 }
+
+func TestTableShowsPullRequestMarkers(t *testing.T) {
+	plain := []Row{{Repository: repository.Repository{Path: "/r", Name: "r"}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}}}
+	var out bytes.Buffer
+	if err := WriteTable(&out, plain); err != nil {
+		t.Fatal(err)
+	}
+	if strings.Contains(out.String(), "GitHub") || strings.Contains(out.String(), "pr #") {
+		t.Fatalf("table without GitHub data mentions it:\n%s", out.String())
+	}
+	rows := append(plain, Row{Repository: repository.Repository{Path: "/w", Name: "w"}, Status: repository.Status{Branch: "feat", Upstream: "origin/feat", ComparisonKnown: true},
+		GitHub: &GitHubInfo{Error: "gh is not logged in to github.com — run gh auth login", PullRequests: []PullRequest{{Number: 42, State: PullRequestOpen, Draft: true, Checks: ChecksFailing, Review: ReviewChangesRequested}}}})
+	out.Reset()
+	if err := WriteTable(&out, rows); err != nil {
+		t.Fatal(err)
+	}
+	for _, want := range []string{"pr #42 draft, checks failing, changes requested, github: gh is not logged in to github.com", "* Pull request data comes from GitHub through gh."} {
+		if !strings.Contains(out.String(), want) {
+			t.Errorf("table lacks %q:\n%s", want, out.String())
+		}
+	}
+}
```

`cmd/gitperch/github_test.go`:
```go
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app -run TestTableShowsPullRequestMarkers -v` and `go test ./cmd/gitperch -run GitHub -v`
Expected: the table test FAILS (no `pr #42` markers), and the command build fails with `undefined: newGitHub`.

- [ ] **Step 3: Implement**

`internal/app/format.go`:
```diff
--- a/internal/app/format.go
+++ b/internal/app/format.go
@@ -41,6 +41,7 @@ func WriteJSON(w io.Writer, rows []Row, warnings []discovery.Warning) error {
 
 func WriteTable(w io.Writer, rows []Row) error {
 	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
+	fromGitHub := false
 	if _, err := fmt.Fprintln(tw, "REPOSITORY / PATH\tBRANCH\tCHANGES\tUNTRACKED\tCONFLICTS\tAHEAD*\tBEHIND*\tUPSTREAM\tSTATE\tATTENTION"); err != nil {
 		return err
 	}
@@ -96,6 +97,8 @@ func WriteTable(w io.Writer, rows []Row) error {
 		if s.Operation != "" {
 			markers = append(markers, "operation: "+s.Operation)
 		}
+		markers = append(markers, githubMarkers(row)...)
+		fromGitHub = fromGitHub || row.GitHub != nil
 		if _, err := fmt.Fprintf(tw, "%s / %s\t%s\tchanges:%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", gitcli.SafeText(row.Name), gitcli.SafeText(row.Path), gitcli.SafeText(branch), s.Changes, s.Untracked, s.Conflicts, ahead, behind, gitcli.SafeText(s.Upstream), gitcli.SafeText(strings.Join(markers, ", ")), attention.Level); err != nil {
 			return err
 		}
@@ -103,6 +106,34 @@ func WriteTable(w io.Writer, rows []Row) error {
 	if err := tw.Flush(); err != nil {
 		return err
 	}
-	_, err := fmt.Fprintln(w, "* Ahead/behind use locally known tracking refs; status does not contact remotes.")
+	if _, err := fmt.Fprintln(w, "* Ahead/behind use locally known tracking refs; status does not contact remotes."); err != nil || !fromGitHub {
+		return err
+	}
+	_, err := fmt.Fprintln(w, "* Pull request data comes from GitHub through gh.")
 	return err
 }
+
+// githubMarkers describe a row's pull request state for the STATE column.
+func githubMarkers(row Row) []string {
+	if row.GitHub == nil {
+		return nil
+	}
+	var markers []string
+	if pr := row.CurrentPullRequest(); pr != nil {
+		state := pr.State
+		if pr.Draft && state == PullRequestOpen {
+			state = "draft"
+		}
+		markers = append(markers, fmt.Sprintf("pr #%d %s", pr.Number, state))
+		if pr.State == PullRequestOpen && pr.Checks == ChecksFailing {
+			markers = append(markers, "checks failing")
+		}
+		if pr.State == PullRequestOpen && pr.Review == ReviewChangesRequested {
+			markers = append(markers, "changes requested")
+		}
+	}
+	if row.GitHub.Error != "" {
+		markers = append(markers, "github: "+row.GitHub.Error)
+	}
+	return markers
+}
```

`cmd/gitperch/github.go`:
```go
package main

import (
	"context"
	"errors"

	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/config"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
)

// newGitHub builds the pull request service and the gh runner behind it when
// the integration can run: enabled in the configuration, Git 2.36 or newer
// and gh on PATH. The error names the first condition that is not met.
func newGitHub(ctx context.Context, cfg config.Config, read gitcli.Runner) (*app.GitHub, github.Runner, error) {
	runner := github.Runner{Timeout: read.Timeout}
	switch {
	case !cfg.GitHub.Enabled:
		return nil, runner, errors.New("GitHub integration is disabled ([github] enabled = false)")
	case !read.SupportsWorktreeInventory(ctx):
		return nil, runner, errors.New("GitHub integration needs Git 2.36 or newer")
	case !runner.Available():
		return nil, runner, errors.New("GitHub integration needs the gh CLI on PATH (https://cli.github.com)")
	}
	return app.NewGitHub(read, github.Client{Runner: runner}, cfg.GitHub.Hosts), runner, nil
}
```

`cmd/gitperch/main.go`:
```diff
--- a/cmd/gitperch/main.go
+++ b/cmd/gitperch/main.go
@@ -49,6 +49,7 @@ func runContext(ctx context.Context, args []string, out, errOut io.Writer) int {
 	workspace := fs.String("workspace", "", "configured workspace name")
 	depth := fs.Int("max-depth", 4, "override maximum descendant depth (root is zero)")
 	noColor := fs.Bool("no-color", false, "disable dashboard color")
+	withGitHub := fs.Bool("github", false, "status: add pull request state from GitHub through gh")
 	fs.Usage = func() {
 		fmt.Fprintln(errOut, "Usage: gitperch [OPTIONS] [ROOT ...]\n       gitperch status [OPTIONS] [ROOT ...]\nTerminal dashboard for local Git repositories.")
 		fs.PrintDefaults()
@@ -132,9 +133,21 @@ func runContext(ctx context.Context, args []string, out, errOut io.Writer) int {
 			fmt.Fprintln(errOut, "--json requires status")
 			return 2
 		}
+		if *withGitHub {
+			fmt.Fprintln(errOut, "--github requires status")
+			return 2
+		}
 		return runTUI(ctx, cfg, ws, *noColor, out, errOut)
 	}
-	snapshot, err := app.Load(ctx, discovery.Options{Roots: ws.Paths, MaxDepth: ws.MaxDepth, IgnoreDirs: ws.IgnoreDirs}, gitcli.Runner{Timeout: time.Duration(cfg.StatusTimeoutSeconds) * time.Second}, cfg.StatusWorkers)
+	read := gitcli.Runner{Timeout: time.Duration(cfg.StatusTimeoutSeconds) * time.Second}
+	var gh *app.GitHub
+	if *withGitHub {
+		if gh, _, err = newGitHub(ctx, cfg, read); err != nil {
+			fmt.Fprintln(errOut, err)
+			return 2
+		}
+	}
+	snapshot, err := app.Load(ctx, discovery.Options{Roots: ws.Paths, MaxDepth: ws.MaxDepth, IgnoreDirs: ws.IgnoreDirs}, read, cfg.StatusWorkers)
 	if err != nil {
 		fmt.Fprintln(errOut, gitcli.SafeText(err.Error()))
 		if ctx.Err() != nil {
@@ -143,6 +156,20 @@ func runContext(ctx context.Context, args []string, out, errOut io.Writer) int {
 		return 2
 	}
 	rows := snapshot.Rows
+	if gh != nil {
+		gh.LookupAll(ctx, rows)
+		gh.Annotate(rows)
+		failed := 0
+		for _, row := range rows {
+			if row.GitHub != nil && row.GitHub.Error != "" {
+				failed++
+			}
+		}
+		if failed > 0 {
+			// Supplementary data: failed lookups never change the exit code.
+			fmt.Fprintf(errOut, "github: %d pull request lookup(s) failed; see each repository's GitHub error\n", failed)
+		}
+	}
 	if *asJSON {
 		err = app.WriteJSON(out, rows, snapshot.Warnings)
 	} else {
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app ./cmd/gitperch`
Expected: PASS. `TestStatusGitHubRefusesWhenUnavailable` passes even on machines with gh installed, because it runs with a `PATH` holding only git.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/app cmd/gitperch
git commit -m "feat: add gitperch status --github"
```

---

### Task 9: Pull requests in the dashboard

**Files:**
- Create: `internal/tui/github.go`
- Modify: `internal/tui/model.go` (fields, `githubMsg`, lookups after snapshots, forced rechecks on `r` and child exit, spinner)
- Modify: `internal/tui/view.go` (header activity, status labels, card height, next steps)
- Modify: `internal/tui/preview.go` (pull request line, file rows)
- Modify: `internal/tui/detail.go` (Pull request section, cancel on quit)
- Modify: `internal/tui/palette.go`, `internal/tui/actions.go` (forced rechecks after Refresh and batches)
- Test: `internal/tui/github_test.go`

**Interfaces:**
- Consumes: `app.GitHub` (`Due`, `Lookup`, `Annotate`), `Row.CurrentPullRequest`, the app constants (Task 6).
- Produces:
  ```go
  func (m *Model) EnableGitHub(g *app.GitHub)
  type githubMsg struct{}
  func (m *Model) forceGitHub()
  func (m *Model) githubLookups() tea.Cmd
  func (m *Model) applyGitHub()
  func openPullRequest(row app.Row) *app.PullRequest
  func pullRequestState(pr app.PullRequest) (label, color string)
  func checksLabel(checks string) (string, string); func reviewLabel(review string) (string, string)
  func hasPullRequestLine(row app.Row) bool
  func (m *Model) pullRequestLine(row app.Row, w int) string
  func (m *Model) pullRequestLines(row app.Row) []string
  func (m *Model) previewRows(row app.Row, h int) int; func pullRequestLineRows(row app.Row, h int) int
  // Model fields: github *app.GitHub; githubCtx context.Context; githubCancel context.CancelFunc; githubPending int; githubForce bool
  // test helpers: githubGit, githubPulls, githubRows(), prRow(prs...), failingPR
  ```

- [ ] **Step 1: Write the failing tests**

`internal/tui/github_test.go`:
```go
package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// githubGit reports every branch as tracking its namesake on origin, which is
// acme/widgets on github.com.
type githubGit struct{}

func (githubGit) LocalBranches(context.Context, string) ([]gitcli.Branch, error) {
	var branches []gitcli.Branch
	for _, name := range []string{"main", "feat/x"} {
		branches = append(branches, gitcli.Branch{Name: name, Remote: "origin", RemoteRef: "refs/heads/" + name})
	}
	return branches, nil
}

func (githubGit) RemoteURL(context.Context, string, string) (string, error) {
	return "https://github.com/acme/widgets.git", nil
}

// githubPulls gives feat/x one open pull request whose checks it reports.
type githubPulls struct {
	checks string
	calls  atomic.Int32
}

func (p *githubPulls) PullRequests(_ context.Context, repo github.Repo, heads []string) (github.Lookup, error) {
	p.calls.Add(1)
	return github.Lookup{Repository: repo.FullName(), Heads: map[string][]github.PullRequest{
		"feat/x": {{Number: 42, Title: "Add tokens", State: "OPEN", Checks: p.checks, BaseRef: "main", BaseRepository: "acme/widgets"}},
	}}, nil
}

// githubRows are two clean, synchronized repositories: alpha on main and zeta
// on feat/x.
func githubRows() []app.Row {
	row := func(name, branch string) app.Row {
		return app.Row{Repository: repository.Repository{Name: name, Path: "/repos/" + name},
			Status: repository.Status{Branch: branch, Upstream: "origin/" + branch, ComparisonKnown: true, CommonDir: "/repos/" + name + "/.git"}}
	}
	return []app.Row{row("alpha", "main"), row("zeta", "feat/x")}
}

// prRow is a clean, synchronized row whose branch has the given pull requests.
func prRow(prs ...app.PullRequest) app.Row {
	row := githubRows()[1]
	row.GitHub = &app.GitHubInfo{Host: "github.com", Repository: "acme/widgets", Branch: "feat/x", PullRequests: prs, CheckedAt: captureNow.Add(-2 * time.Minute)}
	return row
}

var failingPR = app.PullRequest{Number: 42, Title: "Add tokens", URL: "https://github.com/acme/widgets/pull/42", State: app.PullRequestOpen, Draft: true,
	BaseRepository: "acme/widgets", Base: "main", IntoDefaultBranch: true, Checks: app.ChecksFailing, Review: app.ReviewChangesRequested, UpdatedAt: captureNow.Add(-3 * time.Hour)}

func TestGitHubStatusLabels(t *testing.T) {
	m := New(context.Background(), nil, true)
	open := failingPR
	open.Draft, open.Review = false, ""
	changes := open
	changes.Checks = app.ChecksPassing
	changes.Review = app.ReviewChangesRequested
	healthy := changes
	healthy.Review = app.ReviewRequired
	ahead := prRow(open)
	ahead.Status.Ahead = 2
	for _, tc := range []struct {
		row         app.Row
		label, text string
	}{
		{prRow(open), "× checks #42", "unicode"},
		{prRow(changes), "! changes #42", "unicode"},
		{prRow(healthy), "✓ PR #42", "unicode"},
		{prRow(open), "! checks #42", "ascii"},
		{prRow(healthy), "ok PR #42", "ascii"},
		{ahead, "↑ 2 commits", "unicode"}, // Git state comes first
		{prRow(app.PullRequest{Number: 9, State: app.PullRequestClosed}), "✓ clean", "unicode"},
	} {
		m.iconMode = tc.text
		if label, _ := m.primaryStatus(tc.row); label != tc.label {
			t.Errorf("%s: %q, want %q", tc.text, label, tc.label)
		}
	}
}

func TestPreviewShowsPullRequestLine(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.clock = func() time.Time { return captureNow }
	m.applySnapshot(app.Snapshot{Rows: []app.Row{prRow(failingPR)}})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
	withLine := m.layout().bottom
	if view := m.View().Content; !strings.Contains(view, "PR #42 · draft · checks failing · changes requested · 3h ago") {
		t.Fatalf("missing pull request line:\n%s", view)
	}
	m.applySnapshot(app.Snapshot{Rows: githubRows()[1:]})
	if view := m.View().Content; strings.Contains(view, "PR #") || m.layout().bottom != withLine-1 {
		t.Fatalf("a branch without a pull request got a line or the same card height:\n%s", view)
	}
	failed := prRow()
	failed.GitHub.Error = "gh is not logged in to github.com — run gh auth login"
	m.applySnapshot(app.Snapshot{Rows: []app.Row{failed}})
	if view := m.View().Content; !strings.Contains(view, "GitHub: gh is not logged in to github.com — run gh auth login") {
		t.Fatalf("missing GitHub error:\n%s", view)
	}
	stale := prRow(failingPR)
	stale.GitHub.Error = "GitHub lookup failed: timeout"
	m.applySnapshot(app.Snapshot{Rows: []app.Row{stale}})
	if view := m.View().Content; !strings.Contains(view, "· checked 2m ago") {
		t.Fatalf("stale data not labelled:\n%s", view)
	}
	// A six-row card, the smallest, keeps its file row and drops the line.
	for h, want := range map[int]bool{6: false, 7: true} {
		card := strings.Join(m.selectedPreview(56, h), "\n")
		if strings.Contains(card, "PR #42") != want || !strings.Contains(card, "Working tree") {
			t.Fatalf("card of height %d:\n%s", h, card)
		}
	}
}

func TestDetailsShowPullRequestSection(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.clock = func() time.Time { return captureNow }
	earlier := app.PullRequest{Number: 40, State: app.PullRequestMerged, Base: "main", MergedAt: captureNow.Add(-48 * time.Hour)}
	m.applySnapshot(app.Snapshot{Rows: []app.Row{prRow(failingPR, earlier)}})
	m.EnableActions(app.NewActions(newActionFake(false), 1)) // read-only dashboards show no next step
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m.details = true
	view := m.View().Content
	for _, want := range []string{"Pull request · draft", "#42 Add tokens (draft)", "main ← feat/x · acme/widgets", "Checks failing · Changes requested · updated 3h ago",
		"https://github.com/acme/widgets/pull/42", "Earlier: #40 merged into main", "Checked 2m ago", "PR #42 checks failing", "PR #42 checks are failing on GitHub."} {
		if !strings.Contains(view, want) {
			t.Errorf("details lack %q", want)
		}
	}
	none := prRow()
	m.applySnapshot(app.Snapshot{Rows: []app.Row{none}})
	if view := m.View().Content; !strings.Contains(view, "none for feat/x on acme/widgets") {
		t.Fatalf("details without pull requests:\n%s", view)
	}
}

func TestGitHubLookupsAnnotateRowsAndKeepHighlight(t *testing.T) {
	pulls := &githubPulls{checks: "FAILURE"}
	m := New(context.Background(), func(context.Context) (app.Snapshot, error) { return app.Snapshot{Rows: githubRows()}, nil }, true)
	m.EnableGitHub(app.NewGitHub(githubGit{}, pulls, nil))
	m.spinning, m.ticking = true, true // keep drained commands free of timers
	m.attentionFirst = true
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
	drain(m, m.Init())
	if pulls.calls.Load() != 2 || m.githubPending != 0 { // one query per repository
		t.Fatalf("calls %d, pending %d", pulls.calls.Load(), m.githubPending)
	}
	if first := m.rows[m.visibleRows()[0]]; first.Name != "zeta" || first.GitHub == nil {
		t.Fatalf("failing checks did not raise zeta: %+v", first)
	}
	failing := m.github
	passing := app.NewGitHub(githubGit{}, &githubPulls{checks: "SUCCESS"}, nil)
	passing.LookupAll(context.Background(), githubRows())

	// Passing checks drop zeta below alpha; the highlight follows alpha.
	highlightPath(t, m, "/repos/alpha")
	m.github = passing
	m.applyGitHub()
	if row := m.highlightedRow(); row == nil || row.Path != "/repos/alpha" || m.highlight != 0 {
		t.Fatalf("highlight %d on %+v", m.highlight, row)
	}

	// Focus shows only zeta while its checks fail; once they pass it is
	// hidden, so it must not stay selected.
	m.github = failing
	m.applyGitHub()
	m.setScope(1)
	m.key(key(" "))
	if !m.selected["/repos/zeta"] {
		t.Fatal("zeta not selected in Focus")
	}
	m.github = passing
	m.applyGitHub()
	if len(m.selected) != 0 {
		t.Fatalf("a row hidden by new GitHub data stayed selected: %v", m.selected)
	}
}

func TestOnlyManualRefreshesForceGitHub(t *testing.T) {
	m := New(context.Background(), func(context.Context) (app.Snapshot, error) { return app.Snapshot{Rows: githubRows()}, nil }, true)
	m.EnableGitHub(app.NewGitHub(githubGit{}, &githubPulls{}, nil))
	m.SetAutoRefresh(time.Minute)
	m.Update(autoRefreshMsg{generation: m.autoGeneration})
	if m.githubForce {
		t.Fatal("automatic refresh forced a GitHub recheck")
	}
	m.key(key("r"))
	if !m.githubForce {
		t.Fatal("r did not force a GitHub recheck")
	}
	m.githubPending = 1
	m.width = 110
	if line := m.summaryLineAt(106); !strings.Contains(line, "refreshing") && !strings.Contains(line, "checking GitHub") {
		t.Fatalf("header: %q", line)
	}
	m.loading, m.refreshUntil = false, time.Time{}
	if line := m.summaryLineAt(106); !strings.Contains(line, "checking GitHub") {
		t.Fatalf("header without the GitHub activity: %q", line)
	}
	m.key(key("q"))
	if m.githubCtx.Err() == nil {
		t.Fatal("quitting left GitHub lookups running")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/tui -run 'GitHub|PullRequest|Manual' -v`
Expected: the build fails with `m.EnableGitHub undefined` (and the other new names).

- [ ] **Step 3: Implement**

`internal/tui/github.go`:
```go
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// githubMsg reports a finished background lookup. Its results are in the
// shared cache, which applyGitHub copies onto the rows.
type githubMsg struct{}

// EnableGitHub turns on pull request lookups through gh; without it the
// dashboard never runs gh.
func (m *Model) EnableGitHub(g *app.GitHub) {
	m.github = g
	m.githubCtx, m.githubCancel = context.WithCancel(m.ctx)
}

// forceGitHub makes the next snapshot recheck GitHub, at most every
// app.GitHubMinGap; automatic refresh never calls it.
func (m *Model) forceGitHub() { m.githubForce = true }

// githubLookups copies cached results onto the rows and starts the lookups
// that are due; each reports back with a githubMsg.
func (m *Model) githubLookups() tea.Cmd {
	if m.github == nil || m.closing {
		return nil
	}
	m.applyGitHub()
	force := m.githubForce
	m.githubForce = false
	var cmds []tea.Cmd
	for _, group := range m.github.Due(m.rows, force) {
		m.githubPending++
		ctx, g := m.githubCtx, m.github
		cmds = append(cmds, func() tea.Msg {
			g.Lookup(ctx, group)
			return githubMsg{}
		})
	}
	return tea.Batch(cmds...)
}

// applyGitHub annotates the rows, which can change attention and therefore
// order and Focus, then keeps the highlighted repository and drops selected
// rows the view no longer shows, as a refresh does.
func (m *Model) applyGitHub() {
	path := ""
	if row := m.highlightedRow(); row != nil {
		path = row.Path
	}
	m.github.Annotate(m.rows)
	visible := map[string]bool{}
	for _, index := range m.visibleRows() {
		if m.matchesView(m.rows[index]) {
			visible[m.rows[index].Path] = true
		}
	}
	for selected := range m.selected {
		if !visible[selected] {
			delete(m.selected, selected)
		}
	}
	for i, index := range m.visibleRows() {
		if m.rows[index].Path == path {
			m.highlight = i
			break
		}
	}
	m.keepHighlightVisible()
}

// openPullRequest is the row's current pull request when it is open.
func openPullRequest(row app.Row) *app.PullRequest {
	if pr := row.CurrentPullRequest(); pr != nil && pr.State == app.PullRequestOpen {
		return pr
	}
	return nil
}

// pullRequestState names a pull request's state, with its base once merged.
func pullRequestState(pr app.PullRequest) (string, string) {
	switch {
	case pr.State == app.PullRequestMerged:
		return "merged into " + gitcli.SafeText(pr.Base), success
	case pr.State == app.PullRequestClosed:
		return "closed", muted
	case pr.Draft:
		return "draft", muted
	}
	return "open", success
}

func checksLabel(checks string) (string, string) {
	switch checks {
	case app.ChecksFailing:
		return "checks failing", danger
	case app.ChecksPassing:
		return "checks passing", success
	case app.ChecksPending:
		return "checks pending", muted
	}
	return "", ""
}

func reviewLabel(review string) (string, string) {
	switch review {
	case app.ReviewChangesRequested:
		return "changes requested", amber
	case app.ReviewApproved:
		return "approved", success
	case app.ReviewRequired:
		return "review required", muted
	}
	return "", ""
}

// pullRequestAge is when a pull request last changed: its merge, else its
// latest update.
func (m *Model) pullRequestAge(pr app.PullRequest) string {
	if pr.State == app.PullRequestMerged && !pr.MergedAt.IsZero() {
		return relativeTime(m.now(), pr.MergedAt)
	}
	return relativeTime(m.now(), pr.UpdatedAt)
}

// hasPullRequestLine reports whether the preview card shows a pull request
// line for row: it has a pull request or a GitHub error to report.
func hasPullRequestLine(row app.Row) bool {
	return row.GitHub != nil && (row.CurrentPullRequest() != nil || row.GitHub.Error != "")
}

// pullRequestLine is the preview card's summary of the row's pull request,
// such as "PR #42 · open · checks failing · changes requested · 3h ago".
func (m *Model) pullRequestLine(row app.Row, w int) string {
	g, pr := row.GitHub, row.CurrentPullRequest()
	if g == nil || pr == nil && g.Error == "" {
		return ""
	}
	if pr == nil {
		return m.style(cell("GitHub: "+gitcli.SafeText(g.Error), w), amber, false)
	}
	dot := m.style(" · ", muted, false)
	state, color := pullRequestState(*pr)
	parts := []string{m.style(fmt.Sprintf("PR #%d", pr.Number), accent, true), m.style(state, color, false)}
	if pr.State == app.PullRequestOpen {
		if label, color := checksLabel(pr.Checks); label != "" {
			parts = append(parts, m.style(label, color, false))
		}
		if label, color := reviewLabel(pr.Review); label != "" {
			parts = append(parts, m.style(label, color, false))
		}
	}
	parts = append(parts, m.style(m.pullRequestAge(*pr), muted, false))
	if g.Error != "" {
		parts = append(parts, m.style("checked "+relativeTime(m.now(), g.CheckedAt), amber, false))
	}
	return cell(strings.Join(parts, dot), w)
}

// pullRequestLines is the details Overview section for the row's pull
// requests; nil when no lookup applies.
func (m *Model) pullRequestLines(row app.Row) []string {
	g := row.GitHub
	if g == nil {
		return nil
	}
	lines := []string{"", m.style(" Pull request", accent, true)}
	pr := row.CurrentPullRequest()
	if pr == nil {
		if g.Error != "" {
			return append(lines, m.style(" GitHub: "+gitcli.SafeText(g.Error), amber, false))
		}
		return append(lines, m.style(" none for "+gitcli.SafeText(g.Branch)+" on "+gitcli.SafeText(g.Repository), muted, false))
	}
	state, color := pullRequestState(*pr)
	lines[1] += m.style(" · "+state, color, false)
	title := fmt.Sprintf(" #%d %s", pr.Number, gitcli.SafeText(pr.Title))
	if pr.Draft {
		title += m.style(" (draft)", muted, false)
	}
	lines = append(lines, title, m.style(" "+gitcli.SafeText(pr.Base)+" ← "+gitcli.SafeText(g.Branch)+" · "+gitcli.SafeText(pr.BaseRepository), muted, false))
	var facts []string
	if label, _ := checksLabel(pr.Checks); label != "" {
		facts = append(facts, capitalize(label))
	}
	if label, _ := reviewLabel(pr.Review); label != "" {
		facts = append(facts, capitalize(label))
	}
	if pr.State == app.PullRequestMerged {
		facts = append(facts, "merged "+m.pullRequestAge(*pr))
	} else {
		facts = append(facts, "updated "+m.pullRequestAge(*pr))
	}
	lines = append(lines, " "+strings.Join(facts, " · "), m.style(" "+gitcli.SafeText(pr.URL), muted, false))
	if earlier := g.PullRequests[1:]; len(earlier) > 0 {
		var names []string
		for _, other := range earlier[:min(3, len(earlier))] {
			state, _ := pullRequestState(other)
			names = append(names, fmt.Sprintf("#%d %s", other.Number, state))
		}
		lines = append(lines, m.style(" Earlier: "+strings.Join(names, " · "), muted, false))
	}
	checked := " Checked " + relativeTime(m.now(), g.CheckedAt)
	if g.Error != "" {
		checked += " · GitHub: " + gitcli.SafeText(g.Error)
	}
	return append(lines, m.style(checked, muted, false))
}
```

`internal/tui/model.go`:
```diff
--- a/internal/tui/model.go
+++ b/internal/tui/model.go
@@ -96,6 +96,11 @@ type Model struct {
 	// refreshInterrupted records that an action cancelled an in-flight load,
 	// which must resume if the action ends without a batch to refresh after.
 	refreshInterrupted bool
+	github             *app.GitHub // nil: no pull request lookups
+	githubCtx          context.Context
+	githubCancel       context.CancelFunc
+	githubPending      int  // lookups in flight
+	githubForce        bool // the next snapshot rechecks GitHub
 }
 
 type snapshotMsg struct {
@@ -235,7 +240,7 @@ func (m *Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
 		}
 		// Every busy state starts through Update, so one check here keeps the
 		// spinner going without each start site scheduling it.
-		if (m.busy() || m.refreshing()) && !m.spinning && !m.closing {
+		if (m.busy() || m.refreshing() || m.githubPending > 0) && !m.spinning && !m.closing {
 			cmd = tea.Batch(cmd, m.spin())
 		}
 	}()
@@ -287,11 +292,16 @@ func (m *Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
 		} else {
 			m.loadErr = ""
 		}
-		next := m.scheduleAutoRefresh()
+		next := tea.Batch(m.scheduleAutoRefresh(), m.githubLookups())
 		if !m.ticking && !m.closing {
 			next = tea.Batch(next, m.tick())
 		}
 		return m, next
+	case githubMsg:
+		m.githubPending = max(0, m.githubPending-1)
+		if m.github != nil && !m.closing {
+			m.applyGitHub()
+		}
 	case autoRefreshMsg:
 		if msg.generation != m.autoGeneration || m.closing {
 			return m, nil
@@ -306,6 +316,7 @@ func (m *Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
 		} else {
 			m.message = "Child process finished"
 		}
+		m.forceGitHub()
 		return m, m.refresh()
 	case clockMsg:
 		if m.closing {
@@ -314,7 +325,7 @@ func (m *Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
 		return m, m.tick()
 	case spinnerMsg:
 		m.spinning = false
-		if !(m.busy() || m.refreshing()) || m.closing {
+		if !(m.busy() || m.refreshing() || m.githubPending > 0) || m.closing {
 			m.spinnerFrame = 0
 			return m, nil
 		}
@@ -498,6 +509,7 @@ func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
 		case "g":
 			return m.launchLazyGit()
 		case "r":
+			m.forceGitHub()
 			return m.refresh()
 		case "d":
 			m.detailTab = 1
@@ -605,6 +617,7 @@ func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
 		m.highlight, m.scroll = 0, 0
 	case "r":
 		m.message = ""
+		m.forceGitHub()
 		return m.refresh()
 	case "tab", "shift+tab":
 		return m.executeCommand("focus")
```

`internal/tui/view.go`:
```diff
--- a/internal/tui/view.go
+++ b/internal/tui/view.go
@@ -81,6 +81,9 @@ func (m *Model) layout() dashboardLayout {
 				}
 			}
 			desired = max(6, count+5)
+			if hasPullRequestLine(*row) {
+				desired++
+			}
 		}
 		// The card grows into rows the list does not need, keeping a margin
 		// below the list; a long list still leaves it a third of the screen.
@@ -167,6 +170,9 @@ func (m *Model) summaryLineAt(w int) string {
 		right += " " + m.chip(fmt.Sprintf("! %d attention", attention), color)
 	}
 	activity := ""
+	if m.githubPending > 0 {
+		activity = "checking GitHub"
+	}
 	if m.refreshing() {
 		activity = "refreshing"
 	}
@@ -338,7 +344,7 @@ func (m *Model) primaryStatus(row app.Row) (string, string) {
 	if w := row.Worktree; w != nil && w.Prunable {
 		return "◌ stale · directory missing", amber
 	}
-	state := lifecycleState(row)
+	state, open := lifecycleState(row), openPullRequest(row)
 	switch {
 	case s.Error != "":
 		return icons.failed + " failed", danger
@@ -374,6 +380,12 @@ func (m *Model) primaryStatus(row app.Row) (string, string) {
 		return fmt.Sprintf("%s %d %s", icons.ahead, s.Ahead, noun), accent
 	case s.Behind > 0:
 		return fmt.Sprintf("%s %d behind", icons.behind, s.Behind), amber
+	case open != nil && open.Checks == app.ChecksFailing:
+		return fmt.Sprintf("%s checks #%d", icons.failed, open.Number), amber
+	case open != nil && open.Review == app.ReviewChangesRequested:
+		return fmt.Sprintf("! changes #%d", open.Number), amber
+	case open != nil:
+		return fmt.Sprintf("%s PR #%d", icons.clean, open.Number), success
 	default:
 		return icons.clean + " clean", success
 	}
@@ -570,7 +582,7 @@ func syncLabel(row app.Row) (string, string) {
 }
 
 func nextStep(row app.Row) string {
-	s := row.Status
+	s, open := row.Status, openPullRequest(row)
 	switch {
 	case s.Error != "":
 		return "Status unavailable. Open diagnostics with d; refresh with r after resolving the error."
@@ -602,6 +614,10 @@ func nextStep(row app.Row) string {
 		return "Press p to review a push. Only committed changes are included."
 	case s.Dirty():
 		return "Review local changes in your shell or LazyGit. Tracking refs are up to date."
+	case open != nil && open.Checks == app.ChecksFailing:
+		return fmt.Sprintf("PR #%d checks are failing on GitHub.", open.Number)
+	case open != nil && open.Review == app.ReviewChangesRequested:
+		return fmt.Sprintf("Reviewers requested changes on PR #%d.", open.Number)
 	default:
 		return "Worktree is clean and locally known tracking refs are up to date. Select and fetch to check the remote."
 	}
```

`internal/tui/preview.go`:
```diff
--- a/internal/tui/preview.go
+++ b/internal/tui/preview.go
@@ -34,7 +34,11 @@ func (m *Model) selectedPreview(w, h int) []string {
 	if branchWidth > 3 {
 		header += "  " + m.style(branch, branchColor, false)
 	}
-	lines := []string{m.between(header, age, inner), m.previewHint(*row)}
+	lines := []string{m.between(header, age, inner)}
+	if pullRequestLineRows(*row, h) > 0 {
+		lines = append(lines, m.pullRequestLine(*row, inner))
+	}
+	lines = append(lines, m.previewHint(*row))
 	result, loaded := m.detailCache[row.Path]
 	split := inner >= 112 && loaded && len(result.data.Commits) > 0
 	leftWidth := inner
@@ -55,7 +59,7 @@ func (m *Model) selectedPreview(w, h int) []string {
 		head = cell(head, leftWidth) + "   " + m.style("Recent commits", muted, false)
 	}
 	lines = append(lines, head)
-	available := previewFileRows(h)
+	available := m.previewRows(*row, h)
 	fileRows := []string{}
 	switch {
 	case row.Status.Error != "":
@@ -145,7 +149,23 @@ func (m *Model) maxContextOffset() int {
 	if m.notice() != "" {
 		height--
 	}
-	return max(0, len(data.data.Files)-previewFileRows(height))
+	return max(0, len(data.data.Files)-m.previewRows(*row, height))
+}
+
+// previewRows is the room for changed files in row's card of height h, less
+// the pull request line when the card shows one.
+func (m *Model) previewRows(row app.Row, h int) int {
+	return previewFileRows(h) - pullRequestLineRows(row, h)
+}
+
+// pullRequestLineRows is 1 when row's card of height h shows its pull request
+// line, which needs a row of its own and still leaves one for a changed file;
+// shorter cards drop it before any file.
+func pullRequestLineRows(row app.Row, h int) int {
+	if hasPullRequestLine(row) && h >= 7 {
+		return 1
+	}
+	return 0
 }
 
 // previewFileRows is the room for changed files inside a card of height h:
```

`internal/tui/detail.go`:
```diff
--- a/internal/tui/detail.go
+++ b/internal/tui/detail.go
@@ -121,6 +121,7 @@ func (m *Model) repositoryDetails() (lines []string, patchAt int) {
 	}
 	switch m.detailTab {
 	case 0:
+		lines = append(lines, m.pullRequestLines(*row)...)
 		appendFiles(4)
 		appendCommits(3)
 	case 1:
@@ -346,4 +347,7 @@ func (m *Model) closeReads() {
 	if m.patchCancel != nil {
 		m.patchCancel()
 	}
+	if m.githubCancel != nil {
+		m.githubCancel()
+	}
 }
```

`internal/tui/palette.go`:
```diff
--- a/internal/tui/palette.go
+++ b/internal/tui/palette.go
@@ -151,6 +151,7 @@ func (m *Model) executeCommand(id string) tea.Cmd {
 	case "lazygit":
 		return m.launchLazyGit()
 	case "refresh":
+		m.forceGitHub()
 		return m.refresh()
 	case "help":
 		m.help = true
```

`internal/tui/actions.go`:
```diff
--- a/internal/tui/actions.go
+++ b/internal/tui/actions.go
@@ -135,6 +135,7 @@ func (m *Model) actionMessage(msg tea.Msg) (bool, tea.Cmd) {
 			m.message += " · " + msg.err.Error()
 		}
 		m.preview = nil
+		m.forceGitHub() // a push can open or update a pull request's checks
 		return true, m.refresh()
 	}
 	return false, nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui`
Expected: PASS. The existing render captures still match, because their fixtures have no GitHub data yet.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/tui
git commit -m "feat(tui): show pull requests in the dashboard"
```

---

### Task 10: Wire the dashboard, captures and phase 1 docs

**Files:**
- Modify: `cmd/gitperch/tui.go` (enable lookups when `newGitHub` succeeds)
- Modify: `internal/tui/capture_test.go` (design-system gets an open pull request)
- Regenerate: `docs/captures/*.txt` and `*.png`
- Modify: `docs/usage.md`, `docs/ui.md`, `README.md`, `docs/development.md`, `docs/plan.md`, `AGENTS.md`, `CONTRIBUTING.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes: `newGitHub` (Task 8), `Model.EnableGitHub` (Task 9).
- Produces: nothing new.

- [ ] **Step 1: Wire the dashboard**

`cmd/gitperch/tui.go`:
```diff
--- a/cmd/gitperch/tui.go
+++ b/cmd/gitperch/tui.go
@@ -41,6 +41,10 @@ func runTUI(ctx context.Context, cfg config.Config, ws config.Workspace, noColor
 	model.EnableCleanup(actions.CleanupSupported(ctx))
 	model.EnableDetails(read.Details)
 	model.EnablePatch(read.Patch)
+	// Without gh, Git 2.36 or [github] enabled, the dashboard never runs gh.
+	if gh, _, err := newGitHub(ctx, cfg, read); err == nil {
+		model.EnableGitHub(gh)
+	}
 	model.Configure(strings.Join(ws.Paths, ", "), cfg.UI.Icons, cfg.UI.DefaultFocus)
 	model.SetAutoRefresh(time.Duration(cfg.UI.RefreshSeconds) * time.Second)
 	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(out)}
```

- [ ] **Step 2: Give the capture fixture a pull request**

`internal/tui/capture_test.go`:
```diff
--- a/internal/tui/capture_test.go
+++ b/internal/tui/capture_test.go
@@ -24,6 +24,10 @@ func captureRows() []app.Row {
 			rows[i].Status.LastActivity = captureNow.Add(-age)
 		}
 	}
+	// design-system's branch has an open pull request with failing checks.
+	rows[1].GitHub = &app.GitHubInfo{Host: "github.com", Repository: "acme/design-system", Branch: "feat/tokens", CheckedAt: captureNow.Add(-2 * time.Minute),
+		PullRequests: []app.PullRequest{{Number: 128, Title: "Add semantic theme tokens", URL: "https://github.com/acme/design-system/pull/128", State: app.PullRequestOpen,
+			BaseRepository: "acme/design-system", Base: "main", IntoDefaultBranch: true, Checks: app.ChecksFailing, Review: app.ReviewChangesRequested, UpdatedAt: captureNow.Add(-time.Hour)}}}
 	// design-system owns two linked worktrees: one likely finished, one stale.
 	main := rows[1].Path
 	rows[1].Worktree = &app.WorktreeInfo{Main: true, MainPath: main}
```

- [ ] **Step 3: Regenerate and review the captures**

Run:
```sh
ansi=$(mktemp -d)
UPDATE_RENDERS=1 GITPERCH_ANSI_DIR="$ansi" go test ./internal/tui -run 'TestWorkspaceRenderCaptures|TestOverlayRenderCaptures|TestScanningRenderCapture'
/tmp/capture-venv/bin/python scripts/render-captures.py "$ansi"
git diff --stat docs/captures
```
Expected: `workspace-110x35`, `workspace-160x45`, `workspace-78x28`, `palette-110x35`, `worktrees-110x35` and `cleanup-110x35` gain the line `PR #128 · open · checks failing · changes requested · 1h ago` in the preview card. `details-110x35` gains the Pull request section and two attention reasons. `workspace-60x20` and `scanning-80x24` are unchanged: the smallest card drops the line, and the scan has no rows. Check that the 110×35 workspace capture reads:
```text
│ │ ▰ design-system  feat/tokens → origin/feat/tokens                                               2m ago │ │
│ │ PR #128 · open · checks failing · changes requested · 1h ago                                           │ │
│ │ ↑3 ahead · locally known refs                                                                          │ │
```

- [ ] **Step 4: Update the docs**

In `docs/usage.md`, replace:
```markdown
uses Git's no-optional-locks mode. JSON schema version 1 has deterministic repository
ordering, raw path identity, inspection timestamps, and per-repository errors.
```
with:
```markdown
uses Git's no-optional-locks mode; `--github` adds pull request state (see
[GitHub pull requests](#github-pull-requests)). JSON schema version 1 has deterministic repository
ordering, raw path identity, inspection timestamps, and per-repository errors.
```

In `docs/usage.md`, replace:
```markdown
| medium | `behind_upstream`, `no_upstream`, `tracking_unknown` (the upstream's local tracking ref is missing), `no_commits`, `stale_worktree`, `worktree_finished` and `worktree_idle` (a linked worktree's [lifecycle](#worktree-lifecycle)) |
```
with:
```markdown
| medium | `behind_upstream`, `no_upstream`, `tracking_unknown` (the upstream's local tracking ref is missing), `no_commits`, `stale_worktree`, `worktree_finished` and `worktree_idle` (a linked worktree's [lifecycle](#worktree-lifecycle)), and with [GitHub pull requests](#github-pull-requests) `pr_checks_failing` and `pr_changes_requested` (the branch's open pull request) |
```

In `docs/usage.md`, replace:
```markdown
`[]` at low) carry the same result. Attention describes Git state; it does not
decide what to do, and age alone never raises it: only a clean linked
```
with:
```markdown
`[]` at low) carry the same result. Attention describes Git state, plus the
branch's pull request when gh is available; it does not
decide what to do, and age alone never raises it: only a clean linked
```

In `docs/usage.md`, replace:
```markdown
## Authentication and limits
```
with:
````markdown
## GitHub pull requests

With the [GitHub CLI](https://cli.github.com) (`gh`) installed and logged in,
gitperch shows each branch's pull request next to its Git status. gh does all
authentication, GitHub Enterprise included; gitperch never reads a token. The
integration needs Git 2.36 or newer and is off without gh or with
`[github] enabled = false`:

```toml
[github]
enabled = true                  # default
hosts = ["github.example.com"]  # GitHub Enterprise Server hosts besides github.com
```

A branch is looked up when its upstream's remote URL names `github.com` (or
`www.github.com`, `ssh.github.com`) or a configured host. SSH host aliases
from `~/.ssh/config` are not resolved, and triangular setups, which push
somewhere other than the upstream, are not matched. gitperch reads the
upstream's remote and branch from Git and asks GitHub once per repository,
through `gh api graphql`, for the pull requests whose head is exactly that
branch of that repository, including pull requests from a fork into its
parent; the same branch name in someone else's fork never matches. gh runs
outside your repositories and sends GitHub the repository owners, names and
branch names.

The dashboard looks branches up in the background after each scan and reuses
a result for 5 minutes. `r`, returning from a shell or LazyGit, and a finished
batch recheck sooner, at most every 30 seconds per branch; automatic refresh
never forces a recheck. A failed lookup shows why, such as
`gh is not logged in to github.com — run gh auth login`, and keeps the
earlier result with its age. GitHub data never adds workspace warnings or
changes an exit code.

`gitperch status --github` looks every branch up before printing; without the
flag `status` contacts nothing. It exits 2 when GitHub use is disabled, gh is
missing or Git is older than 2.36. Failed lookups are reported per repository
and on one stderr line, and leave the exit code alone. The table's STATE
column adds markers such as `pr #42 open, checks failing`, and JSON adds a
`github` object per repository (schema version still 1):

| Field | Meaning |
| --- | --- |
| `host`, `repository`, `branch` | The upstream on GitHub, such as `github.com`, `acme/widgets` and `feat/x` |
| `pull_requests` | Up to five, open first, then the most recently updated: `number`, `title`, `url`, `state` (`open`, `merged`, `closed`), `draft`, `base_repository`, `base`, `into_default_branch`, `head_oid`, `review` (`approved`, `changes_requested`, `review_required`), `checks` (`passing`, `failing`, `pending`), `merged_at`, `updated_at` |
| `checked_at` | When the pull requests were read |
| `error` | Why the latest lookup failed, if it did |

gitperch never creates, merges, approves or comments on pull requests.

## Authentication and limits
````

In `docs/usage.md`, replace:
```markdown
The dashboard does not stage, commit, stash, reset, clean, rebase, resolve
conflicts, create branches, configure upstreams, or force push.
```
with:
```markdown
The dashboard does not stage, commit, stash, reset, clean, rebase, resolve
conflicts, create branches, configure upstreams, force push, or create, merge
or comment on pull requests.
```

In `docs/ui.md`, replace:
```markdown
## Worktrees in the list
```
with:
```markdown
## Pull requests

With gh available ([GitHub pull requests](usage.md#github-pull-requests)), a
branch whose Git state is otherwise quiet shows its pull request in the status
column: `× checks #42` and `! changes #42` in amber, and `✓ PR #42` for an open
pull request with nothing to do. The preview card adds a line under its header,
such as `PR #42 · open · checks failing · changes requested · 3h ago`, and drops
it before any changed file when the card is at its smallest. The details
Overview has a Pull request section with the title, `base ← head`, checks,
review, URL, up to three earlier pull requests of the branch and when GitHub
was asked. While lookups run, the header shows `checking GitHub`.

## Worktrees in the list
```

In `docs/ui.md`, replace:
```markdown
Agents, semantic agent messages/waiting states, tests, pull requests and tasks have
no real backend integration here, so no fabricated counters, columns, tabs or
buttons appear.
```
with:
```markdown
Agents, semantic agent messages/waiting states, tests and tasks have
no real backend integration here, so no fabricated counters, columns, tabs or
buttons appear. Pull requests come only from gh and are read-only: gitperch
never creates, merges or comments on them.
```

In `README.md`, replace:
```markdown
- **Scriptable status**: `gitperch status` prints a plain table or versioned
  JSON for scripts, CI and agent tooling.
```
with:
```markdown
- **Scriptable status**: `gitperch status` prints a plain table or versioned
  JSON for scripts, CI and agent tooling.
- **Pull requests** (optional, with [gh](https://cli.github.com)): each
  branch's pull request, review and CI checks in the dashboard and in
  `gitperch status --github`.
```

In `README.md`, replace:
````markdown
[ui]
icons = "unicode"     # or "ascii", "nerd"
refresh_seconds = 30  # automatic local status refresh; 0 turns it off
```
````
with:
````markdown
[ui]
icons = "unicode"     # or "ascii", "nerd"
refresh_seconds = 30  # automatic local status refresh; 0 turns it off

[github]
enabled = true        # use gh, when installed and logged in, for pull requests
```
````

In `docs/development.md`, replace:
```markdown
- Optional: [LazyGit](https://github.com/jesseduffield/lazygit) for the
  in-dashboard LazyGit launcher, and Python with `pillow` and `pyte` to
  regenerate PNG render captures.
```
with:
```markdown
- Optional: [LazyGit](https://github.com/jesseduffield/lazygit) for the
  in-dashboard LazyGit launcher, the [GitHub CLI](https://cli.github.com) for
  pull request status, and Python with `pillow` and `pyte` to regenerate PNG
  render captures.
```

In `docs/development.md`, replace:
```markdown
- `testdata/git-status` holds real porcelain v2 output; set
```
with:
```markdown
- Tests never run the real `gh` or contact GitHub (CI runners have gh
  installed): they write fake `gh` executables or use in-memory fakes.
- `testdata/git-status` holds real porcelain v2 output; set
```

In `docs/development.md`, replace:
```markdown
internal/git/        Git CLI runner, porcelain parsing and sync commands
```
with:
```markdown
internal/git/        Git CLI runner, porcelain parsing and sync commands
internal/github/     gh runner, remote URL parsing and pull request queries
```

In `docs/development.md`, replace:
```markdown
Domain logic stays independent of the TUI, and Git is always invoked through
argument arrays, never through a shell.
```
with:
```markdown
Domain logic stays independent of the TUI, and Git and gh are always invoked
through argument arrays, never through a shell.
```

In `docs/plan.md`, replace:
```markdown
> Everything else excluded here, including force removal, stays excluded.
```
with:
```markdown
> Everything else excluded here, including force removal, stays excluded.

> Update 2026-10-10: read-only pull request state through the GitHub CLI,
> and Clean up accepting a pull request merged with exactly the local commit,
> are in scope under
> [the GitHub CLI integration design](superpowers/specs/2026-10-10-github-cli-integration-design.md).
> Creating, merging or commenting on pull requests stays excluded.
```

In `AGENTS.md`, replace:
```markdown
- Invoke Git only through the runner in `internal/git` with argument arrays.
  Never interpolate into shell strings.
```
with:
```markdown
- Invoke Git only through the runner in `internal/git` with argument arrays.
  Never interpolate into shell strings.
- Invoke gh only through the runner in `internal/github`, with argument arrays
  and values in `-f` fields (never `-F`). gitperch only reads from GitHub.
  Tests use fake gh executables, never the real gh.
```

In `CONTRIBUTING.md`, replace:
```markdown
- **Git via argument arrays.** Never build shell command strings.
```
with:
```markdown
- **Git and gh via argument arrays.** Never build shell command strings.
```

In `CHANGELOG.md`, replace:
```markdown
## [Unreleased]
```
with:
```markdown
## [Unreleased]

### Added

- With the GitHub CLI (`gh`) installed and logged in, the dashboard shows each
  branch's pull request: open, draft, merged or closed, its review decision
  and CI checks. They appear in the status column (`× checks #42`,
  `! changes #42`, `✓ PR #42`), on a preview line and in a Pull request
  section of the details. Failing checks and requested changes raise medium
  attention (`pr_checks_failing`, `pr_changes_requested`). Lookups run in the
  background, are reused for five minutes and are never forced by automatic
  refresh. `[github] enabled = false` turns them off and `[github] hosts`
  adds GitHub Enterprise hosts.
- `gitperch status --github` adds the same data: a `github` object per
  repository in JSON and STATE markers in the table. Without the flag,
  `status` still contacts nothing.
```

- [ ] **Step 5: Verify and commit**

Run: `make check`, then `go run ./cmd/gitperch status --github .` from a checkout whose `origin` is on GitHub. With gh logged in, the STATE column of a branch with a pull request shows `pr #N …`. Without gh, the command exits 2 with `GitHub integration needs the gh CLI on PATH`.

```bash
make check
git add cmd/gitperch internal/tui docs README.md AGENTS.md CONTRIBUTING.md CHANGELOG.md
git commit -m "feat: show GitHub pull requests in the dashboard"
```

---

# Phase 2 — Merge evidence

### Task 11: Merge verdicts in the lifecycle and attention

**Files:**
- Modify: `internal/app/github.go` (`MergeVerdict`, `mergeVerdict`, `Row.MergedPullRequest`)
- Modify: `internal/app/lifecycle.go` (`SignalMergedPullRequest`, the merged switch, `mergeSignal`)
- Modify: `internal/app/attention.go` (`ReasonPRMerged`)
- Test: `internal/app/github_test.go`, `internal/app/lifecycle_test.go`

**Interfaces:**
- Consumes: `Row.GitHub`, `PullRequest` (Task 6); `shortOID` (`internal/app/cleanup.go`).
- Produces:
  ```go
  type MergeVerdict struct { PullRequest *PullRequest; Proven bool; Note string } // Task 13 adds Failed and RestoreFrom
  func mergeVerdict(prs []PullRequest, tip string) MergeVerdict
  func (r Row) MergedPullRequest() *PullRequest
  const SignalMergedPullRequest Signal = "merged_pull_request"
  const ReasonPRMerged Reason = "pr_merged" // medium, before worktree_finished
  func (r Row) mergeSignal() Signal
  ```

- [ ] **Step 1: Write the failing tests**

`internal/app/github_test.go`:
```diff
--- a/internal/app/github_test.go
+++ b/internal/app/github_test.go
@@ -247,3 +247,57 @@ func TestReportIncludesGitHubOnlyWhenAnnotated(t *testing.T) {
 		}
 	}
 }
+
+func TestMergeVerdict(t *testing.T) {
+	merged := func(number int, head string, intoDefault bool) PullRequest {
+		return PullRequest{Number: number, State: PullRequestMerged, Base: "main", IntoDefaultBranch: intoDefault, HeadOID: head}
+	}
+	release := merged(43, headA, false)
+	release.Base = "release"
+	other := strings.Repeat("1", 40)
+	for _, tc := range []struct {
+		name   string
+		prs    []PullRequest
+		number int
+		proven bool
+		note   string
+	}{
+		{"none", nil, 0, false, ""},
+		{"merged with the tip", []PullRequest{merged(42, headA, true)}, 42, true, ""},
+		{"an open one comes first", []PullRequest{{Number: 45, State: PullRequestOpen}, merged(42, headA, true)}, 45, false, "PR #45 is still open"},
+		{"an older merge proves it", []PullRequest{merged(44, other, true), merged(42, headA, true)}, 42, true, ""},
+		{"newer local commits", []PullRequest{merged(42, other, true)}, 42, false, "PR #42 merged at 1111111; this branch is at aaaaaaa"},
+		{"merged elsewhere", []PullRequest{release}, 43, false, "PR #43 merged into release, not the default branch"},
+		{"closed", []PullRequest{{Number: 41, State: PullRequestClosed}}, 41, false, "PR #41 was closed without merging"},
+	} {
+		v := mergeVerdict(tc.prs, headA)
+		number := 0
+		if v.PullRequest != nil {
+			number = v.PullRequest.Number
+		}
+		if number != tc.number || v.Proven != tc.proven || v.Note != tc.note {
+			t.Errorf("%s: %+v (PR %d)", tc.name, v, number)
+		}
+	}
+}
+
+func TestMergedPullRequestNeedsTheCheckedOutHead(t *testing.T) {
+	row := ghRow("/repo", "feat/a")
+	row.GitHub = &GitHubInfo{PullRequests: []PullRequest{{Number: 42, State: PullRequestMerged, IntoDefaultBranch: true, HeadOID: headA}}}
+	if pr := row.MergedPullRequest(); pr == nil || pr.Number != 42 {
+		t.Fatalf("merged: %+v", pr)
+	}
+	for name, edit := range map[string]func(*Row){
+		"detached":    func(r *Row) { r.Status.Detached, r.Status.Branch = true, "" },
+		"unborn":      func(r *Row) { r.Status.Unborn = true },
+		"moved on":    func(r *Row) { r.Status.HeadOID = strings.Repeat("b", 40) },
+		"no data":     func(r *Row) { r.GitHub = nil },
+		"no head oid": func(r *Row) { r.Status.HeadOID = "" },
+	} {
+		r := row
+		edit(&r)
+		if pr := r.MergedPullRequest(); pr != nil {
+			t.Errorf("%s: %+v", name, pr)
+		}
+	}
+}
```

`internal/app/lifecycle_test.go`:
```diff
--- a/internal/app/lifecycle_test.go
+++ b/internal/app/lifecycle_test.go
@@ -351,3 +351,50 @@ func TestLifecycleWithoutIntegrationSupport(t *testing.T) {
 		t.Fatalf("without integration: %+v %+v", row.Worktree.Integration, l)
 	}
 }
+
+func TestLifecycleAcceptsMergedPullRequest(t *testing.T) {
+	withPR := func(r Row) Row {
+		r.GitHub = &GitHubInfo{PullRequests: []PullRequest{{Number: 42, State: PullRequestMerged, Base: "main", IntoDefaultBranch: true, HeadOID: r.Status.HeadOID}}}
+		return r
+	}
+	quiet := withPR(linkedRow(6*day, notMerged))
+	l := quiet.Lifecycle()
+	if l.State != WorktreeFinished || !slices.Equal(l.Reasons, []Signal{SignalClean, SignalNothingToPush, SignalMergedPullRequest, SignalInactive}) {
+		t.Fatalf("squash-merged and quiet: %+v", l)
+	}
+	if got := quiet.DescribeSignal(SignalMergedPullRequest); got != "merged via PR #42 into main" {
+		t.Fatalf("describe: %q", got)
+	}
+	a := quiet.Attention()
+	if !slices.Equal(a.Reasons, []Reason{ReasonWorktreeFinished}) || !strings.Contains(quiet.Describe(ReasonWorktreeFinished), "merged via PR #42 into main") {
+		t.Fatalf("attention: %+v, %q", a, quiet.Describe(ReasonWorktreeFinished))
+	}
+	// GitHub deleted the remote branch after the merge.
+	gone := quiet
+	gone.Status.ComparisonKnown = false
+	if l := gone.Lifecycle(); l.State != WorktreeFinished || !slices.Equal(l.Reasons, []Signal{SignalClean, SignalMergedPullRequest, SignalInactive}) {
+		t.Fatalf("remote branch deleted: %+v", l)
+	}
+	// Recently active: not finished yet, but its pull request is merged.
+	busy := withPR(linkedRow(time.Hour, notMerged))
+	if l := busy.Lifecycle(); l.State != WorktreeActive {
+		t.Fatalf("active: %+v", l)
+	}
+	if a := busy.Attention(); !slices.Equal(a.Reasons, []Reason{ReasonPRMerged}) || busy.Describe(ReasonPRMerged) != "PR #42 merged into main; nothing newer on this branch" {
+		t.Fatalf("active attention: %+v", a)
+	}
+	// Git's own comparison wins when it shows the merge.
+	both := withPR(linkedRow(6*day, merged))
+	if l := both.Lifecycle(); !slices.Contains(l.Reasons, SignalMerged) || slices.Contains(l.Reasons, SignalMergedPullRequest) {
+		t.Fatalf("merged both ways: %+v", l)
+	}
+}
+
+func TestAttentionReportsMergedPullRequestOnMainWorktree(t *testing.T) {
+	r := Row{Status: tracking(0, 0)}
+	r.Status.Branch, r.Status.Upstream, r.Status.HeadOID = "feat", "origin/feat", strings.Repeat("c", 40)
+	r.GitHub = &GitHubInfo{PullRequests: []PullRequest{{Number: 9, State: PullRequestMerged, Base: "main", IntoDefaultBranch: true, HeadOID: r.Status.HeadOID}}}
+	if a := r.Attention(); a.Level != Medium || !slices.Equal(a.Reasons, []Reason{ReasonPRMerged}) {
+		t.Fatalf("attention: %+v", a)
+	}
+}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app -run 'MergeVerdict|MergedPullRequest|LifecycleAccepts' -v`
Expected: the build fails with `undefined: mergeVerdict`.

- [ ] **Step 3: Implement**

`internal/app/github.go`:
```diff
--- a/internal/app/github.go
+++ b/internal/app/github.go
@@ -3,6 +3,7 @@ package app
 import (
 	"context"
 	"errors"
+	"fmt"
 	"slices"
 	"sort"
 	"strings"
@@ -97,6 +98,64 @@ func (r Row) CurrentPullRequest() *PullRequest {
 	return &r.GitHub.PullRequests[0]
 }
 
+// MergeVerdict says whether GitHub proves a branch's work merged although Git
+// cannot show it: Proven when a pull request merged into its repository's
+// default branch had exactly the branch tip as its head. Otherwise Note
+// explains PullRequest, the pull request that decided; both are empty when
+// the branch has no pull requests.
+type MergeVerdict struct {
+	PullRequest *PullRequest
+	Proven      bool
+	Note        string
+}
+
+// mergeVerdict decides in this order: an open pull request (unproven: work
+// continues), a pull request merged into the default branch whose head is
+// tip (proven), another merged one (unproven, saying why), a closed one.
+func mergeVerdict(prs []PullRequest, tip string) MergeVerdict {
+	find := func(match func(PullRequest) bool) *PullRequest {
+		for i := range prs {
+			if match(prs[i]) {
+				return &prs[i]
+			}
+		}
+		return nil
+	}
+	if pr := find(func(p PullRequest) bool { return p.State == PullRequestOpen }); pr != nil {
+		return MergeVerdict{PullRequest: pr, Note: fmt.Sprintf("PR #%d is still open", pr.Number)}
+	}
+	if pr := find(func(p PullRequest) bool {
+		return p.State == PullRequestMerged && p.IntoDefaultBranch && p.HeadOID == tip
+	}); pr != nil {
+		return MergeVerdict{PullRequest: pr, Proven: true}
+	}
+	if pr := find(func(p PullRequest) bool { return p.State == PullRequestMerged }); pr != nil {
+		note := fmt.Sprintf("PR #%d merged into %s, not the default branch", pr.Number, gitcli.SafeText(pr.Base))
+		if pr.IntoDefaultBranch {
+			note = fmt.Sprintf("PR #%d merged at %s; this branch is at %s", pr.Number, shortOID(pr.HeadOID), shortOID(tip))
+		}
+		return MergeVerdict{PullRequest: pr, Note: note}
+	}
+	if pr := find(func(p PullRequest) bool { return p.State == PullRequestClosed }); pr != nil {
+		return MergeVerdict{PullRequest: pr, Note: fmt.Sprintf("PR #%d was closed without merging", pr.Number)}
+	}
+	return MergeVerdict{}
+}
+
+// MergedPullRequest is GitHub's proof that the checked-out branch is merged:
+// the proven pull request for HEAD, so everything on the branch went in
+// through it and nothing newer exists locally. Nil otherwise.
+func (r Row) MergedPullRequest() *PullRequest {
+	s := r.Status
+	if r.GitHub == nil || s.Detached || s.Unborn || s.Branch == "" || s.HeadOID == "" {
+		return nil
+	}
+	if v := mergeVerdict(r.GitHub.PullRequests, s.HeadOID); v.Proven {
+		return v.PullRequest
+	}
+	return nil
+}
+
 // GitHub lookup cadence; see GitHub.Due.
 const (
 	// GitHubTTL is how long a lookup's result is reused.
```

`internal/app/lifecycle.go`:
```diff
--- a/internal/app/lifecycle.go
+++ b/internal/app/lifecycle.go
@@ -1,6 +1,7 @@
 package app
 
 import (
+	"fmt"
 	"time"
 
 	gitcli "github.com/mark-lvl/gitperch/internal/git"
@@ -76,11 +77,14 @@ const (
 	SignalNothingToPush    Signal = "nothing_to_push"
 	SignalNoUpstream       Signal = "no_upstream"
 	SignalMerged           Signal = "merged"
-	SignalNotMerged        Signal = "not_merged"
-	SignalMergeUnknown     Signal = "merge_unknown"
-	SignalRecentActivity   Signal = "recent_activity"
-	SignalInactive         Signal = "inactive"
-	SignalActivityUnknown  Signal = "activity_unknown"
+	// SignalMergedPullRequest: Git cannot show HEAD merged (a squash or
+	// rebase merge), but GitHub reports it merged; see Row.MergedPullRequest.
+	SignalMergedPullRequest Signal = "merged_pull_request"
+	SignalNotMerged         Signal = "not_merged"
+	SignalMergeUnknown      Signal = "merge_unknown"
+	SignalRecentActivity    Signal = "recent_activity"
+	SignalInactive          Signal = "inactive"
+	SignalActivityUnknown   Signal = "activity_unknown"
 )
 
 // Lifecycle explains a linked worktree's State with the Signals it was
@@ -161,11 +165,14 @@ func (r Row) Lifecycle() *Lifecycle {
 	merged := false
 	switch in := w.Integration; {
 	case s.Unborn:
-	case in == nil || in.Error != "":
-		reasons = append(reasons, SignalMergeUnknown)
-	case in.Merged:
+	case in != nil && in.Error == "" && in.Merged:
 		merged = true
 		reasons = append(reasons, SignalMerged)
+	case r.MergedPullRequest() != nil:
+		merged = true
+		reasons = append(reasons, SignalMergedPullRequest)
+	case in == nil || in.Error != "":
+		reasons = append(reasons, SignalMergeUnknown)
 	default:
 		reasons = append(reasons, SignalNotMerged)
 	}
@@ -236,6 +243,11 @@ func (r Row) DescribeSignal(signal Signal) string {
 		return "nothing to push to " + gitcli.SafeText(s.Upstream)
 	case SignalMerged:
 		return "merged into " + base
+	case SignalMergedPullRequest:
+		if pr := r.MergedPullRequest(); pr != nil {
+			return fmt.Sprintf("merged via PR #%d into %s", pr.Number, gitcli.SafeText(pr.Base))
+		}
+		return "merged via a pull request"
 	case SignalNotMerged:
 		return "not merged into " + base
 	case SignalMergeUnknown:
@@ -255,6 +267,15 @@ func (r Row) DescribeSignal(signal Signal) string {
 	return gitcli.SafeText(string(signal))
 }
 
+// mergeSignal is the signal that shows the row merged: Git's own comparison
+// when it shows the merge, else GitHub's pull request.
+func (r Row) mergeSignal() Signal {
+	if in := r.Worktree.integration(); !(in.Merged && in.Error == "") && r.MergedPullRequest() != nil {
+		return SignalMergedPullRequest
+	}
+	return SignalMerged
+}
+
 func (w *WorktreeInfo) integration() Integration {
 	if w == nil || w.Integration == nil {
 		return Integration{}
```

`internal/app/attention.go`:
```diff
--- a/internal/app/attention.go
+++ b/internal/app/attention.go
@@ -81,6 +81,9 @@ const (
 	// branch's open pull request on GitHub (Row.GitHub): someone has to act.
 	ReasonPRChecksFailing    Reason = "pr_checks_failing"
 	ReasonPRChangesRequested Reason = "pr_changes_requested"
+	// ReasonPRMerged: GitHub merged the checked-out branch's pull request
+	// with exactly HEAD, so the branch is done; see Row.MergedPullRequest.
+	ReasonPRMerged Reason = "pr_merged"
 )
 
 // reasonOrder lists reasons from most to least severe; Attention.Reasons
@@ -89,7 +92,7 @@ var reasonOrder = []Reason{
 	ReasonActionFailed, ReasonInspectionFailed, ReasonConflicts, ReasonOperation,
 	ReasonDiverged, ReasonUncommitted, ReasonUntracked, ReasonUnpushed, ReasonDetachedCommits,
 	ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree,
-	ReasonWorktreeFinished, ReasonWorktreeIdle,
+	ReasonPRMerged, ReasonWorktreeFinished, ReasonWorktreeIdle,
 }
 
 // Level is the attention level the reason alone warrants.
@@ -99,7 +102,7 @@ func (r Reason) Level() Level {
 		return Critical
 	case ReasonDiverged, ReasonUncommitted, ReasonUntracked, ReasonUnpushed, ReasonDetachedCommits:
 		return High
-	case ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree, ReasonWorktreeFinished, ReasonWorktreeIdle:
+	case ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree, ReasonPRMerged, ReasonWorktreeFinished, ReasonWorktreeIdle:
 		return Medium
 	}
 	return Low
@@ -186,7 +189,8 @@ func (r Row) Attention() Attention {
 		}
 	}
 	// Lifecycle reads the same facts, never attention, so the two cannot loop.
-	if l := r.Lifecycle(); l != nil {
+	l := r.Lifecycle()
+	if l != nil {
 		switch l.State {
 		case WorktreeFinished:
 			a = a.With(ReasonWorktreeFinished)
@@ -194,6 +198,10 @@ func (r Row) Attention() Attention {
 			a = a.With(ReasonWorktreeIdle)
 		}
 	}
+	// A linked worktree that already looks finished says so once.
+	if r.MergedPullRequest() != nil && (l == nil || l.State != WorktreeFinished) {
+		a = a.With(ReasonPRMerged)
+	}
 	return a
 }
 
@@ -236,7 +244,7 @@ func (r Row) Describe(reason Reason) string {
 		return "stale worktree record"
 	case ReasonWorktreeFinished:
 		idle, _ := r.Inactivity()
-		return "linked worktree looks finished: " + r.DescribeSignal(SignalMerged) + ", no HEAD activity for " + span(idle)
+		return "linked worktree looks finished: " + r.DescribeSignal(r.mergeSignal()) + ", no HEAD activity for " + span(idle)
 	case ReasonWorktreeIdle:
 		idle, _ := r.Inactivity()
 		return "linked worktree idle: no HEAD activity for " + span(idle)
@@ -244,6 +252,11 @@ func (r Row) Describe(reason Reason) string {
 		return pullRequestName(r.CurrentPullRequest()) + " checks failing"
 	case ReasonPRChangesRequested:
 		return pullRequestName(r.CurrentPullRequest()) + ": changes requested"
+	case ReasonPRMerged:
+		if pr := r.MergedPullRequest(); pr != nil {
+			return fmt.Sprintf("PR #%d merged into %s; nothing newer on this branch", pr.Number, gitcli.SafeText(pr.Base))
+		}
+		return "pull request merged; nothing newer on this branch"
 	}
 	return gitcli.SafeText(string(reason))
 }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app`
Expected: PASS, including every existing lifecycle and attention test. Without GitHub data the lifecycle switch behaves exactly as before.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/app
git commit -m "feat(app): accept merged pull requests as merge evidence"
```

---

### Task 12: `✓ merged #42`, next steps and phase 2 docs

**Files:**
- Modify: `internal/tui/view.go` (`primaryStatus`, `nextStep`)
- Test: `internal/tui/github_test.go`
- Modify: `docs/usage.md`, `docs/ui.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes: `Row.MergedPullRequest` (Task 11).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

`internal/tui/github_test.go`:
```diff
--- a/internal/tui/github_test.go
+++ b/internal/tui/github_test.go
@@ -217,3 +217,27 @@ func TestOnlyManualRefreshesForceGitHub(t *testing.T) {
 		t.Fatal("quitting left GitHub lookups running")
 	}
 }
+
+func TestMergedPullRequestStatusAndNextStep(t *testing.T) {
+	head := strings.Repeat("c", 40)
+	row := prRow(app.PullRequest{Number: 42, State: app.PullRequestMerged, Base: "main", IntoDefaultBranch: true, HeadOID: head})
+	row.Status.HeadOID, row.Status.ComparisonKnown = head, false // GitHub deleted the remote branch
+	m := New(context.Background(), nil, true)
+	if label, _ := m.primaryStatus(row); label != "✓ merged #42" {
+		t.Fatalf("label %q", label)
+	}
+	if got := nextStep(row); got != "PR #42 is merged. Switch to main in your shell, then review the branch with Clean up (c)." {
+		t.Fatalf("main worktree: %q", got)
+	}
+	linked := row
+	linked.Worktree = &app.WorktreeInfo{Linked: true, MainPath: "/repos/main"}
+	linked.Status.InspectedAt, linked.Status.LastActivity = captureNow, captureNow.Add(-time.Hour)
+	if got := nextStep(linked); got != "PR #42 is merged into main. Review it with Clean up (c)." {
+		t.Fatalf("linked worktree: %q", got)
+	}
+	dirty := row
+	dirty.Status.Changes = 1
+	if label, _ := m.primaryStatus(dirty); label != "● changed" || strings.Contains(nextStep(dirty), "is merged") {
+		t.Fatalf("uncommitted changes come first: %q, %q", label, nextStep(dirty))
+	}
+}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tui -run TestMergedPullRequestStatusAndNextStep -v`
Expected: FAIL with `label "! unknown refs"`.

- [ ] **Step 3: Implement**

`internal/tui/view.go`:
```diff
--- a/internal/tui/view.go
+++ b/internal/tui/view.go
@@ -344,7 +344,7 @@ func (m *Model) primaryStatus(row app.Row) (string, string) {
 	if w := row.Worktree; w != nil && w.Prunable {
 		return "◌ stale · directory missing", amber
 	}
-	state, open := lifecycleState(row), openPullRequest(row)
+	state, open, merged := lifecycleState(row), openPullRequest(row), row.MergedPullRequest()
 	switch {
 	case s.Error != "":
 		return icons.failed + " failed", danger
@@ -364,6 +364,8 @@ func (m *Model) primaryStatus(row app.Row) (string, string) {
 		return icons.clean + " finished?", success
 	case state == app.WorktreeIdle:
 		return "idle " + idleDays(row), amber
+	case merged != nil:
+		return fmt.Sprintf("%s merged #%d", icons.clean, merged.Number), success
 	case s.Detached:
 		return "detached", muted
 	case s.Unborn:
@@ -582,7 +584,7 @@ func syncLabel(row app.Row) (string, string) {
 }
 
 func nextStep(row app.Row) string {
-	s, open := row.Status, openPullRequest(row)
+	s, open, merged := row.Status, openPullRequest(row), row.MergedPullRequest()
 	switch {
 	case s.Error != "":
 		return "Status unavailable. Open diagnostics with d; refresh with r after resolving the error."
@@ -600,6 +602,10 @@ func nextStep(row app.Row) string {
 		return "Looks finished. Press c to review removal; Clean up fetches and rechecks everything first."
 	case lifecycleState(row) == app.WorktreeIdle:
 		return "Idle and clean. Check whether its branch is still needed; Clean up (c) offers removal only once it is merged."
+	case merged != nil && !s.Dirty() && row.Worktree != nil && row.Worktree.Linked:
+		return fmt.Sprintf("PR #%d is merged into %s. Review it with Clean up (c).", merged.Number, gitcli.SafeText(merged.Base))
+	case merged != nil && !s.Dirty():
+		return fmt.Sprintf("PR #%d is merged. Switch to %s in your shell, then review the branch with Clean up (c).", merged.Number, gitcli.SafeText(merged.Base))
 	case s.Upstream == "":
 		return "Configure an upstream in your shell to compare and sync this branch."
 	case !s.ComparisonKnown:
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui`
Expected: PASS. The render captures are unchanged: no fixture row has a merged pull request.

- [ ] **Step 5: Update the docs**

In `docs/usage.md`, replace:
```markdown
| `likely_finished` | `✓ finished?` | Clean, nothing unpushed, HEAD merged into the default branch, and no HEAD activity for 24 hours |
```
with:
```markdown
| `likely_finished` | `✓ finished?` | Clean, nothing unpushed, HEAD merged into the default branch (or, with gh, through a pull request with exactly HEAD as its head), and no HEAD activity for 24 hours |
```

In `docs/usage.md`, replace:
```markdown
local refs without fetching, so a merge the local refs have not seen, and any
squash or rebase merge, reads as not merged.
```
with:
```markdown
local refs without fetching, so a merge the local refs have not seen, and any
squash or rebase merge, reads as not merged, unless gh shows the branch's pull
request merged into the default branch with exactly HEAD as its head (signal
`merged_pull_request`).
```

In `docs/usage.md`, replace:
```markdown
`nothing_to_push`, `no_upstream`, `merged`, `not_merged`, `merge_unknown`, then
```
with:
```markdown
`nothing_to_push`, `no_upstream`, `merged`, `merged_pull_request`, `not_merged`, `merge_unknown`, then
```

In `docs/usage.md`, replace:
```markdown
`pr_checks_failing` and `pr_changes_requested` (the branch's open pull request) |
```
with:
```markdown
`pr_checks_failing` and `pr_changes_requested` (the branch's open pull request), and `pr_merged` (its pull request merged with exactly HEAD while the branch is still checked out) |
```

In `docs/ui.md`, replace:
```markdown
column: `× checks #42` and `! changes #42` in amber, and `✓ PR #42` for an open
pull request with nothing to do.
```
with:
```markdown
column: `× checks #42` and `! changes #42` in amber, and `✓ PR #42` for an open
pull request with nothing to do. `✓ merged #42` marks a branch whose pull
request was merged with exactly its HEAD, which also explains a remote branch
GitHub deleted; a linked worktree like that reads `✓ finished?` once quiet.
```

In `CHANGELOG.md`, replace:
```markdown
- `gitperch status --github` adds the same data: a `github` object per
  repository in JSON and STATE markers in the table. Without the flag,
  `status` still contacts nothing.
```
with:
```markdown
- `gitperch status --github` adds the same data: a `github` object per
  repository in JSON and STATE markers in the table. Without the flag,
  `status` still contacts nothing.
- A branch whose pull request GitHub merged into the default branch with
  exactly the local commit counts as merged: a quiet linked worktree becomes
  `✓ finished?` after a squash or rebase merge, other branches show
  `✓ merged #42` and the `pr_merged` attention reason, and lifecycle JSON
  gains the `merged_pull_request` signal.
```

- [ ] **Step 6: Commit**

```bash
make check
git add internal/tui docs CHANGELOG.md
git commit -m "feat(tui): mark branches merged through pull requests"
```

---

# Phase 3 — Clean up for squash- and rebase-merged work

### Task 13: Squash-merged items in Clean up

**Files:**
- Modify: `internal/app/github.go` (`MergeVerdict.Failed`/`RestoreFrom`, `MergeEvidence`, `restoreSource`)
- Modify: `internal/app/assessment.go` (three codes)
- Modify: `internal/app/actions.go` (`Actions.github`)
- Modify: `internal/app/cleanup.go` (`SetGitHub`, item fields, planning, `assessIntegration`, revalidation, restore commands)
- Test: `internal/app/cleanup_github_test.go`

**Interfaces:**
- Consumes: `mergeVerdict` (Task 11); `GitHub.targets`, `query`, `slots`, `githubError` (Task 6); `fakeGitHubGit`, `fakePulls`, `upstream`, `headA` (Task 6 tests); `cleanupRepo`, `itemFor`, `branchItem`, `actionGit`, `actionTestWrite`, `actionTestCommit`, `execGitResult` (existing tests).
- Produces:
  ```go
  // MergeVerdict gains: Failed bool; RestoreFrom string
  func (g *GitHub) MergeEvidence(ctx context.Context, path string, branches []gitcli.Branch) map[string]MergeVerdict
  func restoreSource(remote, repository string, pr PullRequest) string
  const CleanupMergedPullRequest  CleanupCode = "merged_pull_request"   // allowed
  const CleanupPullRequestUnproven CleanupCode = "pull_request_unproven" // review
  const CleanupGitHubCheckFailed   CleanupCode = "github_check_failed"   // review
  // CleanupItem gains: PullRequest *PullRequest; RestoreFrom string
  func (a *Actions) SetGitHub(g *GitHub)
  // cleanupBase gains: verdicts map[string]MergeVerdict
  type mergeCheck struct{ merged bool; err error }
  func mergedViaText(pr PullRequest) string
  func verdictReason(v MergeVerdict) CleanupReason
  func reviewedVerdicts(item CleanupItem) map[string]MergeVerdict
  func pullRefFetch(item CleanupItem, repo string) string
  ```

- [ ] **Step 1: Write the failing tests**

`internal/app/cleanup_github_test.go`:
```go
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
)

// squashRepo is cleanupRepo plus a linked worktree on branch "squashed",
// pushed and then deleted from origin as GitHub does after a squash merge:
// Git sees the branch unmerged, with a gone upstream.
func squashRepo(t *testing.T) (repo, wt, tip string) {
	t.Helper()
	repo, _, _ = cleanupRepo(t)
	wt = filepath.Join(t.TempDir(), "squashed")
	actionGit(t, repo, "worktree", "add", "-b", "squashed", wt)
	actionTestWrite(t, filepath.Join(wt, "feature"), "work\n")
	actionTestCommit(t, wt, "squashed work")
	actionGit(t, wt, "push", "-u", "origin", "squashed")
	actionGit(t, repo, "push", "origin", "--delete", "squashed")
	return repo, wt, strings.TrimSpace(string(actionGit(t, wt, "rev-parse", "HEAD")))
}

// squashGitHub answers as GitHub would if origin were acme/widgets, with prs
// as the pull requests of every branch named in heads.
func squashGitHub(err error, prs map[string][]github.PullRequest) *GitHub {
	return NewGitHub(fakeGitHubGit{urls: map[string]string{"origin": "https://github.com/acme/widgets.git"}}, &fakePulls{prs: prs, err: err}, nil)
}

func mergedPR(number int, head string) github.PullRequest {
	return github.PullRequest{Number: number, State: "MERGED", BaseRef: "main", BaseRepository: "acme/widgets", IntoDefault: true, HeadOID: head,
		URL: fmt.Sprintf("https://github.com/acme/widgets/pull/%d", number)}
}

func TestCleanupRemovesSquashMergedWorktreeAndBranch(t *testing.T) {
	repo, wt, tip := squashRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"squashed": {mergedPR(42, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	worktree := itemFor(t, preview, RemoveWorktree, wt)
	if !worktree.Eligible() || worktree.PullRequest == nil || worktree.Assessment.Summary() != "merged via PR #42 into main on GitHub" {
		t.Fatalf("worktree: %+v", worktree)
	}
	branch := branchItem(preview, "squashed")
	if branch == nil || !branch.Eligible() || branch.RestoreFrom != "origin" || branch.PullRequest.Number != 42 {
		t.Fatalf("branch: %+v", branch)
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{worktree.ID, branch.ID}, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("results %+v, %v", results, err)
	}
	var restore string
	for _, r := range results {
		if r.State != Succeeded {
			t.Fatalf("result %+v", r)
		}
		if r.Item == branch.ID {
			restore = r.Restore
		}
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still present: %v", err)
	}
	if out, err := execGitResult(repo, "rev-parse", "--verify", "--quiet", "refs/heads/squashed"); err == nil {
		t.Fatalf("branch still exists at %s", out)
	}
	want := "git -C " + repo + " branch squashed " + tip + " || git -C " + repo + " fetch origin 'refs/pull/42/head:refs/heads/squashed'"
	if restore != want {
		t.Fatalf("restore %q, want %q", restore, want)
	}
}

func TestCleanupKeepsWorkGitHubCannotProveMerged(t *testing.T) {
	older := strings.Repeat("1", 40)
	release := mergedPR(43, "")
	release.BaseRef, release.IntoDefault = "release", false
	for name, tc := range map[string]struct {
		prs  func(tip string) []github.PullRequest
		err  error
		code CleanupCode
		note string
	}{
		"newer local commits": {func(string) []github.PullRequest { return []github.PullRequest{mergedPR(42, older)} }, nil, CleanupPullRequestUnproven, "PR #42 merged at 1111111; this branch is at "},
		"merged elsewhere": {func(tip string) []github.PullRequest {
			release.HeadOID = tip
			return []github.PullRequest{release}
		}, nil, CleanupPullRequestUnproven, "PR #43 merged into release, not the default branch"},
		"still open":       {func(string) []github.PullRequest { return []github.PullRequest{{Number: 45, State: "OPEN"}} }, nil, CleanupPullRequestUnproven, "PR #45 is still open"},
		"closed":           {func(string) []github.PullRequest { return []github.PullRequest{{Number: 41, State: "CLOSED"}} }, nil, CleanupPullRequestUnproven, "PR #41 was closed without merging"},
		"gh not logged in": {func(string) []github.PullRequest { return nil }, github.ErrNotAuthenticated, CleanupGitHubCheckFailed, "gh is not logged in to github.com — run gh auth login"},
	} {
		t.Run(name, func(t *testing.T) {
			repo, wt, tip := squashRepo(t)
			actions := NewActions(gitcli.Service{}, 1)
			actions.SetGitHub(squashGitHub(tc.err, map[string][]github.PullRequest{"squashed": tc.prs(tip)}))
			preview, err := actions.PlanCleanup(context.Background(), []string{repo})
			if err != nil {
				t.Fatal(err)
			}
			defer actions.Discard(preview.ID)
			worktree := itemFor(t, preview, RemoveWorktree, wt)
			if worktree.Eligible() || worktree.Assessment.Status != CleanupNeedsReview || !worktree.Assessment.Has(tc.code) || !strings.Contains(worktree.Assessment.Summary(), tc.note) {
				t.Fatalf("worktree: %+v", worktree.Assessment)
			}
			if b := branchItem(preview, "squashed"); b == nil || b.Eligible() || !b.Assessment.Has(tc.code) || !strings.Contains(b.Assessment.Summary(), "squash merge?") {
				t.Fatalf("branch: %+v", b)
			}
		})
	}
}

func TestCleanupMergeEvidenceNeverOverridesBlockers(t *testing.T) {
	repo, wt, tip := squashRepo(t)
	actionTestWrite(t, filepath.Join(wt, "scratch"), "agent notes\n")
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"squashed": {mergedPR(42, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	defer actions.Discard(preview.ID)
	worktree := itemFor(t, preview, RemoveWorktree, wt)
	if worktree.Eligible() || worktree.Assessment.Status != CleanupBlocked || !worktree.Assessment.Has(CleanupMergedPullRequest) {
		t.Fatalf("dirty worktree: %+v", worktree.Assessment)
	}
	// The branch stays checked out in the kept worktree.
	if b := branchItem(preview, "squashed"); b == nil || b.Eligible() || !b.Assessment.Has(CleanupCheckedOutElsewhere) {
		t.Fatalf("branch: %+v", b)
	}
}

func TestCleanupSkipsSquashMergedBranchThatMovedAfterReview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "checkout", "-b", "done")
	actionTestWrite(t, filepath.Join(repo, "done"), "work\n")
	actionTestCommit(t, repo, "done work")
	actionGit(t, repo, "push", "-u", "origin", "done")
	actionGit(t, repo, "checkout", "main")
	actionGit(t, repo, "push", "origin", "--delete", "done")
	tip := strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "done")))
	actions := NewActions(gitcli.Service{}, 1)
	actions.SetGitHub(squashGitHub(nil, map[string][]github.PullRequest{"done": {mergedPR(7, tip)}}))
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	b := branchItem(preview, "done")
	if b == nil || !b.Eligible() {
		t.Fatalf("branch: %+v", b)
	}
	actionGit(t, repo, "update-ref", "refs/heads/done", strings.TrimSpace(string(actionGit(t, repo, "rev-parse", "main"))))
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{b.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "moved since review") {
		t.Fatalf("results %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/done")
}

func TestMergeEvidenceAndRestoreSource(t *testing.T) {
	git := fakeGitHubGit{urls: map[string]string{"origin": "git@github.com:me/widgets.git", "lab": "https://gitlab.com/me/widgets.git"}}
	fork := github.PullRequest{Number: 7, State: "MERGED", BaseRef: "main", BaseRepository: "acme/widgets", IntoDefault: true, HeadOID: headA, URL: "https://github.com/acme/widgets/pull/7"}
	gh := NewGitHub(git, &fakePulls{prs: map[string][]github.PullRequest{"fix": {fork}}}, nil)
	verdicts := gh.MergeEvidence(context.Background(), "/repo", []gitcli.Branch{
		upstream("fix", "origin", "fix"), upstream("plain", "origin", "plain"), upstream("lab", "lab", "lab"), {Name: "local", OID: headA},
	})
	if v := verdicts["fix"]; len(verdicts) != 1 || !v.Proven || v.RestoreFrom != "https://github.com/acme/widgets" {
		t.Fatalf("verdicts %+v", verdicts)
	}
	failing := NewGitHub(git, &fakePulls{err: github.ErrRateLimited}, nil)
	if v := failing.MergeEvidence(context.Background(), "/repo", []gitcli.Branch{upstream("fix", "origin", "fix")})["fix"]; !v.Failed || v.Note != "GitHub API rate limit reached; gitperch checks again later" {
		t.Fatalf("failed lookup: %+v", v)
	}
	if got := restoreSource("origin", "acme/widgets", PullRequest{BaseRepository: "ACME/widgets"}); got != "origin" {
		t.Fatalf("same repository: %q", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app -run 'SquashMerged|GitHubCannotProve|NeverOverridesBlockers|MovedAfterReview|MergeEvidenceAndRestore' -v`
Expected: the build fails with `actions.SetGitHub undefined`.

- [ ] **Step 3: Implement**

`internal/app/github.go`:
```diff
--- a/internal/app/github.go
+++ b/internal/app/github.go
@@ -107,6 +107,11 @@ type MergeVerdict struct {
 	PullRequest *PullRequest
 	Proven      bool
 	Note        string
+	// Failed reports that gh could not answer; Note says why.
+	Failed bool
+	// RestoreFrom is where a proven branch's commits can be fetched again
+	// once deleted; see restoreSource.
+	RestoreFrom string
 }
 
 // mergeVerdict decides in this order: an open pull request (unproven: work
@@ -156,6 +161,68 @@ func (r Row) MergedPullRequest() *PullRequest {
 	return nil
 }
 
+// MergeEvidence asks GitHub now, bypassing the cache, for a verdict on each
+// branch whose upstream is on GitHub. A failed lookup gives that repository's
+// branches Failed verdicts; branches without pull requests get none.
+func (g *GitHub) MergeEvidence(ctx context.Context, path string, branches []gitcli.Branch) map[string]MergeVerdict {
+	verdicts := map[string]MergeVerdict{}
+	targets := g.targets(ctx, path, branches)
+	heads := map[github.Repo][]string{}
+	for _, t := range targets {
+		heads[t.repo] = append(heads[t.repo], t.head)
+	}
+	if len(heads) == 0 {
+		return verdicts
+	}
+	select {
+	case g.slots <- struct{}{}:
+	case <-ctx.Done():
+		return verdicts
+	}
+	defer func() { <-g.slots }()
+	type answer struct {
+		name string
+		prs  map[string][]PullRequest
+		err  error
+	}
+	answers := map[github.Repo]answer{}
+	for repo, list := range heads {
+		name, prs, err := g.query(ctx, repo, list)
+		answers[repo] = answer{name, prs, err}
+	}
+	for _, b := range branches {
+		t, ok := targets[b.Name]
+		if !ok {
+			continue
+		}
+		a := answers[t.repo]
+		if a.err != nil {
+			verdicts[b.Name] = MergeVerdict{Failed: true, Note: githubError(t.repo, a.err)}
+			continue
+		}
+		v := mergeVerdict(a.prs[t.head], b.OID)
+		if v.PullRequest == nil {
+			continue
+		}
+		if v.Proven {
+			v.RestoreFrom = restoreSource(t.remote, a.name, *v.PullRequest)
+		}
+		verdicts[b.Name] = v
+	}
+	return verdicts
+}
+
+// restoreSource is where a deleted squash-merged branch's commits can be
+// fetched again from GitHub's pull request ref: the upstream remote when the
+// pull request lives in its repository, else the base repository's URL, as
+// for a fork's pull request into its parent.
+func restoreSource(remote, repository string, pr PullRequest) string {
+	if strings.EqualFold(pr.BaseRepository, repository) {
+		return remote
+	}
+	return strings.TrimSuffix(pr.URL, fmt.Sprintf("/pull/%d", pr.Number))
+}
+
 // GitHub lookup cadence; see GitHub.Due.
 const (
 	// GitHubTTL is how long a lookup's result is reused.
```

`internal/app/assessment.go`:
```diff
--- a/internal/app/assessment.go
+++ b/internal/app/assessment.go
@@ -49,11 +49,19 @@ const (
 	CleanupNoHiddenChanges  CleanupCode = "no_hidden_changes"
 	CleanupMerged           CleanupCode = "merged"
 	CleanupDirectoryMissing CleanupCode = "directory_missing"
+	// CleanupMergedPullRequest: GitHub merged a pull request into the default
+	// branch whose head is exactly the item's commit; see mergeVerdict.
+	CleanupMergedPullRequest CleanupCode = "merged_pull_request"
 
 	// Facts that need the user's own review.
 	CleanupNotMerged    CleanupCode = "not_merged"
 	CleanupUpstreamGone CleanupCode = "upstream_gone"
 	CleanupNoUpstream   CleanupCode = "no_upstream"
+	// CleanupPullRequestUnproven: the branch's pull request does not prove it
+	// merged, such as one still open or merged at an older commit.
+	CleanupPullRequestUnproven CleanupCode = "pull_request_unproven"
+	// CleanupGitHubCheckFailed: gh could not answer; Git alone decides.
+	CleanupGitHubCheckFailed CleanupCode = "github_check_failed"
 
 	// Checks that could not be made.
 	CleanupInspectionFailed     CleanupCode = "inspection_failed"
@@ -82,9 +90,9 @@ const (
 // directory is missing stays blocked through its lock.
 func (c CleanupCode) Status() CleanupStatus {
 	switch c {
-	case CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupMerged, CleanupDirectoryMissing:
+	case CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupMerged, CleanupDirectoryMissing, CleanupMergedPullRequest:
 		return CleanupAllowed
-	case CleanupNotMerged, CleanupUpstreamGone, CleanupNoUpstream:
+	case CleanupNotMerged, CleanupUpstreamGone, CleanupNoUpstream, CleanupPullRequestUnproven, CleanupGitHubCheckFailed:
 		return CleanupNeedsReview
 	case CleanupInspectionFailed, CleanupCheckFailed, CleanupFetchFailed, CleanupDefaultBranchUnknown:
 		return CleanupUnknown
```

`internal/app/actions.go`:
```diff
--- a/internal/app/actions.go
+++ b/internal/app/actions.go
@@ -90,6 +90,7 @@ type Actions struct {
 	lastFetch      map[string]time.Time
 	restoreLog     string     // appended on branch deletion; "" disables
 	logMu          sync.Mutex // serializes appends from concurrent groups
+	github         *GitHub    // merge evidence for Clean up; nil without gh
 }
 
 func NewActions(service ActionGit, workers int) *Actions {
```

`internal/app/cleanup.go`:
```diff
--- a/internal/app/cleanup.go
+++ b/internal/app/cleanup.go
@@ -36,6 +36,11 @@ type CleanupItem struct {
 	Base       string   // e.g. refs/remotes/origin/main
 	BaseName   string   // e.g. origin/main, or "main (local default)"
 	Assessment CleanupAssessment
+	// PullRequest is GitHub's evidence when the item is allowed because a
+	// pull request merged it; nil otherwise. RestoreFrom is where a deleted
+	// branch's commits can then be fetched again.
+	PullRequest *PullRequest
+	RestoreFrom string
 }
 
 // Eligible reports whether the item may run: every check passed.
@@ -81,6 +86,20 @@ type CleanupGit interface {
 
 var ErrCleanupUnsupported = errors.New("worktree cleanup needs Git 2.36 or newer")
 
+// SetGitHub lets Clean up accept GitHub's evidence that a pull request merged
+// a branch Git cannot show merged (squash and rebase merges); nil turns it off.
+func (a *Actions) SetGitHub(g *GitHub) {
+	a.mu.Lock()
+	a.github = g
+	a.mu.Unlock()
+}
+
+func (a *Actions) gitHub() *GitHub {
+	a.mu.Lock()
+	defer a.mu.Unlock()
+	return a.github
+}
+
 // CleanupSupported lets the TUI hide Clean up on older Git.
 func (a *Actions) CleanupSupported(ctx context.Context) bool {
 	git, ok := a.service.(CleanupGit)
@@ -96,6 +115,14 @@ type cleanupBase struct {
 	ref, name string
 	reason    CleanupReason // why ref is empty
 	branch    string        // raw default branch name, never shown; skipped when deleting branches
+	// verdicts holds GitHub's merge evidence by branch name; nil without gh.
+	verdicts map[string]MergeVerdict
+}
+
+// mergeCheck is one branch's ancestry check against the default ref.
+type mergeCheck struct {
+	merged bool
+	err    error
 }
 
 func kindOrder(k CleanupKind) int {
@@ -209,6 +236,10 @@ func (a *Actions) PlanCleanup(ctx context.Context, paths []string) (CleanupPrevi
 		preview.Items[i] = p.item
 		preview.Items[i].Stale = slices.Clone(p.item.Stale)
 		preview.Items[i].Assessment.Reasons = slices.Clone(p.item.Assessment.Reasons)
+		if pr := p.item.PullRequest; pr != nil {
+			copied := *pr
+			preview.Items[i].PullRequest = &copied
+		}
 	}
 	return preview, nil
 }
@@ -300,6 +331,28 @@ func (a *Actions) planCleanupGroup(ctx context.Context, git CleanupGit, path str
 	if base.ref == "" && base.reason.Code != "" {
 		add(CleanupItem{Kind: CleanupGroup, Path: group, Assessment: Assess(base.reason)})
 	}
+	// Branches are read before worktrees, so one GitHub question about every
+	// unmerged branch with an upstream serves both.
+	var branches []gitcli.Branch
+	var branchErr error
+	ancestry := map[string]mergeCheck{}
+	if base.ref != "" {
+		branches, branchErr = git.LocalBranches(ctx, path)
+		var candidates []gitcli.Branch
+		for _, b := range branches {
+			if b.Name == base.branch || b.Symref != "" {
+				continue // the default branch, or an alias whose deletion Git must never follow
+			}
+			merged, err := git.IsAncestor(ctx, path, b.OID, base.ref)
+			ancestry[b.Name] = mergeCheck{merged, err}
+			if err == nil && !merged && b.Remote != "" {
+				candidates = append(candidates, b)
+			}
+		}
+		if gh := a.gitHub(); gh != nil && len(candidates) > 0 {
+			base.verdicts = gh.MergeEvidence(ctx, path, candidates)
+		}
+	}
 	var stale []string
 	for _, wt := range worktrees {
 		if wt.Main {
@@ -317,6 +370,9 @@ func (a *Actions) planCleanupGroup(ctx context.Context, git CleanupGit, path str
 		}
 		item := CleanupItem{Kind: RemoveWorktree, Path: wt.Path, Branch: wt.Branch, OID: wt.HeadOID}
 		item.Assessment = assessWorktree(ctx, git, wt, base, nil)
+		if item.Assessment.Has(CleanupMergedPullRequest) {
+			item.PullRequest = base.verdicts[wt.Branch].PullRequest
+		}
 		add(item)
 	}
 	pruned := false
@@ -347,38 +403,65 @@ func (a *Actions) planCleanupGroup(ctx context.Context, git CleanupGit, path str
 		checkedOut[wt.Branch] = wt.Path
 	}
 	busy := operationBlocker(ctx, git, worktrees)
-	branches, err := git.LocalBranches(ctx, path)
-	if err != nil {
-		add(CleanupItem{Kind: CleanupGroup, Path: group, Assessment: Assess(CleanupReason{Code: CleanupCheckFailed, Text: "branch list failed: " + gitcli.SafeText(err.Error())})})
+	if branchErr != nil {
+		add(CleanupItem{Kind: CleanupGroup, Path: group, Assessment: Assess(CleanupReason{Code: CleanupCheckFailed, Text: "branch list failed: " + gitcli.SafeText(branchErr.Error())})})
 		return out
 	}
 	for _, b := range branches {
-		if b.Name == base.branch || b.Symref != "" {
+		check, ok := ancestry[b.Name]
+		if !ok {
 			continue // the default branch, or an alias whose deletion Git must never follow
 		}
-		merged, err := git.IsAncestor(ctx, path, b.OID, base.ref)
+		v := base.verdicts[b.Name]
+		proven := v.Proven && v.PullRequest.HeadOID == b.OID
 		item := CleanupItem{Kind: DeleteBranch, Path: group, Branch: b.Name, OID: b.OID}
-		var reason CleanupReason
+		var reasons []CleanupReason
 		switch {
-		case err != nil:
-			reason = CleanupReason{Code: CleanupCheckFailed, Text: "merge check failed: " + gitcli.SafeText(err.Error())}
-		case merged && busy.Code != "":
-			reason = busy
-		case merged && checkedOut[b.Name] != "":
-			reason = CleanupReason{Code: CleanupCheckedOutElsewhere, Text: "merged but checked out in " + gitcli.SafeText(filepath.Base(checkedOut[b.Name]))}
-		case merged:
-			reason = CleanupReason{Code: CleanupMerged, Text: "merged into " + base.name}
+		case check.err != nil:
+			reasons = append(reasons, CleanupReason{Code: CleanupCheckFailed, Text: "merge check failed: " + gitcli.SafeText(check.err.Error())})
+		case (check.merged || proven) && busy.Code != "":
+			reasons = append(reasons, busy)
+		case (check.merged || proven) && checkedOut[b.Name] != "":
+			reasons = append(reasons, CleanupReason{Code: CleanupCheckedOutElsewhere, Text: "merged but checked out in " + gitcli.SafeText(filepath.Base(checkedOut[b.Name]))})
+		case check.merged:
+			reasons = append(reasons, CleanupReason{Code: CleanupMerged, Text: "merged into " + base.name})
+		case proven:
+			reasons = append(reasons, CleanupReason{Code: CleanupMergedPullRequest, Text: mergedViaText(*v.PullRequest)})
+			item.PullRequest, item.RestoreFrom = v.PullRequest, v.RestoreFrom
 		case b.Gone:
-			reason = CleanupReason{Code: CleanupUpstreamGone, Text: "upstream gone but not merged into " + base.name + " — squash merge?"}
+			reasons = append(reasons, CleanupReason{Code: CleanupUpstreamGone, Text: "upstream gone but not merged into " + base.name + " — squash merge?"})
+			if note := verdictReason(v); note.Code != "" {
+				reasons = append(reasons, note)
+			}
+		case v.PullRequest != nil && v.PullRequest.State == PullRequestMerged:
+			// Merged on GitHub, but not with this tip: worth the user's review.
+			reasons = append(reasons, CleanupReason{Code: CleanupNotMerged, Text: "not merged into " + base.name}, verdictReason(v))
 		default:
 			continue // ordinary unmerged branch: not a cleanup candidate
 		}
-		item.Assessment = Assess(reason)
+		item.Assessment = Assess(reasons...)
 		add(item)
 	}
 	return out
 }
 
+// mergedViaText states GitHub's merge evidence for an item.
+func mergedViaText(pr PullRequest) string {
+	return fmt.Sprintf("merged via PR #%d into %s on GitHub", pr.Number, gitcli.SafeText(pr.Base))
+}
+
+// verdictReason is the review reason a GitHub verdict adds to an unmerged
+// item: why its pull request proves nothing, or why GitHub could not answer.
+func verdictReason(v MergeVerdict) CleanupReason {
+	switch {
+	case v.Failed:
+		return CleanupReason{Code: CleanupGitHubCheckFailed, Text: v.Note}
+	case !v.Proven && v.Note != "":
+		return CleanupReason{Code: CleanupPullRequestUnproven, Text: v.Note}
+	}
+	return CleanupReason{}
+}
+
 // staleNames lists up to three stale directory names for the review.
 func staleNames(stale []string) string {
 	var names []string
@@ -605,7 +688,18 @@ func assessIntegration(ctx context.Context, git CleanupGit, path string, s repos
 		as.add(CleanupMerged, "merged into "+base.name)
 		return
 	}
-	as.add(CleanupNotMerged, "not merged into "+base.name)
+	// GitHub's evidence replaces only "not merged" and "upstream gone": a
+	// squash merge explains both. Every other check below still applies.
+	v := base.verdicts[s.Branch]
+	proven := !s.Detached && v.Proven && v.PullRequest.HeadOID == s.HeadOID
+	if proven {
+		as.add(CleanupMergedPullRequest, mergedViaText(*v.PullRequest))
+	} else {
+		as.add(CleanupNotMerged, "not merged into "+base.name)
+		if note := verdictReason(v); note.Code != "" && !s.Detached {
+			as.add(note.Code, note.Text)
+		}
+	}
 	upstream := gitcli.SafeText(s.Upstream)
 	switch {
 	case s.Detached:
@@ -620,7 +714,9 @@ func assessIntegration(ctx context.Context, git CleanupGit, path string, s repos
 	case s.Upstream == "":
 		as.add(CleanupNoUpstream, "branch has no upstream")
 	case !s.ComparisonKnown:
-		as.add(CleanupUpstreamGone, "upstream "+upstream+" is gone — squash merge?")
+		if !proven {
+			as.add(CleanupUpstreamGone, "upstream "+upstream+" is gone — squash merge?")
+		}
 	case s.Ahead > 0 && s.Behind > 0:
 		as.add(CleanupDiverged, fmt.Sprintf("diverged from %s: %d ahead, %d behind", upstream, s.Ahead, s.Behind))
 	case s.Ahead > 0:
@@ -795,7 +891,27 @@ func deletedBranchMessage(item CleanupItem) string {
 // restoreCommand recreates a deleted branch at its reviewed commit, run from
 // inside the repository.
 func restoreCommand(item CleanupItem) string {
-	return fmt.Sprintf("git branch %s %s", shellQuote(gitcli.SafeText(item.Branch)), item.OID)
+	command := fmt.Sprintf("git branch %s %s", shellQuote(gitcli.SafeText(item.Branch)), item.OID)
+	if fetch := pullRefFetch(item, ""); fetch != "" {
+		command += " || " + fetch
+	}
+	return command
+}
+
+// pullRefFetch fetches a squash-merged branch back from GitHub's permanent
+// pull request ref, for when git gc has dropped its unreachable commits. It
+// is empty for a branch merged into the default branch, whose commits stay
+// reachable. repo names the repository for use from any directory.
+func pullRefFetch(item CleanupItem, repo string) string {
+	if item.PullRequest == nil || item.RestoreFrom == "" {
+		return ""
+	}
+	git := "git"
+	if repo != "" {
+		git += " -C " + shellQuote(repo)
+	}
+	refspec := fmt.Sprintf("refs/pull/%d/head:refs/heads/%s", item.PullRequest.Number, gitcli.SafeText(item.Branch))
+	return fmt.Sprintf("%s fetch %s %s", git, shellQuote(gitcli.SafeText(item.RestoreFrom)), shellQuote(refspec))
 }
 
 // recreateCommand adds a removed worktree back at its reviewed path, on its
@@ -827,12 +943,24 @@ func recreateCommand(item CleanupItem) string {
 func restoreAnywhere(item CleanupItem) string {
 	repo, branch := gitcli.SafeText(item.Group), gitcli.SafeText(item.Branch)
 	command := fmt.Sprintf("git -C %s branch %s %s", shellQuote(repo), shellQuote(branch), item.OID)
+	if fetch := pullRefFetch(item, repo); fetch != "" {
+		command += " || " + fetch
+	}
 	if repo != item.Group || branch != item.Branch {
 		command += "  # names shown escaped; inside the repository run: git branch <name> " + item.OID
 	}
 	return command
 }
 
+// reviewedVerdicts is the evidence an item was reviewed with; revalidation
+// never asks GitHub again.
+func reviewedVerdicts(item CleanupItem) map[string]MergeVerdict {
+	if item.PullRequest == nil {
+		return nil
+	}
+	return map[string]MergeVerdict{item.Branch: {PullRequest: item.PullRequest, Proven: true}}
+}
+
 // revalidateCleanup reassesses an item immediately before it runs, against
 // the state the user reviewed. Anything that changed since keeps the item; it
 // is never re-planned silently.
@@ -868,7 +996,7 @@ func revalidateCleanup(ctx context.Context, git CleanupGit, item CleanupItem) Cl
 			if !wt.Main && isStale(wt) {
 				return changed("worktree directory went missing since review")
 			}
-			return assessWorktree(ctx, git, wt, cleanupBase{ref: item.Base, name: item.BaseName}, &item)
+			return assessWorktree(ctx, git, wt, cleanupBase{ref: item.Base, name: item.BaseName, verdicts: reviewedVerdicts(item)}, &item)
 		}
 		return changed("worktree no longer exists")
 	case DeleteBranch:
@@ -897,6 +1025,14 @@ func revalidateCleanup(ctx context.Context, git CleanupGit, item CleanupItem) Cl
 			if b.OID != item.OID {
 				return changed("branch moved since review")
 			}
+			if pr := item.PullRequest; pr != nil {
+				// GitHub is not asked again: a merged pull request stays
+				// merged, and update-ref deletes only at the reviewed commit.
+				if pr.HeadOID != b.OID {
+					return changed("branch moved since review")
+				}
+				return Assess(CleanupReason{Code: CleanupMergedPullRequest, Text: mergedViaText(*pr)})
+			}
 			merged, err := git.IsAncestor(ctx, item.Group, b.OID, item.Base)
 			if err != nil {
 				return failed("merge check", err)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app -run 'Cleanup|MergeEvidence|Restore' -v`, then `go test -race ./internal/app`
Expected: PASS. Every existing cleanup test passes unchanged: without `SetGitHub` there are no verdicts, and branches are still listed and checked against the default ref once each, only before the worktrees instead of after.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/app
git commit -m "feat(app): clean up squash-merged worktrees and branches"
```

---

### Task 14: Wire Clean up and document the policy

**Files:**
- Modify: `cmd/gitperch/tui.go` (`actions.SetGitHub`)
- Modify: `docs/usage.md`, `README.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes: `Actions.SetGitHub` (Task 13), `newGitHub` (Task 8).
- Produces: nothing new.

- [ ] **Step 1: Wire it**

`cmd/gitperch/tui.go`:
```diff
--- a/cmd/gitperch/tui.go
+++ b/cmd/gitperch/tui.go
@@ -44,6 +44,7 @@ func runTUI(ctx context.Context, cfg config.Config, ws config.Workspace, noColor
 	// Without gh, Git 2.36 or [github] enabled, the dashboard never runs gh.
 	if gh, _, err := newGitHub(ctx, cfg, read); err == nil {
 		model.EnableGitHub(gh)
+		actions.SetGitHub(gh)
 	}
 	model.Configure(strings.Join(ws.Paths, ", "), cfg.UI.Icons, cfg.UI.DefaultFocus)
 	model.SetAutoRefresh(time.Duration(cfg.UI.RefreshSeconds) * time.Second)
```

- [ ] **Step 2: Update the docs**

In `docs/usage.md`, replace:
```markdown
| `review` | Nothing at risk was found, but Git cannot show the work is finished: not merged into the default branch (squash and rebase merges look like this), no upstream, or an upstream that is gone |
```
with:
```markdown
| `review` | Nothing at risk was found, but Git cannot show the work is finished: not merged into the default branch (squash and rebase merges look like this unless gh shows them), no upstream, or an upstream that is gone |
```

In `docs/usage.md`, replace:
```markdown
| Linked worktree | It exists, is not the main worktree, is not locked, has no changes, untracked files or conflicts, has no operation in progress, contains no ignored files, has no files marked assume-unchanged or (present) skip-worktree, and its HEAD is reachable from the default branch | `git worktree remove <path>` |
| Local branch | Its tip is reachable from the default ref, it is not the default branch, and no remaining worktree has it checked out | `git update-ref --no-deref -d refs/heads/<name> <commit>`, then `git config --local --remove-section branch.<name>` |
```
with:
```markdown
| Linked worktree | It exists, is not the main worktree, is not locked, has no changes, untracked files or conflicts, has no operation in progress, contains no ignored files, has no files marked assume-unchanged or (present) skip-worktree, and its HEAD is reachable from the default branch or GitHub merged its pull request with exactly that HEAD | `git worktree remove <path>` |
| Local branch | Its tip is reachable from the default ref or GitHub merged its pull request with exactly that tip, it is not the default branch, and no remaining worktree has it checked out | `git update-ref --no-deref -d refs/heads/<name> <commit>`, then `git config --local --remove-section branch.<name>` |
```

In `docs/usage.md`, replace:
```markdown
after the fresh fetch. There is no squash or rebase-merge detection: a branch
merged that way shows as not merged and is kept.
```
with:
```markdown
after the fresh fetch. Git cannot see squash or rebase merges. With gh
([GitHub pull requests](#github-pull-requests)), the review also asks GitHub,
never from the cache, about each unmerged branch with an upstream on GitHub:
a pull request merged into its repository's default branch whose head commit
is exactly the branch tip or the worktree's HEAD counts as merged, shown as
`merged via PR #42 into main on GitHub`. Every other check still applies. A
pull request still open, merged at another commit or into another branch, or
closed keeps the item for review with that reason, as does a failed GitHub
check. Without gh, such merges show as not merged and are kept.
```

In `docs/usage.md`, replace:
```markdown
the status line points to it with "d restore commands".
```
with:
```markdown
the status line points to it with "d restore commands".
A branch deleted on GitHub's evidence alone has commits no other ref reaches,
which `git gc` may eventually drop; its restore command adds a fallback that
fetches them from the pull request, for example
`git branch feat/x <commit> || git fetch origin 'refs/pull/42/head:refs/heads/feat/x'`.
For a fork's pull request into its parent, the fallback fetches from the
parent's URL.
```

In `docs/usage.md`, replace:
```markdown
force-remove worktrees or delete unmerged work; the only branches it deletes
are fully merged ones, after review.
```
with:
```markdown
force-remove worktrees or delete unmerged work; the only branches it deletes
are fully merged ones, by Git or through a GitHub pull request with exactly
their commit, after review.
```

In `README.md`, replace:
```markdown
- **Worktree cleanup** removes only clean worktrees already merged into the
  default branch.
```
with:
```markdown
- **Worktree cleanup** removes only clean worktrees already merged into the
  default branch, by Git or, with gh, through a pull request whose head is
  exactly the local commit.
```

In `README.md`, replace:
```markdown
  branch's pull request, review and CI checks in the dashboard and in
  `gitperch status --github`.
```
with:
```markdown
  branch's pull request, review and CI checks in the dashboard and in
  `gitperch status --github`. Clean up also recognizes squash- and
  rebase-merged branches.
```

In `CHANGELOG.md`, replace:
```markdown
  `✓ merged #42` and the `pr_merged` attention reason, and lifecycle JSON
  gains the `merged_pull_request` signal.
```
with:
```markdown
  `✓ merged #42` and the `pr_merged` attention reason, and lifecycle JSON
  gains the `merged_pull_request` signal.
- Clean up removes squash- and rebase-merged worktrees and branches when a
  fresh GitHub check shows their pull request merged into the default branch
  with exactly their commit; every other check still applies. Such a branch's
  restore command falls back to fetching GitHub's `refs/pull/<N>/head`. Pull
  requests that prove nothing (still open, merged at another commit or into
  another branch, closed) and failed checks are listed as review reasons.
```

- [ ] **Step 3: Verify and commit**

Run: `make check`.

```bash
make check
git add cmd/gitperch docs README.md CHANGELOG.md
git commit -m "feat: let Clean up remove squash-merged work GitHub proves merged"
```

---

# Phase 4 — Open in the browser

### Task 15: `gh browse` commands and browse targets

**Files:**
- Create: `internal/github/browse.go`
- Modify: `internal/app/github.go` (`BrowseTarget`)
- Test: `internal/github/browse_test.go`, `internal/app/github_test.go`

**Interfaces:**
- Consumes: `Runner.executable`, `environment` (Task 1); `Row.CurrentPullRequest` (Task 6).
- Produces:
  ```go
  func (r Runner) BrowseCommand(repo Repo, number int, branch string) (*exec.Cmd, error)
  type BrowseTarget struct { Host, Repository string; Number int; Branch string }
  func (r Row) BrowseTarget() (BrowseTarget, bool)
  ```

- [ ] **Step 1: Write the failing tests**

`internal/github/browse_test.go`:
```go
package github

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBrowseCommand(t *testing.T) {
	gh, _ := fakeGH(t, "", 0)
	repo := Repo{"github.com", "acme", "widgets"}
	for _, tc := range []struct {
		number int
		branch string
		want   []string
	}{
		{42, "ignored", []string{"browse", "42", "--repo=github.com/acme/widgets"}},
		{0, "-x/feature", []string{"browse", "--repo=github.com/acme/widgets", "--branch=-x/feature"}},
		{0, "", []string{"browse", "--repo=github.com/acme/widgets"}},
	} {
		cmd, err := (Runner{Executable: gh}).BrowseCommand(repo, tc.number, tc.branch)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cmd.Args[1:], tc.want) || cmd.Dir != os.TempDir() || !slices.Contains(cmd.Env, "GH_PROMPT_DISABLED=1") {
			t.Fatalf("command %q in %q", cmd.Args, cmd.Dir)
		}
	}
	if _, err := (Runner{Executable: filepath.Join(t.TempDir(), "missing-gh")}).BrowseCommand(repo, 1, ""); err == nil {
		t.Fatal("missing gh built a command")
	}
}
```

`internal/app/github_test.go`:
```diff
--- a/internal/app/github_test.go
+++ b/internal/app/github_test.go
@@ -301,3 +301,22 @@ func TestMergedPullRequestNeedsTheCheckedOutHead(t *testing.T) {
 		}
 	}
 }
+
+func TestBrowseTarget(t *testing.T) {
+	row := ghRow("/repo", "feat/a")
+	if _, ok := row.BrowseTarget(); ok {
+		t.Fatal("a row without GitHub data has a page")
+	}
+	row.GitHub = &GitHubInfo{Host: "github.com", Repository: "me/widgets", Branch: "feat/a", PullRequests: []PullRequest{}}
+	if got, ok := row.BrowseTarget(); !ok || got != (BrowseTarget{Host: "github.com", Repository: "me/widgets", Branch: "feat/a"}) {
+		t.Fatalf("pushed branch: %+v", got)
+	}
+	row.Status.ComparisonKnown = false // the remote branch is gone
+	if got, _ := row.BrowseTarget(); got.Branch != "" {
+		t.Fatalf("gone branch: %+v", got)
+	}
+	row.GitHub.PullRequests = []PullRequest{{Number: 7, BaseRepository: "acme/widgets"}}
+	if got, _ := row.BrowseTarget(); got != (BrowseTarget{Host: "github.com", Repository: "acme/widgets", Number: 7}) {
+		t.Fatalf("fork pull request: %+v", got)
+	}
+}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/github -run TestBrowseCommand -v` and `go test ./internal/app -run TestBrowseTarget -v`
Expected: the builds fail with `BrowseCommand undefined` and `undefined: BrowseTarget`.

- [ ] **Step 3: Implement**

`internal/github/browse.go`:
```go
package github

import (
	"os"
	"os/exec"
	"strconv"
)

// BrowseCommand opens a pull request (number > 0) or the repository, at
// branch when one is given, in the browser gh is set up to use. The caller
// runs it with the terminal handed over, since BROWSER may name a terminal
// browser.
func (r Runner) BrowseCommand(repo Repo, number int, branch string) (*exec.Cmd, error) {
	bin, err := r.executable()
	if err != nil {
		return nil, err
	}
	args := []string{"browse"}
	if number > 0 {
		args = append(args, strconv.Itoa(number))
	}
	args = append(args, "--repo="+repo.String())
	if number == 0 && branch != "" {
		args = append(args, "--branch="+branch)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = environment(os.Environ())
	return cmd, nil
}
```

`internal/app/github.go`:
```diff
--- a/internal/app/github.go
+++ b/internal/app/github.go
@@ -98,6 +98,36 @@ func (r Row) CurrentPullRequest() *PullRequest {
 	return &r.GitHub.PullRequests[0]
 }
 
+// BrowseTarget is the GitHub page b opens for a row; see Row.BrowseTarget.
+type BrowseTarget struct {
+	Host       string
+	Repository string // owner/name
+	Number     int    // a pull request; 0 for the repository
+	Branch     string // with Number 0: the branch page, "" for the repository's
+}
+
+// BrowseTarget is the row's current pull request on the repository that holds
+// it, else its upstream repository at the branch once the branch is pushed.
+// False without GitHub data.
+func (r Row) BrowseTarget() (BrowseTarget, bool) {
+	g := r.GitHub
+	if g == nil || g.Repository == "" {
+		return BrowseTarget{}, false
+	}
+	t := BrowseTarget{Host: g.Host, Repository: g.Repository}
+	if pr := r.CurrentPullRequest(); pr != nil {
+		t.Number = pr.Number
+		if pr.BaseRepository != "" {
+			t.Repository = pr.BaseRepository
+		}
+		return t, true
+	}
+	if r.Status.ComparisonKnown {
+		t.Branch = g.Branch
+	}
+	return t, true
+}
+
 // MergeVerdict says whether GitHub proves a branch's work merged although Git
 // cannot show it: Proven when a pull request merged into its repository's
 // default branch had exactly the branch tip as its head. Otherwise Note
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/github ./internal/app`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
make check
git add internal/github internal/app
git commit -m "feat(github): build gh browse commands for rows"
```

---

### Task 16: `b`, the palette, hints and phase 4 docs

**Files:**
- Modify: `internal/tui/github.go` (`EnableBrowse`, `openInBrowser`, `browseLabel`)
- Modify: `internal/tui/model.go` (`browse` field, `b` key, `browserExitedMsg`)
- Modify: `internal/tui/palette.go`, `internal/tui/detail.go`, `internal/tui/view.go` (palette command, hints, help, next steps)
- Modify: `cmd/gitperch/tui.go` (`EnableBrowse`)
- Test: `internal/tui/github_test.go`
- Modify: `README.md`, `docs/ui.md`, `docs/usage.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes: `Row.BrowseTarget`, `github.Runner.BrowseCommand` (Task 15); `newGitHub` (Task 8).
- Produces:
  ```go
  type browseCommand func(app.BrowseTarget) (*exec.Cmd, error)
  type browserExitedMsg struct{ err error }
  func (m *Model) EnableBrowse(open func(app.BrowseTarget) (*exec.Cmd, error))
  func (m *Model) canBrowse(row app.Row) bool
  func (m *Model) openInBrowser() tea.Cmd
  func browseLabel(row app.Row) string
  ```

- [ ] **Step 1: Write the failing test**

`internal/tui/github_test.go`:
```diff
--- a/internal/tui/github_test.go
+++ b/internal/tui/github_test.go
@@ -2,6 +2,8 @@ package tui
 
 import (
 	"context"
+	"errors"
+	"os/exec"
 	"strings"
 	"sync/atomic"
 	"testing"
@@ -137,7 +139,7 @@ func TestDetailsShowPullRequestSection(t *testing.T) {
 	m.details = true
 	view := m.View().Content
 	for _, want := range []string{"Pull request · draft", "#42 Add tokens (draft)", "main ← feat/x · acme/widgets", "Checks failing · Changes requested · updated 3h ago",
-		"https://github.com/acme/widgets/pull/42", "Earlier: #40 merged into main", "Checked 2m ago", "PR #42 checks failing", "PR #42 checks are failing on GitHub."} {
+		"https://github.com/acme/widgets/pull/42", "Earlier: #40 merged into main", "Checked 2m ago", "PR #42 checks failing", "PR #42 checks are failing on GitHub. Open it with b."} {
 		if !strings.Contains(view, want) {
 			t.Errorf("details lack %q", want)
 		}
@@ -241,3 +243,36 @@ func TestMergedPullRequestStatusAndNextStep(t *testing.T) {
 		t.Fatalf("uncommitted changes come first: %q, %q", label, nextStep(dirty))
 	}
 }
+
+func TestBrowserOpensThePullRequest(t *testing.T) {
+	m := New(context.Background(), nil, true)
+	m.applySnapshot(app.Snapshot{Rows: []app.Row{prRow(failingPR)}})
+	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
+	if cmd := m.key(key("b")); cmd != nil || m.message != "No GitHub repository for this branch" {
+		t.Fatalf("without gh: %v, %q", cmd, m.message)
+	}
+	var opened app.BrowseTarget
+	m.EnableBrowse(func(target app.BrowseTarget) (*exec.Cmd, error) {
+		opened = target
+		return exec.Command("true"), nil
+	})
+	if cmd := m.key(key("b")); cmd == nil || opened != (app.BrowseTarget{Host: "github.com", Repository: "acme/widgets", Number: 42}) {
+		t.Fatalf("b: %v, %+v", cmd, opened)
+	}
+	m.Update(browserExitedMsg{})
+	if m.message != "Opened in the browser" || m.loading {
+		t.Fatalf("after return: %q, loading %v", m.message, m.loading)
+	}
+	if !strings.Contains(m.footer(), "[b] PR") {
+		t.Fatalf("footer: %q", m.footer())
+	}
+	m.palette, m.paletteQuery = true, "browser"
+	if items := m.paletteCommands(); len(items) == 0 || items[0].label != "Open pull request #42 in browser" {
+		t.Fatalf("palette: %+v", items)
+	}
+	m.palette = false
+	m.EnableBrowse(func(app.BrowseTarget) (*exec.Cmd, error) { return nil, errors.New("gh is not installed") })
+	if cmd := m.key(key("b")); cmd != nil || m.message != "gh browse: gh is not installed" {
+		t.Fatalf("failed command: %v, %q", cmd, m.message)
+	}
+}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tui -run 'Browser|DetailsShowPullRequestSection' -v`
Expected: the build fails with `m.EnableBrowse undefined`.

- [ ] **Step 3: Implement**

`internal/tui/github.go`:
```diff
--- a/internal/tui/github.go
+++ b/internal/tui/github.go
@@ -3,6 +3,7 @@ package tui
 import (
 	"context"
 	"fmt"
+	"os/exec"
 	"strings"
 
 	tea "charm.land/bubbletea/v2"
@@ -212,3 +213,48 @@ func (m *Model) pullRequestLines(row app.Row) []string {
 	}
 	return append(lines, m.style(checked, muted, false))
 }
+
+// browseCommand builds the gh command that opens a GitHub page.
+type browseCommand func(app.BrowseTarget) (*exec.Cmd, error)
+
+// browserExitedMsg reports that gh browse returned; nothing is refreshed.
+type browserExitedMsg struct{ err error }
+
+// EnableBrowse lets b open the highlighted row's pull request or repository.
+func (m *Model) EnableBrowse(open func(app.BrowseTarget) (*exec.Cmd, error)) { m.browse = open }
+
+// canBrowse reports whether b works for row.
+func (m *Model) canBrowse(row app.Row) bool {
+	_, ok := row.BrowseTarget()
+	return ok && m.browse != nil
+}
+
+// openInBrowser runs gh browse with the terminal handed over, since BROWSER
+// may name a terminal browser.
+func (m *Model) openInBrowser() tea.Cmd {
+	row := m.highlightedRow()
+	if row == nil {
+		m.message = "Select a repository first"
+		return nil
+	}
+	target, ok := row.BrowseTarget()
+	if !ok || m.browse == nil {
+		m.message = "No GitHub repository for this branch"
+		return nil
+	}
+	cmd, err := m.browse(target)
+	if err != nil {
+		m.message = "gh browse: " + gitcli.SafeText(err.Error())
+		return nil
+	}
+	return tea.ExecProcess(cmd, func(err error) tea.Msg { return browserExitedMsg{err: err} })
+}
+
+// browseLabel names what b opens for row in the command palette.
+func browseLabel(row app.Row) string {
+	target, _ := row.BrowseTarget()
+	if target.Number > 0 {
+		return fmt.Sprintf("Open pull request #%d in browser", target.Number)
+	}
+	return "Open " + gitcli.SafeText(target.Repository) + " on GitHub"
+}
```

`internal/tui/model.go`:
```diff
--- a/internal/tui/model.go
+++ b/internal/tui/model.go
@@ -99,8 +99,9 @@ type Model struct {
 	github             *app.GitHub // nil: no pull request lookups
 	githubCtx          context.Context
 	githubCancel       context.CancelFunc
-	githubPending      int  // lookups in flight
-	githubForce        bool // the next snapshot rechecks GitHub
+	githubPending      int           // lookups in flight
+	githubForce        bool          // the next snapshot rechecks GitHub
+	browse             browseCommand // nil: b is unavailable
 }
 
 type snapshotMsg struct {
@@ -310,6 +311,12 @@ func (m *Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
 			return m, m.scheduleAutoRefresh()
 		}
 		return m, m.refresh()
+	case browserExitedMsg:
+		if msg.err != nil {
+			m.message = "gh browse: " + msg.err.Error()
+		} else {
+			m.message = "Opened in the browser"
+		}
 	case childExitedMsg:
 		if msg.err != nil {
 			m.message = "Child process: " + msg.err.Error()
@@ -508,6 +515,8 @@ func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
 			return m.launchShell()
 		case "g":
 			return m.launchLazyGit()
+		case "b":
+			return m.openInBrowser()
 		case "r":
 			m.forceGitHub()
 			return m.refresh()
@@ -763,6 +772,8 @@ func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
 		return m.launchShell()
 	case "g":
 		return m.launchLazyGit()
+	case "b":
+		return m.openInBrowser()
 	}
 	return nil
 }
```

`internal/tui/palette.go`:
```diff
--- a/internal/tui/palette.go
+++ b/internal/tui/palette.go
@@ -29,6 +29,9 @@ func (m *Model) commands() []command {
 	if m.lazyGitAvailable {
 		commands = append(commands, command{"lazygit", "Open LazyGit · " + name})
 	}
+	if m.canBrowse(*row) {
+		commands = append(commands, command{"browse", browseLabel(*row)})
+	}
 	if m.actions != nil {
 		target := name
 		if len(m.selected) > 0 {
@@ -150,6 +153,8 @@ func (m *Model) executeCommand(id string) tea.Cmd {
 		return m.launchShell()
 	case "lazygit":
 		return m.launchLazyGit()
+	case "browse":
+		return m.openInBrowser()
 	case "refresh":
 		m.forceGitHub()
 		return m.refresh()
@@ -276,6 +281,8 @@ func (m *Model) commandHint(id string) string {
 		return "Interactive shell in the highlighted repository"
 	case "lazygit":
 		return "Open the installed Git interface"
+	case "browse":
+		return "Uses gh and your configured browser"
 	case "fetch":
 		return "Update remote-tracking refs"
 	case "push":
@@ -334,6 +341,8 @@ func (m *Model) commandIcon(id string) (string, string) {
 		return "$", success
 	case "lazygit":
 		return m.symbols().branch, branchColor
+	case "browse":
+		return "↗", accent
 	case "fetch":
 		return "⇣", accent
 	case "push":
```

`internal/tui/detail.go`:
```diff
--- a/internal/tui/detail.go
+++ b/internal/tui/detail.go
@@ -323,6 +323,9 @@ func (m *Model) detailActions(row app.Row) string {
 	if m.lazyGitAvailable {
 		hints = append(hints, hint{"g", "LazyGit", 2})
 	}
+	if m.canBrowse(row) {
+		hints = append(hints, hint{"b", "Browser", 2})
+	}
 	s := row.Status
 	if m.actions != nil && s.Error == "" && s.Operation == "" && s.Conflicts == 0 && !s.Detached && !s.Unborn && s.Upstream != "" && s.ComparisonKnown {
 		if s.Ahead > 0 && s.Behind == 0 {
```

`internal/tui/view.go`:
```diff
--- a/internal/tui/view.go
+++ b/internal/tui/view.go
@@ -621,9 +621,9 @@ func nextStep(row app.Row) string {
 	case s.Dirty():
 		return "Review local changes in your shell or LazyGit. Tracking refs are up to date."
 	case open != nil && open.Checks == app.ChecksFailing:
-		return fmt.Sprintf("PR #%d checks are failing on GitHub.", open.Number)
+		return fmt.Sprintf("PR #%d checks are failing on GitHub. Open it with b.", open.Number)
 	case open != nil && open.Review == app.ReviewChangesRequested:
-		return fmt.Sprintf("Reviewers requested changes on PR #%d.", open.Number)
+		return fmt.Sprintf("Reviewers requested changes on PR #%d. Open it with b.", open.Number)
 	default:
 		return "Worktree is clean and locally known tracking refs are up to date. Select and fetch to check the remote."
 	}
@@ -718,6 +718,9 @@ func (m *Model) footer() string {
 			}
 		}
 	}
+	if row := m.highlightedRow(); row != nil && row.CurrentPullRequest() != nil && m.canBrowse(*row) {
+		hints = append(hints, hint{"b", "PR", 4})
+	}
 	hints = append(hints, hint{"Space", "Select", 4})
 	if m.cleanupSupported && m.actions != nil {
 		hints = append(hints, hint{"c", "Clean up", 5})
@@ -742,6 +745,15 @@ func (m *Model) helpContent() []string {
 		"", " GLOBAL COMMANDS", " : / Ctrl+K    Fuzzy command palette · arrows choose · Enter runs", " ?             Help · q quit · Ctrl+C interrupt", "", " AGENT ACTIONS", " No agent integration is configured in this application.", "", " Sync counts use locally known refs. Fetch checks the remote.", " ↑ ahead · ↓ behind · unknown never means up to date.",
 		" Esc cancels the confirmation popup; during a batch it requests cancellation.", " Esc clears search, then dismisses results. q quits; Ctrl+C interrupts.",
 	}
+	if m.browse != nil {
+		// Documented only where gh can open pages.
+		for i, line := range lines {
+			if strings.HasPrefix(line, " g ") {
+				lines = slices.Insert(lines, i+1, " b             Open the pull request, or the GitHub repository, in the browser (gh)")
+				break
+			}
+		}
+	}
 	if m.cleanupSupported {
 		// Documented only where Clean up is offered.
 		for i, line := range lines {
```

`cmd/gitperch/tui.go`:
```diff
--- a/cmd/gitperch/tui.go
+++ b/cmd/gitperch/tui.go
@@ -10,9 +10,11 @@ import (
 	"github.com/mark-lvl/gitperch/internal/config"
 	"github.com/mark-lvl/gitperch/internal/discovery"
 	gitcli "github.com/mark-lvl/gitperch/internal/git"
+	"github.com/mark-lvl/gitperch/internal/github"
 	"github.com/mark-lvl/gitperch/internal/tui"
 	"io"
 	"os"
+	"os/exec"
 	"strings"
 	"time"
 )
@@ -42,9 +44,13 @@ func runTUI(ctx context.Context, cfg config.Config, ws config.Workspace, noColor
 	model.EnableDetails(read.Details)
 	model.EnablePatch(read.Patch)
 	// Without gh, Git 2.36 or [github] enabled, the dashboard never runs gh.
-	if gh, _, err := newGitHub(ctx, cfg, read); err == nil {
+	if gh, runner, err := newGitHub(ctx, cfg, read); err == nil {
 		model.EnableGitHub(gh)
 		actions.SetGitHub(gh)
+		model.EnableBrowse(func(t app.BrowseTarget) (*exec.Cmd, error) {
+			owner, name, _ := strings.Cut(t.Repository, "/")
+			return runner.BrowseCommand(github.Repo{Host: t.Host, Owner: owner, Name: name}, t.Number, t.Branch)
+		})
 	}
 	model.Configure(strings.Join(ws.Paths, ", "), cfg.UI.Icons, cfg.UI.DefaultFocus)
 	model.SetAutoRefresh(time.Duration(cfg.UI.RefreshSeconds) * time.Second)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui ./cmd/gitperch`
Expected: PASS. The captures are unchanged: they do not enable browsing.

- [ ] **Step 5: Update the docs**

In `README.md`, replace:
```markdown
| `o` / `g` | Open a shell / lazygit in the repository |
```
with:
```markdown
| `o` / `g` | Open a shell / lazygit in the repository |
| `b` | Open the pull request, or the GitHub repository, in the browser (gh) |
```

In `docs/ui.md`, replace:
```markdown
| g | Open LazyGit; palette lists it only when installed |
```
with:
```markdown
| g | Open LazyGit; palette lists it only when installed |
| b | Open the branch's pull request, or its GitHub repository, in the browser through `gh browse`; listed only with gh |
```

In `docs/usage.md`, replace:
```markdown
Use arrows/j/k to navigate, Enter for repository details, `d` for changes, and
`o` for a shell.
```
with:
```markdown
Use arrows/j/k to navigate, Enter for repository details, `d` for changes,
`o` for a shell, and `b` to open the branch's pull request (or GitHub
repository) in the browser through gh.
```

In `CHANGELOG.md`, replace:
```markdown
  requests that prove nothing (still open, merged at another commit or into
  another branch, closed) and failed checks are listed as review reasons.
```
with:
```markdown
  requests that prove nothing (still open, merged at another commit or into
  another branch, closed) and failed checks are listed as review reasons.
- `b`, and the command palette, open the highlighted branch's pull request,
  or its GitHub repository, in the browser through `gh browse`.
```

- [ ] **Step 6: Verify and commit**

Run: `make check`. Then smoke-test by hand in a checkout whose `origin` is on GitHub, with gh logged in:
- `bin/gitperch <root>` shows `checking GitHub` briefly. A branch with an open pull request shows `PR #N · open · …` in its preview, and Enter shows the Pull request section.
- `b` opens the pull request in the browser and returns to the dashboard with "Opened in the browser".
- With `[github] enabled = false` in the config, none of this appears and gh never runs (`strace -f -e trace=execve` or `ps` shows no `gh`).

```bash
make check
git add internal/tui cmd/gitperch README.md docs CHANGELOG.md
git commit -m "feat(tui): open pull requests in the browser with b"
```

---

## After the last task

- `git log --oneline` shows sixteen commits, one per task.
- `make check` passes, and `git status` is clean.
- The spec's out-of-scope list still holds: no GitHub mutation, no other forges, no SSH alias resolution, no triangular matching, no headless Clean up.
- Report what was verified, including the manual smoke test of Task 16, and anything that could not be checked (for example macOS, or PNG captures without `pillow`/`pyte`).
