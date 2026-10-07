# Worktree inventory, worktree cleanup and merged-branch cleanup

Status: approved design, pending implementation plan.
Date: 2026-10-06.

## Goal

AI coding agents leave linked worktrees and feature branches behind. gitperch
should show every worktree that belongs to a discovered repository, grouped
under its main repository, and offer a reviewed cleanup that removes only what
is provably safe: stale worktree records, clean worktrees whose commits are
merged, and local branches that are fully merged into the remote default
branch.

Success: after agents finish, one reviewed batch across many repositories
clears leftover worktrees and branches, and gitperch can never lose
uncommitted, untracked, ignored or unmerged work.

## Decisions

| Topic | Decision |
| --- | --- |
| Safety line | Remove only clean, unlocked, merged worktrees; prune stale records. Never `--force`. |
| Ignored files | A worktree containing any ignored file is kept, with the reason shown. |
| "Merged" | Tip reachable from the remote default ref (`refs/remotes/<remote>/HEAD`) after a fresh fetch. No squash/rebase-merge detection. No remote: local `main`, else `master`, labelled "local default". |
| Surface | TUI only: a "Clean up…" review screen. `status --json` gains read-only worktree fields. No headless mutation (plan.md). |
| Grouping | Linked worktrees are grouped under their main repository in the workspace list, collapsed by default. |
| Inventory source | `git worktree list --porcelain -z`, once per common Git directory (approach A). |
| Older Git | Below Git 2.36 the feature is off entirely, without warnings or errors. |

## Delivery phases

1. Worktree inventory and grouping (read-only).
2. Worktree cleanup (prune stale records, remove merged clean worktrees).
3. Merged-branch cleanup (same review screen).

Each phase ends with `make check`, updated render captures, docs and a
CHANGELOG entry. Phases 2 and 3 depend on phase 1.

## Phase 1: inventory and grouping

### Git layer (`internal/git/worktree.go`)

- `Runner.Worktrees(ctx, path) ([]Worktree, error)` runs
  `worktree list --porcelain -z`.
- `ParseWorktrees([]byte) ([]Worktree, error)` is a strict parser for the
  NUL-separated porcelain format. It rejects malformed records instead of
  guessing.
- `Worktree{Path, HeadOID, Branch, Bare, Detached, Main, Locked, LockReason,
  Prunable, PrunableReason}`. The first record is the main worktree. Reasons
  and paths shown in the UI pass through `SafeText`.
- The command is added to the read-only guarantees in `readonly_test.go`.

### App layer (`internal/app/worktrees.go`)

`Load` gains one step after `Inspect`:

1. Group inspected rows by `Status.CommonDir`.
2. Run `Worktrees` once per common directory, with a bounded worker pool
   (`workers`).
3. Add every listed worktree that is not already a row: nested worktrees
   (for example `.claude/worktrees/x`), worktrees outside the scanned roots,
   and the main worktree when only a linked one was scanned. Each existing
   one is inspected like any other row.
4. Prunable worktrees (directory missing) become rows without inspection.
5. A bare main repository becomes a non-selectable header for its group.

New types:

- `Row.Worktree *WorktreeInfo` (`json:"worktree,omitempty"`):
  `Main, Linked, Locked, LockReason, Prunable, PrunableReason, MainPath,
  OutsideRoots`.
- Groups are not stored separately: rows sharing `Worktree.MainPath` form a
  group, and `MainPath` always equals the group parent row's `Path`. Rows
  without worktree information (inspection or listing failed) form a group of
  one.
- The inventory and cleanup need Git 2.36 or newer (`worktree list -z`).
  gitperch reads `git version` once per Git executable; on older Git, or when
  the version cannot be read, the whole feature is disabled quietly: no
  inventory step, no warnings, no grouping beyond today's flat rows, no
  `worktree` JSON object, and no Clean up command (`c` explains the
  requirement). Status, fetch, push and pull behave exactly as in v0.1.

Failure handling (supported Git only): if `worktree list` fails for a group, that group keeps the
rows the scan found and a discovery warning is added. Loading never fails
because of the inventory.

### TUI

- The list renders groups. A group sorts by the highest `attentionRank` of its
  members; children are sorted by path under the parent.
- Children are indented with `├─`/`└─` and labelled by worktree directory
  name and branch. Stale rows show `◌ stale · <reason>`; locked rows show
  `⊘ locked`.
- Collapsed parents show a badge such as `⑂3 · 1 stale`. Groups start
  collapsed. `→` expands; `←` collapses, or jumps from a child to its parent.
- Selection stays per worktree path. Selecting a parent never selects its
  children. Stale and locked rows cannot be selected for fetch, push or pull.
- When search or a scope matches a child, its parent is shown dimmed as
  context and is not selected.
- The details "Worktree" tab lists all worktrees of the group with branch,
  state, lock and prune reasons.

### JSON

Additive `worktree` object per row. Schema version stays 1. `docs/usage.md`
documents the fields.

## Phases 2 and 3: cleanup

### Entry

The palette command "Clean up…" (key `c` on the workspace) targets the
selected groups, or the highlighted group without selecting it. A selected
child targets its whole group.

### App layer

`app.Actions` gains `PlanCleanup` and `ExecuteCleanup`. They share the single
active preview and the per-common-directory locks with fetch, push and pull,
but keep their own item-shaped plan, so `executeOne` and push/pull are
untouched. `app.Cleanup` is the action label shown in progress and results.
Cleanup targets are items, not paths: `CleanupItem{ID, Group, Kind, Path,
Branch, OID, Base, Eligible, Reason}` with `Kind` one of `PruneStale`,
`RemoveWorktree`, `DeleteBranch`. Events gain an `Item` field holding the item
ID, so several results per repository stay distinct.

