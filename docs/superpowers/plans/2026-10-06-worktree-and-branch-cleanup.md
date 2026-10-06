# Worktree Inventory and Merged Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show every worktree of each discovered repository grouped under its main repository, and offer a reviewed cleanup that prunes stale worktree records, removes clean merged worktrees and deletes fully merged local branches without ever losing work.

**Architecture:** A new read-only inventory step in `app.Load` asks Git for each common directory's worktrees (`worktree list --porcelain -z`) and attaches `WorktreeInfo` to rows; the TUI groups rows by `WorktreeInfo.MainPath`. Cleanup is a second, item-shaped plan inside the existing `app.Actions` engine (`PlanCleanup` / `ExecuteCleanup`) that shares its single-active-preview rule and per-common-directory locks, with dedicated validated Runner methods for the three new mutations.

**Tech Stack:** Go 1.27, Bubble Tea v2, Lip Gloss v2, Git CLI ≥ 2.36 (development uses 2.43).

**Spec:** `docs/superpowers/specs/2026-10-06-worktree-and-branch-cleanup-design.md`

## Global Constraints

- Invoke Git only through `gitcli.Runner.Run` with argument arrays; never build shell strings.
- New mutations are exactly: `worktree prune`, `worktree remove <path>` (never `--force`), `update-ref -d refs/heads/<b> <oid>` plus `config --local --remove-section branch.<b>`. Nothing else.
- A worktree is removable only when it exists, is linked (not main), is not locked, has no changes, untracked files, conflicts, operation in progress or ignored files, and its HEAD is reachable from the default ref.
- "Merged" means reachable (`merge-base --is-ancestor`) from `refs/remotes/<remote>/HEAD` after a fresh fetch; with no remote, local `refs/heads/main`, else `refs/heads/master`, labelled "local default". No squash detection. gitperch never sets `<remote>/HEAD`.
- Cleanup mutations stay TUI-only; `gitperch status` stays read-only and offline.
- JSON schema version stays `1`; new fields are additive.
- Tests touching Git use temporary repositories and local bare remotes only.
- All user-visible text derived from Git passes through `gitcli.SafeText`.
- After TUI changes, update render captures per `docs/ui.md#render-captures-and-validation`.
- Each user-visible change gets an `Unreleased` CHANGELOG entry. Conventional Commit messages ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- `make check` passes at the end of every task.

## Review Focus

1. **Symlinked roots.** A scanned row's path (unresolved) and Git's reported worktree path (resolved) differ → the worktree must appear once, under the scanned path. Test in Task 2 (`TestLoadMatchesSymlinkedWorktreePaths`).
2. **Dangling `origin/HEAD`.** `refs/remotes/origin/HEAD` points at a branch deleted by `fetch --prune` → nothing eligible except prune, with a reason; no panic. Test in Task 7 (`TestCleanupDanglingDefaultRef`).
3. **Unticked worktree, ticked branch.** The user unticks a worktree removal but keeps its branch ticked → `update-ref` does not check worktrees, so revalidation must skip the branch ("checked out in …"). Test in Task 10 (`TestCleanupSkipsBranchStillCheckedOut`).
4. **Huge ignored trees.** `node_modules` with many files → `--directory` collapses it; if output still exceeds the limit the worktree is kept with "too many ignored files", not an error. Test in Task 7 (`TestCleanupKeepsWorktreeWhenIgnoredListingOverflows`).
5. **Default branch checked out in a second worktree.** A clean worktree on `main` is removable (nothing lost), but `main` itself is never a delete candidate. Test in Task 10 (`TestCleanupNeverDeletesDefaultBranch`).

---

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/git/worktree.go` (new) | `Worktree`, `ParseWorktrees`, `Runner.Worktrees` |
| `internal/git/cleanup.go` (new) | Cleanup reads (`ResolveRemote`, `RemoteDefaultRef`, `ResolveCommit`, `IsAncestor`, `IgnoredFiles`, `LocalBranches`) and mutations (`PruneWorktrees`, `RemoveWorktree`, `DeleteBranch`) |
| `internal/git/metadata.go` | `ResolveFetch` delegates validation to `ResolveRemote` |
| `internal/git/service.go` | Read/Write passthroughs for the new methods |
| `internal/app/worktrees.go` (new) | `WorktreeInfo`, `attachWorktrees` inventory step |
| `internal/app/dashboard.go` | `Load` calls `attachWorktrees` |
| `internal/app/inspect.go` | `Row.Worktree`, shared `sortRows` |
| `internal/app/format.go` | Table marker for stale/locked/bare worktrees |
| `internal/app/cleanup.go` (new) | `CleanupGit`, `CleanupItem`, `CleanupPreview`, `PlanCleanup`, `ExecuteCleanup` |
| `internal/app/actions.go` | `Cleanup` action label, `Event.Item` |
| `internal/tui/groups.go` (new) | Grouped ordering, context rows, selectability, tree prefixes, badges |
| `internal/tui/model.go` | `expanded` state, keys, selection rules, `visibleRows` delegating to groups |
| `internal/tui/view.go` | Tree prefix, badge, stale/locked/bare status, attention rank |
| `internal/tui/detail.go` | Worktree tab lists the group; skip detail loads for stale/bare rows |
| `internal/tui/cleanup.go` (new) | Cleanup prepare, review screen, keys, execution |
| `internal/tui/actions.go` | Results keyed by item |
| `internal/tui/palette.go` | `cleanup` command, hint, icon |
| docs, captures, CHANGELOG | per task |

---

# Phase 1 — Inventory and grouping (read-only)

### Task 1: Parse and list worktrees

**Files:**
- Create: `internal/git/worktree.go`
- Test: `internal/git/worktree_test.go`
- Modify: `internal/git/readonly_test.go` (append one test)

**Interfaces:**
- Consumes: `Runner.Run`, `objectID` (`internal/git/parse.go`).
- Produces:
  ```go
  type Worktree struct {
      Path           string // absolute, cleaned, as Git reports it
      HeadOID        string
      Branch         string // short name without refs/heads/; "" when detached/bare
      Bare, Detached bool
      Main           bool   // first record
      Locked         bool
      LockReason     string
      Prunable       bool
      PrunableReason string
  }
  func ParseWorktrees(data []byte) ([]Worktree, error)
  func (r Runner) Worktrees(ctx context.Context, path string) ([]Worktree, error)
  ```

- [ ] **Step 1: Write the failing parser and integration tests**

`internal/git/worktree_test.go`:
```go
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
```

Append to `internal/git/readonly_test.go`:
```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/git -run 'Worktree' -v`
Expected: FAIL to compile, `undefined: ParseWorktrees` / `Runner.Worktrees`.

- [ ] **Step 3: Implement `internal/git/worktree.go`**

```go
package git

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// Worktree is one record of `git worktree list --porcelain -z`. The first
// record is always the main worktree, which may be a bare repository.
type Worktree struct {
	Path           string
	HeadOID        string
	Branch         string
	Bare, Detached bool
	Main           bool
	Locked         bool
	LockReason     string
	Prunable       bool
	PrunableReason string
}

var errWorktreeList = errors.New("malformed worktree list")

// ParseWorktrees reads NUL-terminated attributes; an empty attribute ends a
// record. Unknown attributes from newer Git are ignored, structural surprises
// are rejected rather than guessed.
func ParseWorktrees(data []byte) ([]Worktree, error) {
	var result []Worktree
	var current *Worktree
	fields := strings.Split(string(data), "\x00")
	// Output ends with the record terminator plus Split's trailing empty field.
	if len(fields) < 2 || fields[len(fields)-1] != "" {
		return nil, errWorktreeList
	}
	for _, field := range fields[:len(fields)-1] {
		if field == "" {
			if current == nil {
				return nil, errWorktreeList
			}
			result = append(result, *current)
			current = nil
			continue
		}
		key, value, _ := strings.Cut(field, " ")
		if key == "worktree" {
			if current != nil || !filepath.IsAbs(value) {
				return nil, errWorktreeList
			}
			current = &Worktree{Path: filepath.Clean(value), Main: len(result) == 0}
			continue
		}
		if current == nil {
			return nil, errWorktreeList
		}
		switch key {
		case "HEAD":
			if !objectID(value) {
				return nil, errWorktreeList
			}
			current.HeadOID = value
		case "branch":
			name, ok := strings.CutPrefix(value, "refs/heads/")
			if !ok || name == "" {
				return nil, errWorktreeList
			}
			current.Branch = name
		case "detached":
			current.Detached = true
		case "bare":
			current.Bare = true
		case "locked":
			current.Locked, current.LockReason = true, value
		case "prunable":
			current.Prunable, current.PrunableReason = true, value
		}
	}
	if current != nil || len(result) == 0 {
		return nil, errWorktreeList
	}
	return result, nil
}

// Worktrees lists every worktree sharing path's common Git directory,
// including linked worktrees outside any scanned root and stale records.
func (r Runner) Worktrees(ctx context.Context, path string) ([]Worktree, error) {
	out, err := r.Run(ctx, path, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out.Stdout)
}
```

Note: if `objectID` rejects the all-zero OID Git prints for an unborn HEAD, accept it explicitly here (`value == strings.Repeat("0", len(value))` with length 40 or 64).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/git -run 'Worktree' -v`
Expected: PASS.

- [ ] **Step 5: Run `make check` and commit**

```bash
make check
git add internal/git/worktree.go internal/git/worktree_test.go internal/git/readonly_test.go
git commit -m "feat(git): list worktrees from porcelain output

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Attach the worktree inventory to the snapshot

**Files:**
- Create: `internal/app/worktrees.go`
- Modify: `internal/app/inspect.go` (add `Row.Worktree`, extract `sortRows`)
- Modify: `internal/app/dashboard.go` (`Load` calls `attachWorktrees`)
- Modify: `internal/app/format.go` (table markers)
- Test: `internal/app/worktrees_test.go`

**Interfaces:**
- Consumes: `gitcli.Worktree`, `Runner.Worktrees` (Task 1), `Inspect`, `discovery.Warning`.
- Produces:
  ```go
  type WorktreeInfo struct {
      Main           bool   `json:"main"`
      Linked         bool   `json:"linked"`
      Bare           bool   `json:"bare,omitempty"`
      Locked         bool   `json:"locked,omitempty"`
      LockReason     string `json:"lock_reason,omitempty"`
      Prunable       bool   `json:"prunable,omitempty"`
      PrunableReason string `json:"prunable_reason,omitempty"`
      MainPath       string `json:"main_path"`
      OutsideRoots   bool   `json:"outside_roots,omitempty"`
  }
  // Row gains: Worktree *WorktreeInfo `json:"worktree,omitempty"`
  type WorktreeLister interface {
      Worktrees(context.Context, string) ([]gitcli.Worktree, error)
  }
  func (r Row) Selectable() bool // false for stale, bare rows
  func sortRows(rows []Row)
  ```
  Invariant later tasks rely on: every row of a successfully listed group has `Worktree.MainPath` equal to the group parent row's `Path`.

- [ ] **Step 1: Write the failing tests**

`internal/app/worktrees_test.go` (reuses `actionTestRepo`, `actionGit`, `actionTestWrite`, `actionTestCommit` from `actions_test.go`):
```go
package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
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
```
Add helpers in the same file:
```go
type failingLister struct{ gitcli.Runner }

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
```
(import `errors` and `github.com/mark-lvl/gitperch/internal/repository`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app -run 'TestLoad' -v`
Expected: FAIL to compile (`attachWorktrees`, `Row.Worktree`, `Selectable` undefined).

- [ ] **Step 3: Add the row field and shared sort in `internal/app/inspect.go`**

```go
type Row struct {
	repository.Repository
	Status    repository.Status `json:"status"`
	LastFetch time.Time         `json:"last_successful_fetch,omitzero"`
	Worktree  *WorktreeInfo     `json:"worktree,omitempty"`
}

// Selectable reports whether fetch, push and pull can target the row: stale
// records have no directory and a bare main repository has no worktree.
func (r Row) Selectable() bool {
	return r.Worktree == nil || (!r.Worktree.Prunable && !r.Worktree.Bare)
}

func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Path < rows[j].Path
	})
}
```
Leave `Inspect`'s own repository sort as is.

- [ ] **Step 4: Implement `internal/app/worktrees.go`**