### Preflight, per group (up to `workers` groups in parallel)

1. Fetch the default remote (existing `Fetch`).
2. Resolve the default ref with `symbolic-ref refs/remotes/<remote>/HEAD`. If
   it is missing, nothing in the group is eligible. The reason is "default
   branch unknown — run `git remote set-head <remote> -a`"; gitperch does not
   set it. Without a remote, use local `main`, else `master`; neither →
   nothing eligible.
3. Re-list worktrees, inspect each linked worktree, and list local branches
   with `for-each-ref refs/heads`.

### Eligibility

| Item | Eligible when | Command |
| --- | --- | --- |
| Stale worktree records (one item per group) | at least one prunable record that is not locked, and every such record's HEAD commit is reachable from some branch, remote-tracking branch or tag (otherwise the item is kept, naming the commit and a `git branch` command that saves it) | `worktree prune` |
| Linked worktree | exists; not main; not locked; no changes, untracked files or conflicts; no operation in progress; no ignored files; HEAD reachable from the default ref | `worktree remove <path>` |
| Local branch | not the default branch; not a symbolic ref (such as `master -> main`, skipped silently); tip reachable from the default ref; not checked out in any worktree that remains after this plan | `update-ref --no-deref -d refs/heads/<b> <oid>`, then `config --remove-section branch.<b>` |

- Reachability of a stale record's HEAD uses
  `for-each-ref --count=1 --contains <oid> refs/heads refs/remotes refs/tags`.
  Pruning deletes the record's reflog, so a commit only a detached HEAD reaches
  would become unreferenced; a record on a branch is safe because the branch
  keeps the commits.
- Ignored files are detected with
  `ls-files --others --ignored --exclude-standard --directory -z` in the
  worktree; the reason names the count and up to three paths.
- Reachability uses `merge-base --is-ancestor <oid> <default-ref>`.
- `--no-deref` and the symbolic-ref exclusion together guarantee that deleting
  an alias can never delete the branch it points at.
- `branch -d` is not used: it judges "merged" against HEAD or the upstream,
  not the remote default ref. `update-ref -d` with the expected OID is an
  atomic compare-and-delete, so a branch that moved after review is refused by
  Git. The config removal is best-effort; a missing section is not an error.
- Kept branches are listed only when they are merged but still checked out,
  or when their upstream is gone without being merged; ordinary unmerged
  branches are omitted so repositories with many branches stay readable.
- Kept items carry reasons, for example "dirty (3 files)", "locked: agent
  session", "4 ignored files (.env, node_modules/, …)", "not merged into
  origin/main", "upstream gone but not merged — squash merge?".

### Execution

- Order within a group: prune, then worktree removals, then branch deletions,
  so a branch freed by a removal in the same plan can be deleted.
- Groups run concurrently up to `workers`; items within a group run serially
  under the common-directory lock.
- Revalidate each item immediately before running it:
  - prune: the set of prunable, unlocked records equals the reviewed set and
    every record's HEAD commit is still reachable from a ref;
  - worktree: same HEAD OID, still clean, still no ignored files, still
    unlocked, still reachable from the default ref;
  - branch: same OID, still reachable, still not checked out.
  A mismatch skips the item with a reason.
- Best-effort per item: a failure does not stop the rest of the group. No
  retry, no `--force`, no destructive recovery.
- Results reuse the existing summary line. Each deleted branch's result shows
  its recovery command `git branch <b> <oid>`.

### Review screen

A larger popup, grouped by repository. Eligible items have checkboxes and
start ticked. `Space` toggles the highlighted item, `Enter` runs ticked items,
`Esc` discards. Kept items are listed dimmed below with their reasons.
Execution sends only the ticked item IDs; the plan itself stays immutable.

## Documentation

- README: update the safety sentence and feature list; move "Group worktrees
  under their parent repository" from the roadmap to features.
- `docs/usage.md`: cleanup rules, kept reasons, recovery command, JSON
  fields; the list of things gitperch never does stays true (no stash, reset,
  clean, force).
- `docs/plan.md`: record that deletion of merged branches and clean merged
  worktrees is now in scope under these rules.
- `docs/ui.md` and captures: grouped expanded, grouped collapsed, stale
  child, cleanup review.

## Testing

All Git tests use temporary repositories and local bare remotes.

- Parser fixtures: main, linked, locked with reason, prunable with reason,
  detached, bare, paths with spaces and newlines.
- Inventory: nested, outside-roots and stale worktrees appear in the right
  group; a `worktree list` failure degrades to a warning.
- Version gate: `git version` strings including vendor suffixes
  (`2.39.3 (Apple Git-146)`, `2.45.1.windows.1`) parse; Git 2.35 or an
  unreadable version disables inventory and cleanup with no warnings.
- Eligibility table tests: each kept reason, including ignored files, missing
  `origin/HEAD`, local-default fallback, squash-merged branch kept.
- Revalidation races: branch moved, worktree dirtied, lock added and new stale
  record between review and execution, each skipped.
- Ordering: a branch checked out only in a removed worktree is deleted in the
  same plan.
- TUI: grouping, collapse and expand, selection rules, context parents,
  review toggles, render captures.

## Out of scope

- Squash or rebase-merge detection.
- Removing dirty, locked or ignored-file worktrees, or any `--force`.
- Deleting remote branches.
- Setting `refs/remotes/<remote>/HEAD`.
- Headless (`gitperch cleanup`) mutation.