```go
package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// WorktreeInfo places a row within its repository's worktrees. MainPath is
// the Path of the group's parent row, so the TUI groups rows by it.
type WorktreeInfo struct {
	Main           bool   `json:"main"`
	Linked         bool   `json:"linked"`
	Bare           bool   `json:"bare,omitempty"`
	Locked         bool   `json:"locked,omitempty"`
	LockReason     string `json:"lock_reason,omitempty"`
	Prunable       bool   `json:"prunable,omitempty"`
	PrunableReason string `json:"prunable_reason,omitempty"`
	MainPath       string `json:"main_path"`
	OutsideRoots   bool   `json:"outside_roots,omitempty"`
}

type WorktreeLister interface {
	Worktrees(context.Context, string) ([]gitcli.Worktree, error)
}

// identity compares paths the way Git reports them (symlinks resolved) while
// rows keep the path discovery found them under.
func identity(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func missingDir(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

func underRoots(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(identity(root), identity(path))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// attachWorktrees lists worktrees once per common Git directory, adds rows
// for worktrees the scan could not reach and labels every member. A listing
// failure leaves that group's rows unlabelled and adds a warning.
func attachWorktrees(ctx context.Context, rows []Row, service GitService, workers int, roots []string) ([]Row, []discovery.Warning) {
	lister, ok := service.(WorktreeLister)
	if !ok {
		return rows, nil
	}
	groups := map[string][]int{}
	var order []string
	for i, row := range rows {
		common := row.Status.CommonDir
		if row.Status.Error != "" || common == "" {
			continue
		}
		if _, seen := groups[common]; !seen {
			order = append(order, common)
		}
		groups[common] = append(groups[common], i)
	}
	lists := make([][]gitcli.Worktree, len(order))
	errs := make([]error, len(order))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(1, min(workers, len(order))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lists[i], errs[i] = lister.Worktrees(ctx, rows[groups[order[i]][0]].Path)
			}
		}()
	}
	for i := range order {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var warnings []discovery.Warning
	var extra []repository.Repository
	type pending struct {
		info WorktreeInfo
		wt   gitcli.Worktree
	}
	added := map[string]pending{}
	for g, common := range order {
		if errs[g] != nil {
			warnings = append(warnings, discovery.Warning{Path: rows[groups[common][0]].Path, Message: "worktree list: " + gitcli.SafeText(errs[g].Error())})
			continue
		}
		byIdentity := map[string]int{}
		for _, i := range groups[common] {
			byIdentity[identity(rows[i].Path)] = i
		}
		mainPath := lists[g][0].Path
		if i, ok := byIdentity[identity(mainPath)]; ok {
			mainPath = rows[i].Path
		}
		for _, wt := range lists[g] {
			// Git never reports a locked worktree as prunable, even when its
			// directory is gone; it is just as unusable as a row.
			wt.Prunable = wt.Prunable || !wt.Bare && missingDir(wt.Path)
			if wt.Prunable && wt.PrunableReason == "" {
				wt.PrunableReason = "directory missing"
			}
			info := WorktreeInfo{Main: wt.Main, Linked: !wt.Main, Bare: wt.Bare, Locked: wt.Locked, LockReason: gitcli.SafeText(wt.LockReason), Prunable: wt.Prunable, PrunableReason: gitcli.SafeText(wt.PrunableReason), MainPath: mainPath}
			if i, ok := byIdentity[identity(wt.Path)]; ok {
				rows[i].Worktree = &info
				continue
			}
			info.OutsideRoots = !underRoots(wt.Path, roots)
			repo := repository.Repository{Path: wt.Path, Name: filepath.Base(wt.Path)}
			if wt.Main {
				repo.Path = mainPath
			}
			added[repo.Path] = pending{info, wt}
			if !wt.Prunable && !wt.Bare {
				extra = append(extra, repo)
			}
		}
	}
	inspected, _ := Inspect(ctx, extra, service, max(1, workers))
	for _, row := range inspected {
		p := added[row.Path]
		row.Worktree = &p.info
		rows = append(rows, row)
		delete(added, row.Path)
	}
	for path, p := range added { // stale records and bare main repositories
		info := p.info
		rows = append(rows, Row{Repository: repository.Repository{Path: path, Name: filepath.Base(path)}, Status: repository.Status{Branch: p.wt.Branch, HeadOID: p.wt.HeadOID, Detached: p.wt.Detached}, Worktree: &info})
	}
	sortRows(rows)
	return rows, warnings
}
```

Modify `Load` in `internal/app/dashboard.go`:
```go
func Load(ctx context.Context, opts discovery.Options, service GitService, workers int) (Snapshot, error) {
	result, err := discovery.Scan(ctx, opts)
	s := Snapshot{Warnings: result.Warnings}
	if err != nil {
		return s, err
	}
	s.Rows, err = Inspect(ctx, result.Repositories, service, workers)
	if err != nil {
		return s, err
	}
	var warnings []discovery.Warning
	s.Rows, warnings = attachWorktrees(ctx, s.Rows, service, workers, opts.Roots)
	s.Warnings = append(s.Warnings, warnings...)
	return s, ctx.Err()
}
```

In `WriteTable` (`internal/app/format.go`), before the `Synchronized` check add:
```go
		if w := row.Worktree; w != nil {
			switch {
			case w.Bare:
				markers = append(markers, "bare repository")
			case w.Prunable:
				markers = append(markers, "stale worktree: "+w.PrunableReason)
			case w.Locked:
				markers = append(markers, "locked worktree")
			}
		}
```
and skip the `no upstream` marker when `row.Worktree != nil && (row.Worktree.Prunable || row.Worktree.Bare)`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app -v -run 'TestLoad'` then `go test ./...`
Expected: PASS. Fix any existing `app`/`cmd` tests that assert exact row counts or JSON by adding the new `worktree` object to their expectations (every listed repository now has `"worktree": {"main": true, "linked": false, "main_path": …}`).

- [ ] **Step 6: Commit**

```bash
make check
git add internal/app cmd
git commit -m "feat(app): attach every repository's worktrees to the snapshot

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Group worktrees in the workspace list

**Files:**
- Create: `internal/tui/groups.go`
- Modify: `internal/tui/model.go` (`expanded` field and init, `visibleRows`, keys `right`/`left`, Space/`a` selection rules, `applySnapshot` selection filter, `executeCommand` fetch/push/pull target filter, `launchShell`/`launchLazyGit` guard)
- Modify: `internal/tui/view.go` (`attentionRank`, `primaryStatus`, `repositoryList`, `tableRow` signature)
- Modify: `internal/tui/detail.go` (`ensureDetail` skips stale/bare rows), `internal/tui/patch.go` (`ensurePatch` likewise)
- Modify: `internal/tui/theme.go` (icon `worktree`: `⑂` unicode, `wt` ascii)
- Test: `internal/tui/groups_test.go`

**Interfaces:**
- Consumes: `app.Row.Worktree`, `app.Row.Selectable()` (Task 2).
- Produces:
  ```go
  func groupKey(row app.Row) string            // Worktree.MainPath, else row.Path
  func (m *Model) matchesView(row app.Row) bool // filter and scope
  func (m *Model) isContext(index int) bool     // visible only as a parent of a match
  func (m *Model) treePrefix(pos int, indices []int) string // "", "├─ ", "└─ "
  func (m *Model) groupBadge(row app.Row) string
  // Model gains: expanded map[string]bool
  ```

- [ ] **Step 1: Write the failing tests**

`internal/tui/groups_test.go`:
```go
package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/repository"
)

func groupedRows() []app.Row {
	main := "/home/mark/projects/api"
	info := func(linked bool) *app.WorktreeInfo {
		return &app.WorktreeInfo{Main: !linked, Linked: linked, MainPath: main}
	}
	stale := info(true)
	stale.Prunable, stale.PrunableReason = true, "gitdir file points to non-existent location"
	return []app.Row{
		{Repository: repository.Repository{Name: "api", Path: main}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}, Worktree: info(false)},
		{Repository: repository.Repository{Name: "fix-auth", Path: main + "/.claude/worktrees/fix-auth"}, Status: repository.Status{Branch: "fix-auth", Changes: 2}, Worktree: info(true)},
		{Repository: repository.Repository{Name: "old", Path: "/tmp/old"}, Status: repository.Status{Branch: "old"}, Worktree: stale},
		{Repository: repository.Repository{Name: "web", Path: "/home/mark/projects/web"}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}},
	}
}

func visibleNames(m *Model) []string {
	var names []string
	for _, i := range m.visibleRows() {
		names = append(names, m.rows[i].Name)
	}
	return names
}

func TestGroupsCollapseAndExpand(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	if got := strings.Join(visibleNames(m), ","); got != "api,web" {
		t.Fatalf("collapsed: %s", got)
	}
	m.key(key("right"))
	if got := strings.Join(visibleNames(m), ","); got != "api,old,fix-auth,web" && got != "api,fix-auth,old,web" {
		t.Fatalf("expanded: %s", got)
	}
	m.highlight = 2
	m.key(key("left"))
	if m.highlightedRow().Name != "api" {
		t.Fatalf("left on a child should jump to its parent, got %s", m.highlightedRow().Name)
	}
	m.key(key("left"))
	if got := strings.Join(visibleNames(m), ","); got != "api,web" {
		t.Fatalf("collapsed again: %s", got)
	}
}

func TestAttentionSortsGroupsByNeediestMember(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.attentionFirst = true
	rows := groupedRows()
	rows = append(rows, app.Row{Repository: repository.Repository{Name: "aaa", Path: "/home/mark/projects/aaa"}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true, Behind: 1}})
	m.applySnapshot(app.Snapshot{Rows: rows})
	if got := visibleNames(m)[0]; got != "api" {
		t.Fatalf("group with a dirty child should rank first, got %s", got)
	}
}

func TestSearchRevealsChildUnderContextParent(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.filter = "fix-auth"
	if got := strings.Join(visibleNames(m), ","); got != "api,fix-auth" {
		t.Fatalf("filtered: %s", got)
	}
	if !m.isContext(m.visibleRows()[0]) || m.isContext(m.visibleRows()[1]) {
		t.Fatal("parent should be context, child a match")
	}
	m.key(key("a"))
	if len(m.selected) != 1 || !m.selected["/home/mark/projects/api/.claude/worktrees/fix-auth"] {
		t.Fatalf("select-all must skip context parents: %v", m.selected)
	}
}

func TestStaleRowsAreNotSelectable(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.key(key("right"))
	for i, index := range m.visibleRows() {
		if m.rows[index].Name == "old" {
			m.highlight = i
		}
	}
	m.key(key(" "))
	if len(m.selected) != 0 || !strings.Contains(m.message, "Clean up") {
		t.Fatalf("stale row selected: %v, message %q", m.selected, m.message)
	}
}

func TestCollapsedParentShowsBadgeAndChildrenShowTree(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	view := m.View().Content
	if !strings.Contains(view, "⑂2") || !strings.Contains(view, "1 stale") {
		t.Fatalf("badge missing:\n%s", view)
	}
	m.key(key("right"))
	view = m.View().Content
	if !strings.Contains(view, "├─ ") || !strings.Contains(view, "└─ ") || !strings.Contains(view, "stale") {
		t.Fatalf("tree missing:\n%s", view)
	}
}
```
(import `tea "charm.land/bubbletea/v2"`; `key(...)` is the existing test helper that builds a `tea.KeyPressMsg` — if it only handles printable keys, extend it so `key("right")`/`key("left")` produce `tea.KeyPressMsg{Code: tea.KeyRight}` / `tea.KeyLeft`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui -run 'Group|Attention|Search|Stale|Badge' -v`
Expected: FAIL (`isContext` undefined, wrong visible rows).

- [ ] **Step 3: Implement `internal/tui/groups.go`**

```go
package tui

import (
	"fmt"
	"sort"

	"github.com/mark-lvl/gitperch/internal/app"
)

// groupKey is the parent row's path; rows without worktree information stand
// alone.
func groupKey(row app.Row) string {
	if row.Worktree != nil && row.Worktree.MainPath != "" {
		return row.Worktree.MainPath
	}
	return row.Path
}

func isParent(row app.Row) bool { return groupKey(row) == row.Path }

func (m *Model) matchesView(row app.Row) bool { return matches(row, m.filter) && m.inScope(row) }

// narrowed reports whether search or a scope hides rows, in which case
// matching children appear without expanding their group.
func (m *Model) narrowed() bool { return m.filter != "" || m.scope != 0 }

func (m *Model) isContext(index int) bool { return !m.matchesView(m.rows[index]) }

// visibleRows orders groups as the flat list was ordered before (attention,
// then name and path), keeps each parent first and lists children by path.
func (m *Model) visibleRows() []int {
	type group struct {
		parent   int
		children []int
		matched  bool
		rank     int
	}
	groups := map[string]*group{}
	var keys []string
	for i, row := range m.rows {
		k := groupKey(row)
		g := groups[k]
		if g == nil {
			g = &group{parent: -1}
			groups[k] = g
			keys = append(keys, k)
		}
		if isParent(row) {
			g.parent = i
		} else {
			g.children = append(g.children, i)
		}
		if m.matchesView(row) {
			g.matched = true
			g.rank = max(g.rank, m.attentionRank(row))
		}
	}
	var ordered []*group
	for _, k := range keys {
		g := groups[k]
		if !g.matched {
			continue
		}
		if g.parent < 0 { // parent missing from the snapshot: promote first child
			g.parent, g.children = g.children[0], g.children[1:]
		}
		sort.SliceStable(g.children, func(i, j int) bool { return m.rows[g.children[i]].Path < m.rows[g.children[j]].Path })
		ordered = append(ordered, g)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := m.rows[ordered[i].parent], m.rows[ordered[j].parent]
		if m.attentionFirst && ordered[i].rank != ordered[j].rank {
			return ordered[i].rank > ordered[j].rank
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	indices := make([]int, 0, len(m.rows))
	for _, g := range ordered {
		indices = append(indices, g.parent)
		expanded := m.expanded[m.rows[g.parent].Path]
		for _, c := range g.children {
			// Search and scopes reveal matching children; otherwise a group
			// shows its children only when expanded.
			if m.narrowed() && m.matchesView(m.rows[c]) || !m.narrowed() && expanded {
				indices = append(indices, c)
			}
		}
	}
	return indices
}

func (m *Model) treePrefix(pos int, indices []int) string {
	row := m.rows[indices[pos]]
	if isParent(row) {
		return ""
	}
	if pos+1 < len(indices) && !isParent(m.rows[indices[pos+1]]) && groupKey(m.rows[indices[pos+1]]) == groupKey(row) {
		return "├─ "
	}
	return "└─ "
}

// groupBadge summarizes a collapsed parent's worktrees, e.g. "⑂2 · 1 stale".
func (m *Model) groupBadge(row app.Row) string {
	if !isParent(row) || m.expanded[row.Path] {
		return ""
	}
	linked, stale := 0, 0
	for _, other := range m.rows {
		if other.Path != row.Path && groupKey(other) == row.Path {
			linked++
			if other.Worktree != nil && other.Worktree.Prunable {
				stale++
			}
		}
	}
	if linked == 0 {
		return ""
	}
	badge := fmt.Sprintf("%s%d", m.symbols().worktree, linked)
	if stale > 0 {
		badge += fmt.Sprintf(" · %d stale", stale)
	}
	return badge
}
```
Delete the old `visibleRows` body in `model.go` (this file replaces it). In `New`, initialise `expanded: make(map[string]bool)`.

- [ ] **Step 4: Wire keys, selection and guards in `internal/tui/model.go`**

In the workspace `switch key` add:
```go
	case "right":
		if row := m.highlightedRow(); row != nil {
			m.expanded[groupKey(*row)] = true
		}
	case "left":
		if row := m.highlightedRow(); row != nil {
			parent := groupKey(*row)
			if !isParent(*row) {
				for i, index := range m.visibleRows() {
					if m.rows[index].Path == parent {
						m.highlight = i
					}
				}
			} else {
				delete(m.expanded, parent)
			}
			m.keepHighlightVisible()
		}
```
Replace the Space case:
```go
	case " ", "space":
		if row := m.highlightedRow(); row != nil {
			if !row.Selectable() {
				m.message = "Stale worktrees and bare repositories are handled by Clean up (c)"
				return nil
			}
			m.selected[row.Path] = !m.selected[row.Path]
			if !m.selected[row.Path] {
				delete(m.selected, row.Path)
			}
		}
```
In the `a` case, compute `allSelected` and the selection over `indices` filtered by `!m.isContext(index) && m.rows[index].Selectable()`.
In `applySnapshot`, keep a previous selection only when `oldSelection[row.Path] && m.matchesView(row) && row.Selectable()`.
In `executeCommand` (`palette.go`) `fetch/push/pull`: when falling back to the highlighted row, require `row.Selectable()`; otherwise set `m.message = "Stale worktrees and bare repositories are handled by Clean up (c)"` and return.
In `launchShell` / `launchLazyGit`, after the nil check: `if !row.Selectable() { m.message = "This worktree has no directory to open"; return nil }`.
In `ensureDetail` and `ensurePatch`, return `nil` when `!row.Selectable()`.

- [ ] **Step 5: Render tree, badge and states in `internal/tui/view.go`**

At the top of `attentionRank(row)`:
```go
	if w := row.Worktree; w != nil {
		if w.Bare {
			return 0
		}
		if w.Prunable {
			return 2
		}
	}
```
At the top of `primaryStatus`'s state switch (after the results block):
```go
	if w := row.Worktree; w != nil && w.Bare {
		return "bare repository", muted
	}
	if w := row.Worktree; w != nil && w.Prunable {
		return "◌ stale · directory missing", amber
	}
```
and add `case row.Worktree != nil && row.Worktree.Locked: return "⊘ locked", muted` immediately before `case s.Detached:`.
Change `tableRow(row, position, highlighted, w)` to `tableRow(row app.Row, position int, highlighted bool, tree string, w int)`; in `repositoryList` pass `m.treePrefix(pos, indices)`. Inside `tableRow`, build the name cell from `tree + gitcli.SafeText(row.Name)`, and when `badge := m.groupBadge(row); badge != ""` append `" " + m.style(badge, muted, false)` within the same `cell(..., c.name)` width (truncate the name, never the badge). Render context rows (`m.isContext(index)`) with the name in `muted` instead of `ink` — pass a `context bool` alongside `tree` or compute it from the row's index in `repositoryList`.
Add `worktree string` to the `icons` struct in `theme.go`: `"⑂"` for unicode/nerd sets, `"wt"` for ascii.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/tui -v -run 'Group|Attention|Search|Stale|Badge'` then `go test ./internal/tui`
Expected: PASS. Existing capture tests still pass because `dashboardRows()` has no worktree info (all groups of one).

- [ ] **Step 7: Commit**

```bash
make check
git add internal/tui
git commit -m "feat(tui): group linked worktrees under their main repository

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Worktree details tab, help, captures and docs for phase 1

**Files:**
- Modify: `internal/tui/detail.go` (`case 3` lists the group)
- Modify: `internal/tui/view.go` (help text near line 639: `→/← expand/collapse worktrees`)
- Modify: `internal/tui/capture_test.go` (`captureRows` gains a group; new `worktrees-110x35` capture)
- Modify: `docs/usage.md`, `docs/ui.md`, `README.md`, `CHANGELOG.md`
- Regenerate: `docs/captures/*`
- Test: `internal/tui/groups_test.go` (append)

**Interfaces:**
- Consumes: `groupKey`, `isParent` (Task 3).
- Produces: nothing new for later tasks.

- [ ] **Step 1: Write the failing test**

Append to `internal/tui/groups_test.go`:
```go
func TestWorktreeTabListsGroup(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m.details, m.detailTab = true, 3
	view := m.View().Content
	for _, want := range []string{"Worktrees", "fix-auth", "2 changed", "old", "stale"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/tui -run TestWorktreeTabListsGroup -v`
Expected: FAIL (missing "Worktrees").

- [ ] **Step 3: Implement the tab**

In `repositoryDetails`, after the existing `case 3` metadata lines:
```go
		lines = append(lines, "", m.style(" Worktrees", accent, true))
		key := groupKey(*row)
		for _, other := range m.rows {
			if groupKey(other) != key {
				continue
			}
			state, color := worktreeLabel(other)
			kind := "linked"
			if isParent(other) {
				kind = "main"
			}
			if w := other.Worktree; w != nil {
				switch {
				case w.Bare:
					state, color = "bare", muted
				case w.Prunable:
					state, color = "stale · "+w.PrunableReason, amber
				case w.Locked && w.LockReason != "":
					state += " · locked: " + w.LockReason
				case w.Locked:
					state += " · locked"
				}
				if w.OutsideRoots {
					kind += ", outside roots"
				}
			}
			lines = append(lines, " "+gitcli.SafeText(other.Name)+"  "+m.style(branchLabel(other), muted, false)+"  "+m.style(state, color, false)+"  "+m.style("("+kind+")", muted, false))
			lines = append(lines, "   "+m.style(gitcli.SafeText(other.Path), muted, false))
		}
```
Since `case 3` no longer needs loaded details for this list, make sure the "Loading repository context…" line does not hide it (it is appended after, which is fine).

- [ ] **Step 4: Run tests, then update captures**

Run: `go test ./internal/tui -v -run TestWorktreeTabListsGroup` → PASS.
In `capture_test.go`, give `captureRows()` two linked worktrees under `design-system` (one `fix-tokens` clean on branch `fix-tokens`, one stale `old-spike` with `PrunableReason: "gitdir file points to non-existent location"`), with `Worktree.MainPath` set on all three rows. Add a `worktrees-110x35` capture to `TestWorkspaceRenderCaptures`' sibling `TestOverlayRenderCaptures` (or a new subtest in the same function family) that sets `m.expanded["/home/mark/projects/design-system"] = true` before rendering. Then:
```bash
ansi=$(mktemp -d)
UPDATE_RENDERS=1 GITPERCH_ANSI_DIR="$ansi" go test ./internal/tui -run 'TestWorkspaceRenderCaptures|TestOverlayRenderCaptures|TestScanningRenderCapture'
python scripts/render-captures.py "$ansi"
```
Review the regenerated `.txt` diffs by eye: badge `⑂2 · 1 stale` on design-system in collapsed captures; tree lines in the expanded one. If `pillow`/`pyte` are unavailable, commit the text captures and say so in the task report.

- [ ] **Step 5: Docs**

- `docs/usage.md`: a "Worktrees" section — grouping, `→`/`←`, stale/locked/bare states, worktrees outside roots, Git ≥ 2.36 requirement, and the JSON `worktree` object (field list from Task 2, schema still 1).
- `docs/ui.md`: add the `worktrees-110x35` capture to the capture list; mention child rows and badges.
- `README.md`: features list — "Worktree inventory: every linked worktree, including nested, outside-root and stale ones, grouped under its repository"; remove "Group worktrees created by agents under their parent repository" from the roadmap.
- `CHANGELOG.md` under `## [Unreleased]`:
  ```markdown
  ### Added

  - The workspace groups linked worktrees under their main repository, including
    worktrees nested inside a repository, outside the scanned roots, or whose
    directory no longer exists. `→`/`←` expand and collapse a group, and the
    details Worktree section lists the group.
  - `gitperch status --json` reports a `worktree` object per repository (schema
    version 1, additive).
  ```

- [ ] **Step 6: Commit**

```bash
make check
git add internal/tui docs README.md CHANGELOG.md
git commit -m "feat(tui): list a repository's worktrees in details

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

# Phase 2 — Worktree cleanup

### Task 5: Cleanup read primitives in the Git layer

**Files:**
- Create: `internal/git/cleanup.go`
- Modify: `internal/git/metadata.go` (`ResolveFetch` delegates to `ResolveRemote`)
- Modify: `internal/git/service.go`
- Test: `internal/git/cleanup_test.go`

**Interfaces:**
- Consumes: `Runner.Run`, `Metadata`, `objectID`.
- Produces:
  ```go
  var ErrNoDefaultRef = errors.New("default branch unknown")
  func (r Runner) ResolveRemote(ctx context.Context, path string, m Metadata, remote string) (FetchTarget, error)
  func (r Runner) RemoteDefaultRef(ctx context.Context, path, remote string) (string, error) // "refs/remotes/origin/main" or ErrNoDefaultRef
  func (r Runner) ResolveCommit(ctx context.Context, path, ref string) (string, error)      // ref must start with "refs/"
  func (r Runner) IsAncestor(ctx context.Context, path, oid, ref string) (bool, error)
  func (r Runner) IgnoredFiles(ctx context.Context, path string) ([]string, error)          // may return ErrOutputLimit
  // Service passthroughs (all Read): ResolveRemote, RemoteDefaultRef, ResolveCommit, IsAncestor, IgnoredFiles, Worktrees
  ```

- [ ] **Step 1: Write the failing tests**

`internal/git/cleanup_test.go`:
```go
package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoWithRemote(t *testing.T) (repo, remote string) {
	t.Helper()
	repo = disposable(t)
	write(t, filepath.Join(repo, "f"), "x\n")
	commit(t, repo)
	remote = filepath.Join(t.TempDir(), "remote.git")
	gitCmd(t, filepath.Dir(remote), "init", "--bare", "-b", "main", remote)
	gitCmd(t, repo, "remote", "add", "origin", remote)
	gitCmd(t, repo, "push", "-u", "origin", "main")
	return repo, remote
}

func TestRemoteDefaultRef(t *testing.T) {
	repo, _ := repoWithRemote(t)
	r := Runner{}
	if _, err := r.RemoteDefaultRef(context.Background(), repo, "origin"); !errors.Is(err, ErrNoDefaultRef) {
		t.Fatalf("missing origin/HEAD: %v", err)
	}
	gitCmd(t, repo, "remote", "set-head", "origin", "main")
	ref, err := r.RemoteDefaultRef(context.Background(), repo, "origin")
	if err != nil || ref != "refs/remotes/origin/main" {
		t.Fatalf("ref %q, %v", ref, err)
	}
}

func TestResolveCommitAndIsAncestor(t *testing.T) {
	repo, _ := repoWithRemote(t)
	r := Runner{}
	head, err := r.ResolveCommit(context.Background(), repo, "refs/heads/main")
	if err != nil || !objectID(head) {
		t.Fatalf("head %q, %v", head, err)
	}
	if _, err := r.ResolveCommit(context.Background(), repo, "--help"); err == nil {
		t.Fatal("non-ref argument accepted")
	}
	if _, err := r.ResolveCommit(context.Background(), repo, "refs/heads/missing"); err == nil {
		t.Fatal("missing ref resolved")
	}
	gitCmd(t, repo, "checkout", "-b", "feature")
	write(t, filepath.Join(repo, "g"), "y\n")
	commit(t, repo)
	feature, _ := r.ResolveCommit(context.Background(), repo, "refs/heads/feature")
	if ok, err := r.IsAncestor(context.Background(), repo, head, "refs/heads/feature"); err != nil || !ok {
		t.Fatalf("main in feature: %v %v", ok, err)
	}
	if ok, err := r.IsAncestor(context.Background(), repo, feature, "refs/remotes/origin/main"); err != nil || ok {
		t.Fatalf("feature in origin/main: %v %v", ok, err)
	}
	if _, err := r.IsAncestor(context.Background(), repo, "zzz", "refs/heads/main"); err == nil {
		t.Fatal("invalid oid accepted")
	}
}

func TestIgnoredFiles(t *testing.T) {
	repo := disposable(t)
	write(t, filepath.Join(repo, ".gitignore"), ".env\nnode_modules/\n")
	commit(t, repo)
	r := Runner{}
	if got, err := r.IgnoredFiles(context.Background(), repo); err != nil || len(got) != 0 {
		t.Fatalf("clean: %v %v", got, err)
	}
	write(t, filepath.Join(repo, ".env"), "SECRET=1\n")
	if err := os.MkdirAll(filepath.Join(repo, "node_modules", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "node_modules", "a", "b"), "1")
	got, err := r.IgnoredFiles(context.Background(), repo)
	if err != nil || strings.Join(got, ",") != ".env,node_modules/" {
		t.Fatalf("ignored: %v %v", got, err)
	}
}

func TestResolveFetchStillValidatesRemote(t *testing.T) {
	repo, _ := repoWithRemote(t)
	r := Runner{}
	m, err := r.Metadata(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	a, errA := r.ResolveFetch(context.Background(), repo, m)
	b, errB := r.ResolveRemote(context.Background(), repo, m, "origin")
	if errA != nil || errB != nil || a != b {
		t.Fatalf("fetch %+v %v, remote %+v %v", a, errA, b, errB)
	}
	if _, err := r.ResolveRemote(context.Background(), repo, m, "nope"); err == nil {
		t.Fatal("unknown remote accepted")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/git -run 'RemoteDefaultRef|ResolveCommit|IgnoredFiles|ResolveFetchStill' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Refactor `ResolveFetch` in `metadata.go`**

Split the existing function: keep remote selection in `ResolveFetch`, move everything from `valid := false` through the URL lookup into `ResolveRemote`:
```go
func (r Runner) ResolveFetch(ctx context.Context, path string, m Metadata) (FetchTarget, error) {
	remote := m.Value("branch." + m.Status.Branch + ".remote")
	if remote == "" {
		if len(m.Remotes) != 1 {
			return FetchTarget{}, fmt.Errorf("no upstream remote and no sole unambiguous remote")
		}
		remote = m.Remotes[0]
	}
	return r.ResolveRemote(ctx, path, m, remote)
}

// ResolveRemote validates a named remote for a safe fetch: configured,
// external, not a mirror, and with conventional tracking refspecs only.
func (r Runner) ResolveRemote(ctx context.Context, path string, m Metadata, remote string) (FetchTarget, error) {
	valid := false
	// ... existing body from `for _, name := range m.Remotes` to the final return, unchanged
}
```

- [ ] **Step 4: Implement `internal/git/cleanup.go` (reads only)**

```go
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrNoDefaultRef = errors.New("default branch unknown")

func (r Runner) RemoteDefaultRef(ctx context.Context, path, remote string) (string, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return "", fmt.Errorf("invalid remote")
	}
	out, err := r.Run(ctx, path, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", ErrNoDefaultRef
		}
		return "", err
	}
	ref := strings.TrimSpace(string(out.Stdout))
	if !strings.HasPrefix(ref, "refs/remotes/"+remote+"/") || ref == "refs/remotes/"+remote+"/HEAD" {
		return "", ErrNoDefaultRef
	}
	return ref, nil
}

func (r Runner) ResolveCommit(ctx context.Context, path, ref string) (string, error) {
	if !strings.HasPrefix(ref, "refs/") {
		return "", fmt.Errorf("invalid reference")
	}
	out, err := r.Run(ctx, path, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out.Stdout))
	if !objectID(oid) {
		return "", fmt.Errorf("invalid commit")
	}
	return oid, nil
}

// IsAncestor reports whether oid is reachable from ref; exit status 1 means
// "no", any other failure is an error.
func (r Runner) IsAncestor(ctx context.Context, path, oid, ref string) (bool, error) {
	if !objectID(oid) || !strings.HasPrefix(ref, "refs/") {
		return false, fmt.Errorf("invalid ancestry check")
	}
	_, err := r.Run(ctx, path, "merge-base", "--is-ancestor", oid, ref)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// IgnoredFiles lists ignored paths in a worktree, collapsing wholly ignored
// directories, so a cleanup can refuse to delete them with the worktree.
func (r Runner) IgnoredFiles(ctx context.Context, path string) ([]string, error) {
	out, err := r.Run(ctx, path, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range bytes.Split(out.Stdout, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files, nil
}
```
If `rev-parse` on this Git rejects `--end-of-options` together with `--verify`, drop it: the `refs/` prefix check already prevents option injection.

Add to `service.go`:
```go
func (s Service) Worktrees(ctx context.Context, path string) ([]Worktree, error) {
	return s.Read.Worktrees(ctx, path)
}
func (s Service) ResolveRemote(ctx context.Context, path string, m Metadata, remote string) (FetchTarget, error) {
	return s.Read.ResolveRemote(ctx, path, m, remote)
}
func (s Service) RemoteDefaultRef(ctx context.Context, path, remote string) (string, error) {
	return s.Read.RemoteDefaultRef(ctx, path, remote)
}
func (s Service) ResolveCommit(ctx context.Context, path, ref string) (string, error) {
	return s.Read.ResolveCommit(ctx, path, ref)
}
func (s Service) IsAncestor(ctx context.Context, path, oid, ref string) (bool, error) {
	return s.Read.IsAncestor(ctx, path, oid, ref)
}
func (s Service) IgnoredFiles(ctx context.Context, path string) ([]string, error) {
	return s.Read.IgnoredFiles(ctx, path)
}
```

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/git -v -run 'RemoteDefaultRef|ResolveCommit|IgnoredFiles|ResolveFetch'` → PASS; `make check`.
```bash
git add internal/git
git commit -m "feat(git): add read-only checks for merged cleanup

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Worktree mutations

**Files:**
- Modify: `internal/git/cleanup.go`, `internal/git/service.go`
- Test: `internal/git/cleanup_test.go` (append)

**Interfaces:**
- Produces:
  ```go
  func (r Runner) PruneWorktrees(ctx context.Context, path string) error
  func (r Runner) RemoveWorktree(ctx context.Context, path, worktree string) error // worktree must be absolute; never --force
  // Service passthroughs (Write): PruneWorktrees, RemoveWorktree
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestPruneWorktreesKeepsLockedRecords(t *testing.T) {
	repo := disposable(t)
	write(t, filepath.Join(repo, "f"), "x\n")
	commit(t, repo)
	base := t.TempDir()
	gitCmd(t, repo, "worktree", "add", "-b", "a", filepath.Join(base, "a"))
	gitCmd(t, repo, "worktree", "add", "--lock", "-b", "b", filepath.Join(base, "b"))
	os.RemoveAll(filepath.Join(base, "a"))
	os.RemoveAll(filepath.Join(base, "b"))
	if err := (Runner{}).PruneWorktrees(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	got, _ := (Runner{}).Worktrees(context.Background(), repo)
	if len(got) != 2 || !got[1].Locked {
		t.Fatalf("after prune: %+v", got)
	}
}

func TestRemoveWorktreeRefusesDirtyAndForceIsNeverUsed(t *testing.T) {
	repo := disposable(t)
	write(t, filepath.Join(repo, "f"), "x\n")
	commit(t, repo)
	wt := filepath.Join(t.TempDir(), "wt")
	gitCmd(t, repo, "worktree", "add", "-b", "wt", wt)
	write(t, filepath.Join(wt, "new"), "untracked")
	r := Runner{}
	if err := r.RemoveWorktree(context.Background(), repo, wt); err == nil {
		t.Fatal("dirty worktree removed")
	}
	if _, err := os.Stat(filepath.Join(wt, "new")); err != nil {
		t.Fatal("untracked file lost")
	}
	os.Remove(filepath.Join(wt, "new"))
	if err := r.RemoveWorktree(context.Background(), repo, "relative/wt"); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := r.RemoveWorktree(context.Background(), repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still present: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/git -run 'PruneWorktrees|RemoveWorktree' -v` → FAIL to compile.

- [ ] **Step 3: Implement**

Append to `cleanup.go`:
```go
// PruneWorktrees drops administrative records of worktrees whose directory
// is gone. Git never prunes locked records.
func (r Runner) PruneWorktrees(ctx context.Context, path string) error {
	_, err := r.Run(ctx, path, "worktree", "prune")
	return err
}

// RemoveWorktree deletes a clean linked worktree. Without --force Git refuses
// modified or untracked files, locked worktrees and the main worktree.
func (r Runner) RemoveWorktree(ctx context.Context, path, worktree string) error {
	if !filepath.IsAbs(worktree) {
		return fmt.Errorf("invalid reviewed worktree path")
	}
	_, err := r.Run(ctx, path, "worktree", "remove", worktree)
	return err
}
```
(import `path/filepath`.) Add Write passthroughs to `service.go`:
```go
func (s Service) PruneWorktrees(ctx context.Context, path string) error {
	return s.Write.PruneWorktrees(ctx, path)
}
func (s Service) RemoveWorktree(ctx context.Context, path, worktree string) error {
	return s.Write.RemoveWorktree(ctx, path, worktree)
}
```

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/git -v -run 'PruneWorktrees|RemoveWorktree'` → PASS; `make check`.
```bash
git add internal/git
git commit -m "feat(git): prune stale and remove clean worktrees without force

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Cleanup planning and execution for worktrees

**Files:**
- Create: `internal/app/cleanup.go`
- Modify: `internal/app/actions.go` (`Cleanup` label, `Event.Item`, `pendingCleanup` fields)
- Test: `internal/app/cleanup_test.go`

**Interfaces:**
- Consumes: Task 5/6 Git methods via `gitcli.Service`; `Actions.lockFor`, `Actions.active/nextID/mu`.
- Produces:
  ```go
  const Cleanup Action = "clean up"
  // Event gains: Item string // cleanup item ID; "" for fetch/push/pull
  type CleanupKind string
  const (
      CleanupGroup   CleanupKind = "inspect"
      PruneStale     CleanupKind = "prune stale worktrees"
      RemoveWorktree CleanupKind = "remove worktree"
      DeleteBranch   CleanupKind = "delete branch"
  )
  type CleanupItem struct {
      ID       string
      Group    string   // main worktree path as Git reports it
      Kind     CleanupKind
      Path     string   // worktree path; group path for prune and branches
      Branch   string
      OID      string
      Stale    []string // PruneStale: reviewed stale worktree paths, sorted
      Base     string   // e.g. refs/remotes/origin/main
      BaseName string   // e.g. origin/main, or "main (local default)"
      Eligible bool
      Reason   string
      Failed   bool
  }
  type CleanupPreview struct {
      ID    uint64
      Items []CleanupItem
  }
  type CleanupGit interface {
      ActionGit
      Worktrees(context.Context, string) ([]gitcli.Worktree, error)
      ResolveRemote(context.Context, string, gitcli.Metadata, string) (gitcli.FetchTarget, error)
      RemoteDefaultRef(context.Context, string, string) (string, error)
      ResolveCommit(context.Context, string, string) (string, error)
      IsAncestor(context.Context, string, string, string) (bool, error)
      IgnoredFiles(context.Context, string) ([]string, error)
      PruneWorktrees(context.Context, string) error
      RemoveWorktree(context.Context, string, string) error
  }
  func (a *Actions) PlanCleanup(ctx context.Context, paths []string) (CleanupPreview, error)
  func (a *Actions) ExecuteCleanup(ctx context.Context, id uint64, chosen []string, emit func(Event)) ([]Event, error)
  ```
  `Discard(id)` must also discard a pending cleanup with that ID.

- [ ] **Step 1: Write the failing tests**

`internal/app/cleanup_test.go`:
```go
package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// cleanupRepo returns a repository with origin/HEAD set and a merged, clean
// linked worktree "merged" plus an unmerged one "ahead".
func cleanupRepo(t *testing.T) (repo, merged, ahead string) {
	t.Helper()
	repo, _ = actionTestRepoWithRemote(t)
	// Git 2.48+ would recreate origin/HEAD on fetch; older Git ignores this key.
	actionGit(t, repo, "config", "remote.origin.followRemoteHEAD", "never")
	actionGit(t, repo, "remote", "set-head", "origin", "main")
	base := t.TempDir()
	merged, ahead = filepath.Join(base, "merged"), filepath.Join(base, "ahead")
	actionGit(t, repo, "worktree", "add", "-b", "merged", merged)
	actionGit(t, repo, "worktree", "add", "-b", "ahead", ahead)
	actionTestWrite(t, filepath.Join(ahead, "new"), "work\n")
	actionTestCommit(t, ahead, "unmerged work")
	return repo, merged, ahead
}

func itemFor(t *testing.T, p CleanupPreview, kind CleanupKind, path string) CleanupItem {
	t.Helper()
	for _, item := range p.Items {
		if item.Kind == kind && item.Path == path {
			return item
		}
	}
	t.Fatalf("no %s item for %s in %+v", kind, path, p.Items)
	return CleanupItem{}
}

func TestCleanupPlansMergedWorktreeAndKeepsOthers(t *testing.T) {
	repo, merged, ahead := cleanupRepo(t)
	dirty := filepath.Join(t.TempDir(), "dirty")
	actionGit(t, repo, "worktree", "add", "-b", "dirty", dirty)
	actionTestWrite(t, filepath.Join(dirty, "scratch"), "x")
	ignored := filepath.Join(t.TempDir(), "ignored")
	actionGit(t, repo, "worktree", "add", "-b", "ignored", ignored)
	actionTestWrite(t, filepath.Join(repo, ".git", "info", "exclude"), ".env\n") // shared by all worktrees
	actionTestWrite(t, filepath.Join(ignored, ".env"), "SECRET=1\n")
	locked := filepath.Join(t.TempDir(), "locked")
	actionGit(t, repo, "worktree", "add", "--lock", "--reason", "agent session", "-b", "locked", locked)

	actions := NewActions(gitcli.Service{}, 2)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); !item.Eligible || item.Branch != "merged" || item.BaseName != "origin/main" {
		t.Fatalf("merged: %+v", item)
	}
	for path, reason := range map[string]string{ahead: "not merged into origin/main", dirty: "dirty", ignored: "ignored file", locked: "locked: agent session"} {
		if item := itemFor(t, preview, RemoveWorktree, path); item.Eligible || !strings.Contains(item.Reason, reason) {
			t.Fatalf("%s: %+v", path, item)
		}
	}
	for _, item := range preview.Items {
		if item.Path == repo && item.Kind == RemoveWorktree {
			t.Fatal("main worktree offered for removal")
		}
	}
}

func TestCleanupExecutesOnlyChosenItems(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	stale := filepath.Join(t.TempDir(), "stale")
	actionGit(t, repo, "worktree", "add", "-b", "stale", stale)
	os.RemoveAll(stale)
	actions := NewActions(gitcli.Service{}, 2)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prune := itemFor(t, preview, PruneStale, preview.Items[0].Group)
	if !prune.Eligible || len(prune.Stale) != 1 {
		t.Fatalf("prune: %+v", prune)
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, []string{prune.ID}, nil)
	if err != nil || len(results) != 1 || results[0].State != Succeeded || results[0].Item != prune.ID {
		t.Fatalf("results %+v %v", results, err)
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("unchosen worktree was removed")
	}
	if _, err := actions.ExecuteCleanup(context.Background(), preview.ID, nil, nil); err == nil {
		t.Fatal("a cleanup preview ran twice")
	}
}

func TestCleanupRevalidatesBeforeRemoving(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	item := itemFor(t, preview, RemoveWorktree, merged)
	actionTestWrite(t, filepath.Join(merged, "late"), "agent wrote this after review\n")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{item.ID}, nil)
	if len(results) != 1 || results[0].State != Skipped {
		t.Fatalf("dirtied worktree not skipped: %+v", results)
	}
	if _, err := os.Stat(filepath.Join(merged, "late")); err != nil {
		t.Fatal("late file lost")
	}
}

func TestCleanupDanglingDefaultRef(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/gone")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible || !strings.Contains(item.Reason, "default branch") {
		t.Fatalf("dangling origin/HEAD: %+v", item)
	}
}

func TestCleanupMissingDefaultRefSuggestsSetHead(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	actionGit(t, repo, "remote", "set-head", "origin", "--delete")
	actions := NewActions(gitcli.Service{}, 1)
	preview, _ := actions.PlanCleanup(context.Background(), []string{repo})
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible || !strings.Contains(item.Reason, "git remote set-head origin -a") {
		t.Fatalf("missing origin/HEAD: %+v", item)
	}
}

func TestCleanupLocalDefaultWithoutRemote(t *testing.T) {
	repo := actionTestRepo(t)
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	wt := filepath.Join(t.TempDir(), "wt")
	actionGit(t, repo, "worktree", "add", "-b", "wt", wt)
	preview, err := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, wt); !item.Eligible || item.BaseName != "main (local default)" {
		t.Fatalf("local default: %+v", item)
	}
}

type overflowIgnored struct{ gitcli.Service }

func (overflowIgnored) IgnoredFiles(context.Context, string) ([]string, error) {
	return nil, gitcli.ErrOutputLimit
}

func TestCleanupKeepsWorktreeWhenIgnoredListingOverflows(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	preview, err := NewActions(overflowIgnored{}, 1).PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, preview, RemoveWorktree, merged); item.Eligible || !strings.Contains(item.Reason, "too many ignored files") {
		t.Fatalf("overflow: %+v", item)
	}
}

func TestCleanupSharesTheActivePreview(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err == nil {
		t.Fatal("fetch planned while a cleanup preview is pending")
	}
	actions.Discard(preview.ID)
	if _, err := actions.Plan(context.Background(), Fetch, []string{repo}); err != nil {
		t.Fatalf("discard did not release the cleanup preview: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/app -run Cleanup -v` → FAIL to compile.

- [ ] **Step 3: Extend `internal/app/actions.go`**

```go
const (
	Fetch   Action = "fetch"
	Push    Action = "push"
	Pull    Action = "fast-forward pull"
	Cleanup Action = "clean up"
)
type Event struct {
	Path    string
	Item    string // cleanup item ID; empty for fetch, push and pull
	State   State
	Message string
	Status  repository.Status
}
```
Add to `Actions`: `pendingCleanup []plannedCleanup`. Change `Discard` to:
```go
func (a *Actions) Discard(id uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id != 0 && a.pendingID == id && (len(a.pending) > 0 || a.pendingCleanup != nil) {
		a.pending, a.pendingCleanup = nil, nil
		a.pendingID = 0
		a.active = false
	}
}
```
`Plan` keeps rejecting `Cleanup` (it is not in its allowed list).

- [ ] **Step 4: Implement `internal/app/cleanup.go`**

```go
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// (types from the Interfaces block above)

type plannedCleanup struct {
	item   CleanupItem
	common string
}

type cleanupBase struct {
	ref, name, reason string
	failed            bool
}

func kindOrder(k CleanupKind) int {
	return map[CleanupKind]int{CleanupGroup: 0, PruneStale: 1, RemoveWorktree: 2, DeleteBranch: 3}[k]
}

// PlanCleanup groups paths by common Git directory and plans each group:
// fetch, resolve the default ref, then classify worktrees (and, in phase 3,
// branches). Like Plan it holds the single active preview until Execute or
// Discard.
func (a *Actions) PlanCleanup(ctx context.Context, paths []string) (CleanupPreview, error) {
	git, ok := a.service.(CleanupGit)
	if !ok {
		return CleanupPreview{}, errors.New("cleanup is not available")
	}
	if len(paths) == 0 {
		return CleanupPreview{}, errors.New("select repositories explicitly before a batch action")
	}
	a.mu.Lock()
	if a.active {
		a.mu.Unlock()
		return CleanupPreview{}, errors.New("another preview or batch is active")
	}
	a.active = true
	a.nextID++
	id := a.nextID
	a.mu.Unlock()
	done := false
	defer func() {
		if !done {
			a.mu.Lock()
			a.active = false
			a.mu.Unlock()
		}
	}()

	type group struct {
		path     string
		metadata gitcli.Metadata
	}
	var groups []group
	var planned []plannedCleanup
	seen := map[string]bool{}
	paths = slices.Clone(paths)
	sort.Strings(paths)
	for _, path := range paths {
		m, err := a.service.Metadata(ctx, path)
		if err != nil {
			planned = append(planned, plannedCleanup{item: CleanupItem{ID: string(CleanupGroup) + "\x00" + path, Group: path, Kind: CleanupGroup, Path: path, Reason: gitcli.SafeText(err.Error()), Failed: true}})
			continue
		}
		if seen[m.CommonDir] {
			continue
		}
		seen[m.CommonDir] = true
		groups = append(groups, group{path, m})
	}
	results := make([][]plannedCleanup, len(groups))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(a.workers, max(1, len(groups))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lock := a.lockFor(groups[i].metadata.CommonDir)
				select {
				case <-ctx.Done():
					continue
				case <-lock:
				}
				results[i] = a.planCleanupGroup(ctx, git, groups[i].path, groups[i].metadata)
				lock <- struct{}{}
			}
		}()
	}
	for i := range groups {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return CleanupPreview{}, err
	}
	for _, r := range results {
		planned = append(planned, r...)
	}
	sort.SliceStable(planned, func(i, j int) bool {
		a, b := planned[i].item, planned[j].item
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if kindOrder(a.Kind) != kindOrder(b.Kind) {
			return kindOrder(a.Kind) < kindOrder(b.Kind)
		}
		return a.Path+a.Branch < b.Path+b.Branch
	})
	a.mu.Lock()
	a.pendingID = id
	a.pendingCleanup = planned
	if a.pendingCleanup == nil {
		a.pendingCleanup = []plannedCleanup{}
	}
	a.mu.Unlock()
	done = true
	preview := CleanupPreview{ID: id, Items: make([]CleanupItem, len(planned))}
	for i, p := range planned {
		preview.Items[i] = p.item
		preview.Items[i].Stale = slices.Clone(p.item.Stale)
	}
	return preview, nil
}

// resolveBase fetches the cleanup remote and finds the default ref. Without
// remotes the local main, else master, is the base.
func (a *Actions) resolveBase(ctx context.Context, git CleanupGit, path string, m gitcli.Metadata, fetch bool) cleanupBase {
	if len(m.Remotes) == 0 {
		for _, name := range []string{"main", "master"} {
			if _, err := git.ResolveCommit(ctx, path, "refs/heads/"+name); err == nil {
				return cleanupBase{ref: "refs/heads/" + name, name: name + " (local default)"}
			}
		}
		return cleanupBase{reason: "no remote and no local main or master branch"}
	}
	remote := ""
	switch {
	case slices.Contains(m.Remotes, "origin"):
		remote = "origin"
	case len(m.Remotes) == 1:
		remote = m.Remotes[0]
	default:
		return cleanupBase{reason: "several remotes and none named origin"}
	}
	if fetch {
		target, err := git.ResolveRemote(ctx, path, m, remote)
		if err == nil {
			err = git.Fetch(ctx, path, target)
		}
		if err != nil {
			return cleanupBase{reason: "preflight fetch failed: " + gitcli.SafeText(err.Error()), failed: true}
		}
	}
	ref, err := git.RemoteDefaultRef(ctx, path, remote)
	if errors.Is(err, gitcli.ErrNoDefaultRef) {
		return cleanupBase{reason: fmt.Sprintf("default branch unknown — run git remote set-head %s -a", remote)}
	}
	if err == nil {
		_, err = git.ResolveCommit(ctx, path, ref)
	}
	if err != nil {
		return cleanupBase{reason: "default branch unknown: " + gitcli.SafeText(err.Error())}
	}
	return cleanupBase{ref: ref, name: strings.TrimPrefix(ref, "refs/remotes/")}
}

func (a *Actions) planCleanupGroup(ctx context.Context, git CleanupGit, path string, m gitcli.Metadata) []plannedCleanup {
	base := a.resolveBase(ctx, git, path, m, true)
	worktrees, err := git.Worktrees(ctx, path)
	if err != nil {
		return []plannedCleanup{{common: m.CommonDir, item: CleanupItem{ID: string(CleanupGroup) + "\x00" + path, Group: path, Kind: CleanupGroup, Path: path, Reason: gitcli.SafeText(err.Error()), Failed: true}}}
	}
	group := worktrees[0].Path
	var out []plannedCleanup
	add := func(item CleanupItem) {
		item.Group, item.Base, item.BaseName = group, base.ref, base.name
		item.ID = string(item.Kind) + "\x00" + group + "\x00" + item.Path + "\x00" + item.Branch
		out = append(out, plannedCleanup{item: item, common: m.CommonDir})
	}
	var stale []string
	for _, wt := range worktrees {
		if wt.Main {
			continue
		}
		if wt.Prunable || missingDir(wt.Path) {
			if wt.Locked { // never pruned by Git; the user must unlock it first
				add(CleanupItem{Kind: RemoveWorktree, Path: wt.Path, Branch: wt.Branch, Reason: lockReason(wt) + " (directory missing)"})
			} else {
				stale = append(stale, wt.Path)
			}
			continue
		}
		item := CleanupItem{Kind: RemoveWorktree, Path: wt.Path, Branch: wt.Branch, OID: wt.HeadOID}
		item.Reason = a.worktreeBlocker(ctx, git, wt, base)
		item.Eligible = item.Reason == ""
		if item.Eligible {
			item.Reason = "merged into " + base.name
		}
		add(item)
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		add(CleanupItem{Kind: PruneStale, Path: group, Stale: stale, Eligible: true, Reason: fmt.Sprintf("%d stale worktree record(s)", len(stale))})
	}
	return out
}

func lockReason(wt gitcli.Worktree) string {
	if wt.LockReason != "" {
		return "locked: " + gitcli.SafeText(wt.LockReason)
	}
	return "locked"
}

// worktreeBlocker returns why a linked worktree must stay, or "" when it is
// clean, unlocked, free of ignored files and merged into the base.
func (a *Actions) worktreeBlocker(ctx context.Context, git CleanupGit, wt gitcli.Worktree, base cleanupBase) string {
	if wt.Locked {
		return lockReason(wt)
	}
	s := git.Inspect(ctx, wt.Path)
	switch {
	case s.Error != "":
		return "inspection failed: " + gitcli.SafeText(s.Error)
	case s.Operation != "":
		return "operation in progress: " + gitcli.SafeText(s.Operation)
	case s.Conflicts > 0:
		return fmt.Sprintf("%d conflicts", s.Conflicts)
	case s.Dirty():
		return fmt.Sprintf("dirty (%d files)", s.Changes+s.Untracked)
	case s.Unborn:
		return "no commits"
	}
	ignored, err := git.IgnoredFiles(ctx, wt.Path)
	switch {
	case errors.Is(err, gitcli.ErrOutputLimit):
		return "too many ignored files to list"
	case err != nil:
		return "could not list ignored files: " + gitcli.SafeText(err.Error())
	case len(ignored) > 0:
		shown := ignored[:min(3, len(ignored))]
		text := fmt.Sprintf("%d ignored file(s) (%s", len(ignored), gitcli.SafeText(strings.Join(shown, ", ")))
		if len(ignored) > 3 {
			text += ", …"
		}
		return text + ")"
	}
	if base.ref == "" {
		return base.reason
	}
	merged, err := git.IsAncestor(ctx, wt.Path, s.HeadOID, base.ref)
	if err != nil {
		return "merge check failed: " + gitcli.SafeText(err.Error())
	}
	if !merged {
		return "not merged into " + base.name
	}
	return ""
}

// ExecuteCleanup runs the chosen eligible items of the pending cleanup.
// Groups run concurrently; items within a group run in kind order under the
// group's lock, each revalidated immediately before it runs.
func (a *Actions) ExecuteCleanup(ctx context.Context, id uint64, chosen []string, emit func(Event)) ([]Event, error) {
	a.mu.Lock()
	if !a.active || a.pendingID != id || a.pendingCleanup == nil {
		a.mu.Unlock()
		return nil, errors.New("preview is no longer active")
	}
	items := a.pendingCleanup
	a.pendingCleanup = nil
	a.pendingID = 0
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.active = false; a.mu.Unlock() }()
	if emit == nil {
		emit = func(Event) {}
	}
	git := a.service.(CleanupGit)
	want := map[string]bool{}
	for _, id := range chosen {
		want[id] = true
	}
	byGroup := map[string][]plannedCleanup{}
	var order []string
	for _, p := range items {
		if !p.item.Eligible || !want[p.item.ID] {
			continue
		}
		if byGroup[p.item.Group] == nil {
			order = append(order, p.item.Group)
		}
		byGroup[p.item.Group] = append(byGroup[p.item.Group], p)
		emit(Event{Path: p.item.Path, Item: p.item.ID, State: Queued})
	}
	var mu sync.Mutex
	var results []Event
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(a.workers, max(1, len(order))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range jobs {
				for _, p := range byGroup[group] {
					event := a.executeCleanupItem(ctx, git, p, emit)
					emit(event)
					mu.Lock()
					results = append(results, event)
					mu.Unlock()
				}
			}
		}()
	}
	for _, g := range order {
		jobs <- g
	}
	close(jobs)
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool { return results[i].Item < results[j].Item })
	return results, ctx.Err()
}

func (a *Actions) executeCleanupItem(ctx context.Context, git CleanupGit, p plannedCleanup, emit func(Event)) Event {
	item := p.item
	result := Event{Path: item.Path, Item: item.ID}
	lock := a.lockFor(p.common)
	select {
	case <-ctx.Done():
		result.State, result.Message = Cancelled, ctx.Err().Error()
		return result
	case <-lock:
	}
	defer func() { lock <- struct{}{} }()
	if err := a.revalidateCleanup(ctx, git, item); err != nil {
		result.State, result.Message = Skipped, gitcli.SafeText(err.Error())
		if ctx.Err() != nil {
			result.State = Cancelled
		}
		return result
	}
	emit(Event{Path: item.Path, Item: item.ID, State: Running, Message: string(item.Kind)})
	var err error
	switch item.Kind {
	case PruneStale:
		err = git.PruneWorktrees(ctx, item.Group)
	case RemoveWorktree:
		err = git.RemoveWorktree(ctx, item.Group, item.Path)
	default:
		err = fmt.Errorf("unsupported cleanup item %q", item.Kind)
	}
	if err != nil {
		result.State = Failed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, gitcli.ErrOutputLimit) {
			result.State = OutcomeUnknown
		}
		result.Message = gitcli.SafeText(err.Error())
		return result
	}
	result.State = Succeeded
	switch item.Kind {
	case PruneStale:
		result.Message = fmt.Sprintf("pruned %d stale worktree record(s)", len(item.Stale))
	case RemoveWorktree:
		result.Message = "removed worktree " + gitcli.SafeText(item.Path)
	}
	return result
}

func (a *Actions) revalidateCleanup(ctx context.Context, git CleanupGit, item CleanupItem) error {
	worktrees, err := git.Worktrees(ctx, item.Group)
	if err != nil {
		return err
	}
	switch item.Kind {
	case PruneStale:
		var stale []string
		for _, wt := range worktrees {
			if wt.Prunable && !wt.Locked {
				stale = append(stale, wt.Path)
			}
		}
		sort.Strings(stale)
		if !slices.Equal(stale, item.Stale) {
			return errors.New("stale worktrees changed since review")
		}
		return nil
	case RemoveWorktree:
		for _, wt := range worktrees {
			if wt.Path != item.Path {
				continue
			}
			if wt.Main || wt.Prunable {
				return errors.New("worktree changed since review")
			}
			if wt.HeadOID != item.OID {
				return errors.New("worktree HEAD moved since review")
			}
			if blocker := a.worktreeBlocker(ctx, git, wt, cleanupBase{ref: item.Base, name: item.BaseName}); blocker != "" {
				return errors.New(blocker)
			}
			return nil
		}
		return errors.New("worktree no longer exists")
	}
	return fmt.Errorf("unsupported cleanup item %q", item.Kind)
}
```

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/app -run Cleanup -v` → PASS; then `go test -race ./internal/app`; `make check`.
```bash
git add internal/app
git commit -m "feat(app): plan and run reviewed worktree cleanup

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Cleanup review screen in the TUI

**Files:**
- Create: `internal/tui/cleanup.go`
- Modify: `internal/tui/model.go` (fields; `key` routes to `cleanupKey`; `c` key; `autoRefreshPaused`; `detailsContent` results loop)
- Modify: `internal/tui/actions.go` (`progressMsg`/`batchDoneMsg` key results with `resultKey`)
- Modify: `internal/tui/view.go` (`View` shows `cleanupView`; help line for `c`)
- Modify: `internal/tui/palette.go` (`cleanup` command, `commandHint`, `commandIcon`)
- Test: `internal/tui/cleanup_test.go`; captures; docs; CHANGELOG

**Interfaces:**
- Consumes: `Actions.PlanCleanup`, `Actions.ExecuteCleanup`, `Actions.Discard`, `app.CleanupItem`, `app.Event.Item`, `groupKey`.
- Produces:
  ```go
  type cleanupMsg struct { generation uint64; preview app.CleanupPreview; err error }
  func resultKey(e app.Event) string // e.Item if set, else e.Path
  func (m *Model) cleanupPaths() []string
  func (m *Model) prepareCleanup() tea.Cmd
  func (m *Model) cleanupKey(key string) tea.Cmd
  func (m *Model) cleanupView() tea.View
  // Model gains: cleanup *app.CleanupPreview; cleanupTicked map[string]bool; cleanupCursor int
  ```

- [ ] **Step 1: Write the failing tests**

`internal/tui/cleanup_test.go` — drive the real `app.Actions` against a temporary repository (helpers: copy `actionTestRepoWithRemote`-style setup locally with `exec.Command("git", ...)`, `GIT_CONFIG_GLOBAL` pointed at a temp file and `GIT_CONFIG_NOSYSTEM=1`):
```go
// cleanupModel loads a real temporary workspace and highlights repo.
func cleanupModel(t *testing.T, repo string) *Model {
	t.Helper()
	m := New(context.Background(), func(ctx context.Context) (app.Snapshot, error) {
		return app.Load(ctx, discovery.Options{Roots: []string{filepath.Dir(repo)}, MaxDepth: 2}, gitcli.Runner{}, 2)
	}, true)
	m.EnableActions(app.NewActions(gitcli.Service{}, 2))
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
	drain(m, m.Init())
	highlightPath(t, m, repo)
	return m
}

func TestCleanupReviewTogglesAndRuns(t *testing.T) {
	repo, merged, ahead := tuiCleanupRepo(t) // origin/HEAD set; merged clean worktree; ahead unmerged
	m := cleanupModel(t, repo)
	drain(m, m.key(key("c")))
	if m.cleanup == nil {
		t.Fatalf("no review opened: %q", m.message)
	}
	view := m.View().Content
	for _, want := range []string{"Clean up", "[x]", filepath.Base(merged), "merged into origin/main", "kept", filepath.Base(ahead), "not merged"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	m.key(key(" ")) // untick the only eligible item
	if strings.Contains(m.View().Content, "[x]") {
		t.Fatal("toggle did not untick")
	}
	m.key(key("enter"))
	if m.cleanup == nil || !strings.Contains(m.message, "Nothing ticked") {
		t.Fatalf("empty run should be refused: %q", m.message)
	}
	m.key(key(" "))
	drain(m, m.key(key("enter")))
	if _, err := os.Stat(merged); !os.IsNotExist(err) {
		t.Fatal("merged worktree not removed")
	}
	if !strings.Contains(m.message, "Clean up finished · 1 succeeded") {
		t.Fatalf("summary: %q", m.message)
	}
}

func TestCleanupEscDiscards(t *testing.T) {
	repo, merged, _ := tuiCleanupRepo(t)
	m := cleanupModel(t, repo)
	drain(m, m.key(key("c")))
	m.key(key("esc"))
	if m.cleanup != nil || m.message != "Cancelled" {
		t.Fatalf("esc: %v %q", m.cleanup, m.message)
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("cancelled cleanup removed a worktree")
	}
}

func TestCleanupTargetsWholeGroupFromChild(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.key(key("right"))
	m.highlight = 1
	if got := m.cleanupPaths(); len(got) != 1 || got[0] != "/home/mark/projects/api" {
		t.Fatalf("paths: %v", got)
	}
}
```
`tuiCleanupRepo(t)` mirrors `cleanupRepo` from `internal/app/cleanup_test.go` (repo under its own temp parent, local bare remote, `followRemoteHEAD=never`, `remote set-head origin main`, worktrees `merged` and `ahead` created outside that parent), using `exec.Command("git", "-C", ...)` with `GIT_CONFIG_GLOBAL` pointed at a temp file and `GIT_CONFIG_NOSYSTEM=1`. `drain(m, cmd)` runs a command and feeds resulting messages back into `m.Update` until none remain (follow the existing helper in `sync_actions_test.go` if one exists; otherwise implement: execute `cmd()`, unwrap `tea.BatchMsg`, loop with a bound of 1000 messages). `highlightPath` sets `m.highlight` to the visible position of a path.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/tui -run Cleanup -v` → FAIL to compile.

- [ ] **Step 3: Implement `internal/tui/cleanup.go`**

```go
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

type cleanupMsg struct {
	generation uint64
	preview    app.CleanupPreview
	err        error
}

func resultKey(e app.Event) string {
	if e.Item != "" {
		return e.Item
	}
	return e.Path
}

// cleanupPaths names one existing worktree per targeted group: the parent,
// or its first existing child when the parent is bare or stale.
func (m *Model) cleanupPaths() []string {
	groups := map[string]bool{}
	for path := range m.selected {
		for _, row := range m.rows {
			if row.Path == path {
				groups[groupKey(row)] = true
			}
		}
	}
	if len(groups) == 0 {
		if row := m.highlightedRow(); row != nil {
			groups[groupKey(*row)] = true
		}
	}
	var paths []string
	for key := range groups {
		chosen := ""
		for _, row := range m.rows {
			if groupKey(row) != key || !row.Selectable() {
				continue
			}
			if row.Path == key || chosen == "" {
				chosen = row.Path
			}
		}
		if chosen != "" {
			paths = append(paths, chosen)
		}
	}
	sort.Strings(paths)
	return paths
}

func (m *Model) prepareCleanup() tea.Cmd {
	if m.actions == nil {
		return nil
	}
	paths := m.cleanupPaths()
	if len(paths) == 0 {
		m.message = "Nothing to clean up here"
		return nil
	}
	if m.loadCancel != nil {
		m.loadCancel()
		m.loadCancel = nil
	}
	m.generation++
	m.refreshInterrupted = m.refreshInterrupted || m.loading
	m.loading = false
	m.actionGeneration++
	generation := m.actionGeneration
	ctx, cancel := context.WithCancel(m.ctx)
	m.actionCancel, m.actionCtx = cancel, ctx
	m.preparing = true
	m.details, m.help, m.palette = false, false, false
	m.message = "Fetching and checking " + plural(len(paths), "repository", "repositories") + " for cleanup"
	actions := m.actions
	return func() tea.Msg {
		preview, err := actions.PlanCleanup(ctx, paths)
		return cleanupMsg{generation, preview, err}
	}
}

func (m *Model) cleanupMessage(msg cleanupMsg) tea.Cmd {
	if msg.generation != m.actionGeneration {
		m.actions.Discard(msg.preview.ID)
		return nil
	}
	m.preparing = false
	if msg.err != nil {
		m.message = msg.err.Error()
		if m.actionCtx != nil && m.actionCtx.Err() != nil {
			m.message = "Cleanup preparation cancelled"
		}
		return m.resumeInterruptedRefresh()
	}
	if len(msg.preview.Items) == 0 {
		m.actions.Discard(msg.preview.ID)
		m.message = "Nothing to clean up"
		return m.resumeInterruptedRefresh()
	}
	m.cleanup = &msg.preview
	m.cleanupTicked = map[string]bool{}
	for _, item := range msg.preview.Items {
		if item.Eligible {
			m.cleanupTicked[item.ID] = true
		}
		if item.Failed {
			m.actionFailed = true
		}
	}
	m.cleanupCursor = 0
	m.message = ""
	return nil
}

func (m *Model) eligibleCleanup() []app.CleanupItem {
	var items []app.CleanupItem
	for _, item := range m.cleanup.Items {
		if item.Eligible {
			items = append(items, item)
		}
	}
	return items
}

func (m *Model) cleanupKey(key string) tea.Cmd {
	eligible := m.eligibleCleanup()
	switch key {
	case "j", "down":
		m.cleanupCursor = min(max(0, len(eligible)-1), m.cleanupCursor+1)
	case "k", "up":
		m.cleanupCursor = max(0, m.cleanupCursor-1)
	case " ", "space":
		if m.cleanupCursor < len(eligible) {
			id := eligible[m.cleanupCursor].ID
			m.cleanupTicked[id] = !m.cleanupTicked[id]
		}
	case "a":
		all := true
		for _, item := range eligible {
			all = all && m.cleanupTicked[item.ID]
		}
		for _, item := range eligible {
			m.cleanupTicked[item.ID] = !all
		}
	case "enter":
		var chosen []string
		for _, item := range eligible {
			if m.cleanupTicked[item.ID] {
				chosen = append(chosen, item.ID)
			}
		}
		if len(chosen) == 0 {
			m.message = "Nothing ticked · Space ticks an item, Esc closes"
			return nil
		}
		return m.runCleanup(chosen)
	case "esc", "q", "ctrl+c":
		m.actions.Discard(m.cleanup.ID)
		m.cleanup = nil
		if m.actionCancel != nil {
			m.actionCancel()
			m.actionCancel = nil
		}
		m.message = "Cancelled"
		return m.resumeInterruptedRefresh()
	}
	return nil
}

func (m *Model) runCleanup(chosen []string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	if m.actionCancel != nil {
		m.actionCancel()
	}
	m.actionCancel, m.actionCtx = cancel, ctx
	m.running = true
	m.runningAction = app.Cleanup
	m.actionTotal = len(chosen)
	m.message = ""
	m.results = map[string]app.Event{}
	id, generation, actions := m.cleanup.ID, m.actionGeneration, m.actions
	m.cleanup = nil
	events := make(chan app.Event, 64)
	m.events = events
	run := func() tea.Msg {
		results, err := actions.ExecuteCleanup(ctx, id, chosen, func(event app.Event) { events <- event })
		close(events)
		return batchDoneMsg{generation: generation, results: results, err: err}
	}
	return tea.Batch(run, m.nextEvent())
}

func (m *Model) cleanupLabel(item app.CleanupItem) string {
	switch item.Kind {
	case app.PruneStale:
		return fmt.Sprintf("prune %s", plural(len(item.Stale), "stale worktree record", "stale worktree records"))
	case app.RemoveWorktree:
		label := "remove worktree " + gitcli.SafeText(filepath.Base(item.Path))
		if item.Branch != "" {
			label += "  " + m.style(gitcli.SafeText(item.Branch), muted, false)
		}
		return label
	case app.DeleteBranch:
		return "delete branch " + gitcli.SafeText(item.Branch)
	}
	return gitcli.SafeText(m.targetName(item.Path))
}

// cleanupView lists ticked-by-default eligible items per repository, then
// what stays and why. Only ticked item IDs reach ExecuteCleanup.
func (m *Model) cleanupView() tea.View {
	eligible := m.eligibleCleanup()
	groups := map[string]bool{}
	for _, item := range m.cleanup.Items {
		groups[item.Group] = true
	}
	title := fmt.Sprintf("Clean up %s?", plural(len(eligible), "item", "items"))
	if len(groups) > 1 {
		title = fmt.Sprintf("Clean up %s in %s?", plural(len(eligible), "item", "items"), plural(len(groups), "repository", "repositories"))
	}
	w := min(96, m.width-4)
	inner := max(1, w-4)
	lines := []string{m.style(title, ink, true)}
	room := max(3, m.height-10)
	start := max(0, min(m.cleanupCursor-room/2, len(eligible)-room))
	group := ""
	for i := start; i < min(len(eligible), start+room); i++ {
		item := eligible[i]
		if item.Group != group {
			group = item.Group
			lines = append(lines, m.style(gitcli.SafeText(m.targetName(group)), accent, true))
		}
		box := "[ ]"
		if m.cleanupTicked[item.ID] {
			box = "[x]"
		}
		pointer := "  "
		if i == m.cleanupCursor {
			pointer = m.symbols().pointer + " "
		}
		lines = append(lines, m.between(pointer+box+" "+m.cleanupLabel(item), m.style(gitcli.SafeText(item.Reason), muted, false), inner))
	}
	kept := 0
	for _, item := range m.cleanup.Items {
		if item.Eligible {
			continue
		}
		if kept == 4 {
			lines = append(lines, m.style("  … more kept · d details after closing", muted, false))
			break
		}
		if kept == 0 {
			lines = append(lines, "", m.style("kept", amber, true))
		}
		kept++
		lines = append(lines, m.style("  "+m.cleanupLabel(item)+": "+gitcli.SafeText(item.Reason), amber, false))
	}
	footer := "Space toggle · ↑↓ move · " + m.symbols().enter + " clean up · Esc cancel"
	if len(eligible) == 0 {
		footer = "Nothing can be cleaned up safely · Esc close"
	}
	lines = append(lines, "", m.style(footer, muted, false))
	if m.width < 24 || m.height < 6 {
		return m.screen(lines)
	}
	return m.overlay(lines, w)
}
```
Import `path/filepath`. Kept items are shown only in the review; Diagnostics list executed items.

- [ ] **Step 4: Wire it in**

- `Model` fields: `cleanup *app.CleanupPreview`, `cleanupTicked map[string]bool`, `cleanupCursor int`.
- `actionMessage`: add `case cleanupMsg: return true, m.cleanupMessage(msg)`; in `progressMsg` and `batchDoneMsg` replace `m.results[msg.event.Path]` / `m.results[result.Path]` with `m.results[resultKey(...)]`.
- `key()`: after the `m.preview != nil` branch add `if m.cleanup != nil { return m.cleanupKey(key) }`; in the workspace switch add `case "c": return m.prepareCleanup()`; in the details switch add `case "c": return m.prepareCleanup()`.
- `autoRefreshPaused`: `return m.busy() || m.preview != nil || m.cleanup != nil || m.details || m.palette`.
- `View()`: `if m.cleanup != nil { return m.cleanupView() }` right after the preview check.
- `detailsContent` BATCH RESULTS: iterate results in sorted key order so removed worktrees (no longer rows) still show:
  ```go
  keys := make([]string, 0, len(m.results))
  for k := range m.results {
      keys = append(keys, k)
  }
  sort.Strings(keys)
  for _, k := range keys {
      result := m.results[k]
      content = append(content, gitcli.SafeText(result.Path)+": "+string(result.State)+" · "+gitcli.SafeText(result.Message))
  }
  ```
- `palette.go` `commands()`: inside `if m.actions != nil`, append `command{"cleanup", "Clean up worktrees and branches · " + target}`; `executeCommand` `case "cleanup": return m.prepareCleanup()`; `commandHint("cleanup")` → `"Prune stale, remove merged clean worktrees and merged branches after review"`; `commandIcon("cleanup")` → a broom-free plain glyph consistent with the set (`"⌫"` unicode, `"x"` ascii).
- Help (`view.go` near line 639): add `c  Clean up merged worktrees and branches (review first)`; workspace hint bar: add `[c] Clean up` where space allows (narrowest tier may omit).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/tui -run Cleanup -v` then `go test -race ./internal/tui` → PASS.

- [ ] **Step 6: Captures, docs, CHANGELOG**

- Add a `cleanup-110x35` capture to `TestOverlayRenderCaptures` using a fixed `app.CleanupPreview` assigned directly to `m.cleanup` (no Git): one prune item, two eligible removals, two kept items ("dirty (3 files)", "locked: agent session"). Regenerate captures as in Task 4.
- `docs/usage.md`: "Cleaning up worktrees" section: `c`/palette, what gets fetched, eligibility table and kept reasons (copy the spec's eligibility table for worktrees), `git remote set-head <remote> -a` advice, revalidation, no `--force`; update the "The dashboard does not …" sentence to say it does not force-remove worktrees or delete unmerged work, and keep "stage, commit, stash, reset, clean, rebase" as is.
- `README.md`: feature bullet "Reviewed cleanup: prune stale worktree records and remove clean worktrees already merged into the default branch"; adjust the safety paragraph: "never force-pushes or force-removes, never commits, stashes or resets, and only deletes worktrees that are clean and merged".
- `docs/plan.md`: under the non-goals/safety rules, add a dated note (2026-10-06) that worktree removal and merged-branch deletion are in scope under `docs/superpowers/specs/2026-10-06-worktree-and-branch-cleanup-design.md`.
- `docs/ui.md`: add the capture.
- `CHANGELOG.md` `### Added`: "Clean up (`c`) reviews and removes stale worktree records and clean linked worktrees already merged into the remote default branch. Dirty, locked, unmerged worktrees and worktrees with ignored files are kept with the reason shown; nothing is forced."

- [ ] **Step 7: Commit**

```bash
make check
git add internal/tui docs README.md CHANGELOG.md
git commit -m "feat(tui): review and run worktree cleanup

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

# Phase 3 — Merged branch cleanup

### Task 9: Branch listing and compare-and-delete in the Git layer

**Files:**
- Modify: `internal/git/cleanup.go`, `internal/git/service.go`
- Test: `internal/git/cleanup_test.go` (append)

**Interfaces:**
- Produces:
  ```go
  type Branch struct {
      Name     string // short name
      OID      string
      Upstream string // e.g. refs/remotes/origin/x; "" when none
      Gone     bool   // upstream configured but its tracking ref is missing
  }
  func (r Runner) LocalBranches(ctx context.Context, path string) ([]Branch, error)
  func (r Runner) DeleteBranch(ctx context.Context, path, name, oid string) error
  // Service: LocalBranches (Read), DeleteBranch (Write)
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestLocalBranches(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "branch", "plain")
	gitCmd(t, repo, "branch", "tracked")
	gitCmd(t, repo, "push", "-u", "origin", "tracked")
	gitCmd(t, repo, "update-ref", "-d", "refs/remotes/origin/tracked")
	got, err := (Runner{}).LocalBranches(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Branch{}
	for _, b := range got {
		byName[b.Name] = b
	}
	if len(got) != 3 || !objectID(byName["plain"].OID) || byName["plain"].Upstream != "" || !byName["tracked"].Gone || byName["main"].Gone {
		t.Fatalf("branches: %+v", got)
	}
}

func TestDeleteBranchComparesAndCleansConfig(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "branch", "done")
	gitCmd(t, repo, "config", "branch.done.description", "agent work")
	oid := strings.TrimSpace(string(gitCmd(t, repo, "rev-parse", "done")))
	r := Runner{}
	if err := r.DeleteBranch(context.Background(), repo, "done", strings.Repeat("1", 40)); err == nil {
		t.Fatal("deleted with a stale expected OID")
	}
	if err := r.DeleteBranch(context.Background(), repo, "done", oid); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/done").Output(); len(out) != 0 {
		t.Fatal("branch still exists")
	}
	if out, _ := exec.Command("git", "-C", repo, "config", "--get", "branch.done.description").Output(); len(out) != 0 {
		t.Fatal("branch config left behind")
	}
	gitCmd(t, repo, "branch", "noconfig")
	oid = strings.TrimSpace(string(gitCmd(t, repo, "rev-parse", "noconfig")))
	if err := r.DeleteBranch(context.Background(), repo, "noconfig", oid); err != nil {
		t.Fatalf("missing config section is not an error: %v", err)
	}
}
```
(import `os/exec`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/git -run 'LocalBranches|DeleteBranch' -v` → FAIL to compile.

- [ ] **Step 3: Implement**

```go
type Branch struct {
	Name     string
	OID      string
	Upstream string
	Gone     bool
}

func (r Runner) LocalBranches(ctx context.Context, path string) ([]Branch, error) {
	out, err := r.Run(ctx, path, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(upstream)%00%(upstream:track)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(strings.TrimSuffix(string(out.Stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		name, ok := strings.CutPrefix(fields[0], "refs/heads/")
		if len(fields) != 4 || !ok || name == "" || !objectID(fields[1]) {
			return nil, fmt.Errorf("malformed branch list")
		}
		branches = append(branches, Branch{Name: name, OID: fields[1], Upstream: fields[2], Gone: fields[3] == "[gone]"})
	}
	return branches, nil
}

// DeleteBranch removes refs/heads/<name> only while it still points at oid
// (Git compares and deletes atomically), then drops its config section.
func (r Runner) DeleteBranch(ctx context.Context, path, name, oid string) error {
	if name == "" || !objectID(oid) {
		return fmt.Errorf("invalid reviewed branch")
	}
	if _, err := r.Run(ctx, path, "update-ref", "-d", "refs/heads/"+name, oid); err != nil {
		return err
	}
	section := "branch." + name
	_, err := r.Run(ctx, path, "config", "--local", "--name-only", "--get-regexp", "^"+regexp.QuoteMeta(section)+`\.`)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil // no configuration for this branch
	}
	if err != nil {
		return fmt.Errorf("branch deleted; configuration not checked: %w", err)
	}
	if _, err := r.Run(ctx, path, "config", "--local", "--remove-section", section); err != nil {
		return fmt.Errorf("branch deleted; configuration left behind: %w", err)
	}
	return nil
}
```
(import `regexp`.) Add passthroughs: `LocalBranches` via `s.Read`, `DeleteBranch` via `s.Write`.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/git -v -run 'LocalBranches|DeleteBranch'` → PASS; `make check`.
```bash
git add internal/git
git commit -m "feat(git): list branches and delete one only at its reviewed commit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Merged branch items in the cleanup plan

**Files:**
- Modify: `internal/app/cleanup.go`
- Test: `internal/app/cleanup_test.go` (append)

**Interfaces:**
- Consumes: `LocalBranches`, `DeleteBranch` (Task 9); `CleanupGit` gains both methods.
- Produces: `DeleteBranch` items with `Path = group`, `Branch`, `OID`; success message `deleted <b> · restore: git branch <b> <oid>`.

- [ ] **Step 1: Write the failing tests**

```go
func TestCleanupPlansMergedBranchesAfterTheirWorktree(t *testing.T) {
	repo, merged, ahead := cleanupRepo(t)
	actionGit(t, repo, "branch", "done") // merged, not checked out
	actionGit(t, repo, "branch", "gone-squash", "ahead")
	actionGit(t, repo, "push", "-u", "origin", "gone-squash")
	actionGit(t, repo, "push", "origin", "--delete", "gone-squash")
	actions := NewActions(gitcli.Service{}, 1)
	preview, err := actions.PlanCleanup(context.Background(), []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	group := preview.Items[0].Group
	branch := func(name string) *CleanupItem {
		for i, item := range preview.Items {
			if item.Kind == DeleteBranch && item.Branch == name {
				return &preview.Items[i]
			}
		}
		return nil
	}
	if b := branch("done"); b == nil || !b.Eligible {
		t.Fatalf("done: %+v", b)
	}
	if b := branch("merged"); b == nil || !b.Eligible {
		t.Fatalf("branch of a removable worktree should be eligible: %+v", b)
	}
	if b := branch("gone-squash"); b == nil || b.Eligible || !strings.Contains(b.Reason, "squash merge?") {
		t.Fatalf("gone-squash: %+v", b)
	}
	if b := branch("ahead"); b != nil {
		t.Fatalf("ordinary unmerged branch listed: %+v", b)
	}
	var ids []string
	for _, item := range preview.Items {
		if item.Eligible {
			ids = append(ids, item.ID)
		}
	}
	results, err := actions.ExecuteCleanup(context.Background(), preview.ID, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.State != Succeeded {
			t.Fatalf("result %+v", r)
		}
	}
	if out := actionGit(t, group, "branch", "--list", "merged", "done"); len(strings.TrimSpace(string(out))) != 0 {
		t.Fatalf("branches remain: %s", out)
	}
	if _, err := os.Stat(merged); !os.IsNotExist(err) {
		t.Fatal("worktree not removed before its branch")
	}
	_ = ahead
	for _, r := range results {
		if strings.Contains(r.Item, "done") && !strings.Contains(r.Message, "restore: git branch done ") {
			t.Fatalf("no recovery hint: %+v", r)
		}
	}
}

func TestCleanupSkipsBranchStillCheckedOut(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actions := NewActions(gitcli.Service{}, 1)
	preview, _ := actions.PlanCleanup(context.Background(), []string{repo})
	var branchID string
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "merged" {
			branchID = item.ID
		}
	}
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{branchID}, nil)
	if len(results) != 1 || results[0].State != Skipped || !strings.Contains(results[0].Message, "checked out") {
		t.Fatalf("branch under a kept worktree: %+v", results)
	}
	actionGit(t, repo, "rev-parse", "--verify", "refs/heads/merged")
}

func TestCleanupNeverDeletesDefaultBranch(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "checkout", "--detach")
	preview, _ := NewActions(gitcli.Service{}, 1).PlanCleanup(context.Background(), []string{repo})
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "main" {
			t.Fatalf("default branch offered: %+v", item)
		}
	}
}

func TestCleanupSkipsBranchThatMoved(t *testing.T) {
	repo, _, _ := cleanupRepo(t)
	actionGit(t, repo, "branch", "done")
	actions := NewActions(gitcli.Service{}, 1)
	preview, _ := actions.PlanCleanup(context.Background(), []string{repo})
	var id string
	for _, item := range preview.Items {
		if item.Kind == DeleteBranch && item.Branch == "done" {
			id = item.ID
		}
	}
	actionGit(t, repo, "branch", "-f", "done", "ahead")
	results, _ := actions.ExecuteCleanup(context.Background(), preview.ID, []string{id}, nil)
	if len(results) != 1 || results[0].State != Skipped {
		t.Fatalf("moved branch: %+v", results)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/app -run 'Branch' -v` → FAIL (no branch items).

- [ ] **Step 3: Implement**

Add `LocalBranches` and `DeleteBranch` to `CleanupGit`. At the end of `planCleanupGroup`, before returning:
```go
	if base.ref == "" {
		return out
	}
	defaultName := strings.TrimSuffix(base.name, " (local default)")
	defaultName = defaultName[strings.Index(defaultName, "/")+1:] // origin/main → main
	removed := map[string]bool{}
	for _, p := range out {
		if p.item.Kind == RemoveWorktree && p.item.Eligible && p.item.Branch != "" {
			removed[p.item.Branch] = true
		}
	}
	checkedOut := map[string]string{}
	for _, wt := range worktrees {
		if wt.Branch != "" && !wt.Prunable && !removed[wt.Branch] {
			checkedOut[wt.Branch] = wt.Path
		}
	}
	branches, err := git.LocalBranches(ctx, path)
	if err != nil {
		add(CleanupItem{Kind: CleanupGroup, Path: group, Reason: "branch list failed: " + gitcli.SafeText(err.Error()), Failed: true})
		return out
	}
	for _, b := range branches {
		if b.Name == defaultName {
			continue
		}
		merged, err := git.IsAncestor(ctx, path, b.OID, base.ref)
		item := CleanupItem{Kind: DeleteBranch, Path: group, Branch: b.Name, OID: b.OID}
		switch {
		case err != nil:
			item.Reason = "merge check failed: " + gitcli.SafeText(err.Error())
		case merged && checkedOut[b.Name] != "":
			item.Reason = "merged but checked out in " + gitcli.SafeText(filepath.Base(checkedOut[b.Name]))
		case merged:
			item.Eligible, item.Reason = true, "merged into "+base.name
		case b.Gone:
			item.Reason = "upstream gone but not merged into " + base.name + " — squash merge?"
		default:
			continue // ordinary unmerged branch: not a cleanup candidate
		}
		add(item)
	}
	return out
```
(import `path/filepath`.) Note `defaultName` for a local base `"main (local default)"` is `main`: `strings.Index` returns -1, so `[0:]` keeps it whole — correct.

In `executeCleanupItem` add:
```go
	case DeleteBranch:
		err = git.DeleteBranch(ctx, item.Group, item.Branch, item.OID)
```
and on success:
```go
	case DeleteBranch:
		result.Message = fmt.Sprintf("deleted %s · restore: git branch %s %s", gitcli.SafeText(item.Branch), gitcli.SafeText(item.Branch), item.OID)
```
If `DeleteBranch` returns an error whose text starts with "branch deleted;", set `result.State = Succeeded` with the message plus `" · " + err` — the branch is gone and the recovery command must still be shown.

In `revalidateCleanup` add:
```go
	case DeleteBranch:
		for _, wt := range worktrees {
			if wt.Branch == item.Branch && !wt.Prunable {
				return fmt.Errorf("checked out in %s", filepath.Base(wt.Path))
			}
		}
		branches, err := git.LocalBranches(ctx, item.Group)
		if err != nil {
			return err
		}
		for _, b := range branches {
			if b.Name != item.Branch {
				continue
			}
			if b.OID != item.OID {
				return errors.New("branch moved since review")
			}
			merged, err := git.IsAncestor(ctx, item.Group, b.OID, item.Base)
			if err != nil {
				return err
			}
			if !merged {
				return errors.New("no longer merged into " + item.BaseName)
			}
			return nil
		}
		return errors.New("branch no longer exists")
```
A branch checked out only in a *stale* record is still deletable: Git keeps no working files for it, and `worktree prune` (ordered first) removes the record.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/app -run 'Cleanup|Branch' -v` → PASS; `go test -race ./internal/app`; `make check`.
```bash
git add internal/app
git commit -m "feat(app): delete local branches merged into the default branch

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Branch cleanup in the TUI, docs and final captures

**Files:**
- Modify: `internal/tui/cleanup.go` (branch label shows short OID; recovery hint in summary)
- Modify: `internal/tui/actions.go` (`batchDoneMsg`: when `runningAction == app.Cleanup` and any branch was deleted, append `· d restore commands`)
- Modify: `internal/tui/capture_test.go` (cleanup capture gains two branch items: one eligible, one "upstream gone … squash merge?")
- Modify: `docs/usage.md`, `README.md`, `CHANGELOG.md`; regenerate captures
- Test: `internal/tui/cleanup_test.go` (append)

**Interfaces:**
- Consumes: `app.DeleteBranch` items and their success message (Task 10).

- [ ] **Step 1: Write the failing test**

```go
func TestCleanupSummaryPointsToRestoreCommands(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.running, m.runningAction, m.results = true, app.Cleanup, map[string]app.Event{}
	m.Update(batchDoneMsg{generation: m.actionGeneration, results: []app.Event{{Path: "/r", Item: "delete branch\x00/r\x00/r\x00done", State: app.Succeeded, Message: "deleted done · restore: git branch done 0123456789abcdef0123456789abcdef01234567"}}})
	if !strings.Contains(m.message, "d restore commands") {
		t.Fatalf("summary: %q", m.message)
	}
	m.details, m.detailTab = true, 0
	if !strings.Contains(strings.Join(m.detailsContent(), "\n"), "restore: git branch done 0123456789abcdef") {
		t.Fatal("restore command missing from diagnostics")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/tui -run TestCleanupSummaryPointsToRestoreCommands -v` → FAIL.

- [ ] **Step 3: Implement**

In `batchDoneMsg` after the existing summary is built:
```go
		if m.runningAction == app.Cleanup {
			for _, result := range msg.results {
				if result.State == app.Succeeded && strings.Contains(result.Message, "restore: git branch") {
					m.message += " · d restore commands"
					break
				}
			}
		}
```
In `cleanupLabel` for `app.DeleteBranch`: `"delete branch " + name + "  " + m.style(shortCommit(item.OID), muted, false)`.

- [ ] **Step 4: Captures, docs, CHANGELOG**

- Regenerate captures (Task 4 commands) with the extended cleanup fixture.
- `docs/usage.md`: extend the cleanup section with branch rules — merged into the default ref, never the default branch, never a branch checked out in a remaining worktree, unmerged branches with a gone upstream are shown as kept ("squash merge?"), compare-and-delete at the reviewed commit, config section removed, restore command in Diagnostics (`d`).
- `README.md`: extend the cleanup feature bullet with "and local branches fully merged into it".
- `CHANGELOG.md` `### Added`: "Clean up also deletes local branches fully merged into the remote default branch, only at the commit you reviewed; Diagnostics (`d`) lists a `git branch <name> <commit>` command to restore each one."

- [ ] **Step 5: Final verification and commit**

```bash
make check
go run ./cmd/gitperch status --json . | head -40   # worktree object present, schema_version 1
git add internal/tui docs README.md CHANGELOG.md
git commit -m "feat(tui): review merged branch deletion with restore commands

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

Then a manual smoke check in a scratch workspace (temporary repos only): create a repo with a local bare remote, `remote set-head origin -a`, a merged clean worktree, a dirty worktree, a stale worktree, a merged branch; run `bin/gitperch <scratch>`; confirm the grouped list, `→` expansion, `c` review contents, toggling, execution, summary and `d` restore commands. Record the result in the final task report.
