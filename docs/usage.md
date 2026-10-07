# Usage guide

Detailed behavior of every gitperch command, the configuration file and the
safety rules behind each Git action. For key bindings and layouts see the
[interface guide](ui.md).

## Read-only status

```sh
go run ./cmd/gitperch status .
go run ./cmd/gitperch status --max-depth 4 /path/to/projects
go run ./cmd/gitperch status --json /path/to/projects
```

The table shows independent changes, untracked entries, conflicts, branch, upstream,
and locally known ahead/behind counts. `?` means no valid comparison is available;
it never means synchronized. `changes:N` counts tracked status entries once even
when both the index and worktree changed. Status performs no remote fetch and
uses Git's no-optional-locks mode. JSON schema version 1 has deterministic repository
ordering, raw path identity, inspection timestamps, and per-repository errors.
Terminal output escapes control characters and redacts credentials in URLs.

### Attention

gitperch ranks each repository and worktree by the Git state most likely to
need you. Every condition is read from that worktree's own status, so linked
worktrees of one repository are judged independently.

| Level | Reasons (JSON code) |
| --- | --- |
| critical | `inspection_failed`, `conflicts`, `operation_in_progress` (merge, rebase, cherry-pick, revert, bisect), `action_failed` (dashboard only: a fetch, push or pull failed or has an unknown outcome) |
| high | `uncommitted_changes` (staged or unstaged), `untracked_files`, `unpushed_commits`, `diverged`, `detached_commits` (a detached HEAD no branch, remote-tracking branch or tag contains) |
| medium | `behind_upstream`, `no_upstream`, `tracking_unknown` (the upstream's local tracking ref is missing), `no_commits`, `stale_worktree`, `worktree_finished` and `worktree_idle` (a linked worktree's [lifecycle](#worktree-lifecycle)) |
| low | Nothing to do: clean and synchronized, a detached HEAD a ref contains, a locked or bare worktree |

The level is the most severe reason. Levels above low count as needing
attention in the dashboard header, Focus and collapsed-group badges; attention
order (`s`) sorts by level, then name and path. The table's `ATTENTION` column
and each JSON repository's `attention` object (`{"level": "high", "reasons":
["uncommitted_changes", "unpushed_commits"]}`, reasons most severe first and
`[]` at low) carry the same result. Attention describes Git state; it does not
decide what to do, and age alone never raises it: only a clean linked
worktree with nothing unfinished can become `worktree_idle`.

Absolute paths distinguish duplicate names. Root depth is zero. Scanning stops
at a repository; explicitly supplied nested repositories are still eligible.
Both `.git` directories and regular worktree `.git` files are candidates.
Bare repositories are not candidates. Directory symlinks and roots reached via
symlink ancestors are skipped. Descendants named `node_modules`, `vendor`,
`target`, `.cache`, `.next`, `dist`, or `build` are ignored.

Missing or inaccessible roots print warnings while valid roots continue.
Exit codes: 0 completed, 1 partial failure, 2 invalid invocation, 130 interrupted.
In the TUI, a failed/cancelled/uncertain batch remains an exit-code-1 failure
even after dismissing its displayed results. Policy skips alone are not failures.
Ctrl+C exits with 130 while browsing; in a confirmation popup it cancels the
popup, and during a batch it requests cancellation instead of exiting immediately.
Git command output is capped at 4 MiB per stream,
with a 15-second default deadline. Repository-routing environment variables are
removed. Background Git has closed stdin, disabled terminal/askpass authentication,
and SSH BatchMode. Normal credential helpers and SSH agents remain available;
authenticate in a normal shell first. Custom `GIT_SSH_COMMAND` overrides are not
used; put host/key settings in SSH config. Hooks/helpers still run as configured
by Git; this tool does not sandbox repositories.

## Workspaces

On Linux/WSL, config defaults to `$XDG_CONFIG_HOME/gitperch/config.toml` or
`~/.config/gitperch/config.toml`. Other platforms use Go's platform-specific
user config directory. An absent default config scans the current directory.
An explicitly named missing config is an error.

```toml
default_workspace = "personal"
status_workers = 8
action_workers = 2
status_timeout_seconds = 15
action_timeout_seconds = 120

[[workspace]]
name = "personal"
paths = ["~/projects", "./experiments"]
max_depth = 4
ignore_dirs = ["node_modules", "vendor", "target", ".cache", ".next", "dist", "build"]
```

```sh
gitperch status --workspace personal
gitperch status --config /path/to/config.toml --json
gitperch --config /path/to/config.toml status /explicit/root
```

Positional roots override workspace paths. Config-relative roots use the config
directory; command-line roots use the current directory. Only `~` and `~/...`
expand. Workers default to eight for status and two for actions and are capped
at 64. Invalid TOML, duplicate or unknown workspaces, negative depths,
nonpositive workers/timeouts, and duration overflow are rejected. Inspection
runs concurrently while rows retain name/path ordering. One inspection failure
does not stop other rows. Configuration uses pinned go-toml v2.4.3.

## Interactive dashboard

```sh
gitperch /path/to/projects
gitperch --workspace personal
gitperch --no-color /path/to/projects
NO_COLOR=1 gitperch /path/to/projects
```

The default command requires terminal input and output; use `status` when piping.
The Bubble Tea v2 workspace shows a compact health header, repository list,
selected preview, and contextual actions. At 120+ columns its selected preview places changed files beside recent
commits. Narrower terminals stack content vertically. The compact frame follows
the content, and the command palette floats over the workspace. Short
terminals hide the preview first. The recommended minimum is 60×12.

Use arrows/j/k to navigate, Enter for repository details, `d` for changes, and
`o` for a shell. Details have Overview, Changes, Commits, and Worktree sections;
Tab switches sections and arrows/PgUp/PgDn scroll. `:` or Ctrl+K opens the fuzzy
action palette; arrows choose, Enter executes, and Esc closes it. `/` filters by
name/path/branch with arrow navigation and Enter to open a result. `?` opens help.

Space selects repositories; `a` toggles all visible rows. `c` reviews worktree cleanup. `p`/`l` push or
fast-forward pull the selected set, or the highlighted repository when no
selection exists, after one confirmation popup. `f` fetches explicitly selected
repositories straight away. Plans are still revalidated before anything runs.
Search/scope changes clear selection; refresh preserves visible selections and
the highlighted repository by path. Tab toggles All/Focus; the palette provides the other repository filters. `s` toggles attention/name ordering, `[ / ]` scroll preview files, and `r` refreshes immediately.

Colors supplement status symbols and text, using the terminal palette. Existing
TOML configuration accepts `[ui]` with `icons = "unicode"` (also `ascii` or `nerd`)
and `default_focus = false`. `refresh_seconds = 30` reloads local status
automatically while the dashboard list is idle (not while details, a
confirmation, the palette or an operation is open); `0` turns it off, otherwise
the value must be between 5 and 86400. Automatic refresh never fetches. Agent/test/task actions are omitted because there is
no real integration yet. See the [interface guide](ui.md) for all bindings,
configuration, limitations, and wide/medium/narrow render captures.

Child tools temporarily take over the terminal; the dashboard restores and
refreshes after exit. The child shell does not change the parent shell's
directory. Missing LazyGit is reported visibly. Git errors, discovery warnings,
and operation results remain accessible in repository details. `q` quits and
Ctrl+C interrupts; during operations they request cancellation and wait.

## Worktrees

gitperch asks Git for each repository's worktree list, so linked worktrees are
grouped under their main repository even when they are nested inside it, live
outside the scanned roots (marked `outside roots`), or have been deleted from
disk. A collapsed group shows a badge such as `⑂3 · 1 stale · 1 needs attention`
(three linked worktrees, one stale, one dirty, ahead or otherwise needing
attention); where the name column is narrow it shortens to `⑂3 ◌1 !1`, then to
`⑂3`. `→` expands the
group, `←` collapses it, and `←` on a worktree jumps to its repository. Search
and scopes show matching worktrees without expanding. The details Worktree
section lists every worktree in the group with its branch, state and path.

States: a worktree whose directory is missing is `stale` (Git calls it
prunable) and is listed but cannot be selected or acted on; a `locked`
worktree shows its lock reason when Git has one; a bare repository is shown as
`bare` and is not selectable. These are read-only views; the only changes
gitperch makes to worktrees are the reviewed ones under
[Cleaning up worktrees and branches](#cleaning-up-worktrees-and-branches). It
never creates or locks them.

The inventory needs Git 2.36 or newer. With older Git, or when the Git version
cannot be read, the feature is silently off: no grouping, no stale worktrees, no
warnings and no `worktree` object in JSON. Everything else works unchanged.

`gitperch status --json` adds a `worktree` object per repository when the
inventory is available (schema version still 1; the field is additive):

| Field | Meaning |
| --- | --- |
| `main` | The repository is the main worktree |
| `linked` | The repository is a linked worktree |
| `bare` | Bare repository (omitted when false) |
| `locked`, `lock_reason` | Locked worktree and Git's reason, if any (omitted when empty) |
| `prunable`, `prunable_reason` | Stale worktree and Git's reason, if any (omitted when empty) |
| `main_path` | Path of the main worktree that owns this group |
| `outside_roots` | Listed by Git but outside the scanned roots (omitted when false) |
| `integration` | Linked worktrees only: `base` (the default branch compared with, such as `origin/main`), `merged` (the base contains HEAD) and `error` (why no comparison was made, omitted when empty) |

### Worktree lifecycle

Each linked worktree also gets a lifecycle: a suggestion of what to review,
inferred from its current Git state. It never removes anything and never
makes a worktree eligible for Clean up, which repeats its own checks against a
fresh fetch. The main worktree, stale records and bare repositories have none.

| State (JSON) | Shown as | When |
| --- | --- | --- |
| `blocked` | the existing status, such as `! conflict` or `⊘ locked` | Conflicts, an operation in progress, a lock, or a status that could not be read |
| `in_progress` | the existing status, such as `● changed` | Uncommitted or untracked files, unpushed or detached commits, a diverged branch, or no commits |
| `active` | `✓ clean` | Clean, and HEAD moved in the last 24 hours |
| `likely_finished` | `✓ finished?` | Clean, nothing unpushed, HEAD merged into the default branch, and no HEAD activity for 24 hours |
| `idle` | `idle 21d` | Clean, nothing unpushed, not shown merged, and no HEAD activity for 14 days |
| `unknown` | `✓ clean` | Clean, but the signals point neither way |

"Activity" is when HEAD last moved in that worktree (a commit, checkout, pull
or reset), read from its HEAD reflog; gitperch does not track creation time or
who made a change. The default branch is chosen as Clean up chooses it, but from
local refs without fetching, so a merge the local refs have not seen, and any
squash or rebase merge, reads as not merged. That costs one lookup per
repository with linked worktrees and one `git merge-base --is-ancestor` per
linked worktree on each refresh. A worktree created from the default branch
looks merged at first, which is why recent activity keeps it `active`.

`likely_finished` and `idle` add the medium attention reasons
`worktree_finished` and `worktree_idle`, so they rank below unfinished work and
above clean repositories. The details Worktree section lists the signals behind
each state, such as `merged into origin/main` and `no HEAD activity for 6 days`.
JSON adds a top-level `lifecycle` object beside `attention`, omitted for rows
without one:

```json
"lifecycle": {"state": "likely_finished", "reasons": ["clean", "nothing_to_push", "merged", "inactive"]}
```

Signals, in this order: `inspection_failed`, `conflicts`,
`operation_in_progress`, `locked`, `uncommitted_changes`, `untracked_files`,
`diverged`, `unpushed_commits`, `detached_commits`, `no_commits`, `clean`,
`nothing_to_push`, `no_upstream`, `merged`, `not_merged`, `merge_unknown`, then
one of `recent_activity`, `inactive` or `activity_unknown`.

## Cleaning up worktrees and branches

Press `c` (or choose Clean up from the palette) to review leftover worktrees
and merged local branches, typically after agents finish. It targets the selected repositories, or the
highlighted one; a selected or highlighted linked worktree targets its whole
group. Clean up is hidden below Git 2.36, where `c` explains the requirement.

gitperch first fetches each repository's default remote, so "merged" is judged
against fresh data, then opens a review. Eligible items start ticked: Space
toggles the highlighted item, `a` toggles all, Enter runs the ticked items and
Esc discards the review without changing anything. The highlighted item lists
every check it passed, such as
`✓ working tree clean · no ignored files · no hidden edits · merged into origin/main`.
Items that are kept stay listed below with a status and every reason found:

| Status | Meaning |
| --- | --- |
| `blocked` | Running it would lose work or disturb Git: changes, untracked, ignored or hidden files, conflicts, an operation in progress, a lock, unpushed or diverged commits, a commit no ref reaches, a branch still checked out, or state that changed since review |
| `unknown` | A check could not be made, such as the default branch or a merge check, so safety is not established |
| `review` | Nothing at risk was found, but Git cannot show the work is finished: not merged into the default branch (squash and rebase merges look like this), no upstream, or an upstream that is gone |
| `failed` | A step for the whole repository failed, such as the preflight fetch |

Only items whose every check passed can be ticked; gitperch never runs a
`review` item, so decide those yourself. Only the ticked items you reviewed
run, each revalidated right before it runs; if anything changed in the meantime
(HEAD, the checked-out branch, status, lock state, ignored files, hidden
changes, reachability) the item is skipped with the reason and
"review again". Results appear in the status line and in details under Batch
results.

| Item | Removed when | Command |
| --- | --- | --- |
| Stale worktree records (one item per repository) | At least one record is stale (directory missing) and not locked, and every such record's HEAD commit is reachable from a branch, remote-tracking branch or tag | `git worktree prune` |
| Linked worktree | It exists, is not the main worktree, is not locked, has no changes, untracked files or conflicts, has no operation in progress, contains no ignored files, has no files marked assume-unchanged or (present) skip-worktree, and its HEAD is reachable from the default branch | `git worktree remove <path>` |
| Local branch | Its tip is reachable from the default ref, it is not the default branch, and no remaining worktree has it checked out | `git update-ref --no-deref -d refs/heads/<name> <commit>`, then `git config --local --remove-section branch.<name>` |

Reasons a worktree is kept include `3 uncommitted files`, `1 untracked file`,
`locked: agent session`, `4 ignored file(s) (.env, node_modules/, …)`,
`1 file(s) marked assume-unchanged or skip-worktree (config/dev.env)`,
`not merged into origin/main` and `2 commits not pushed to origin/feat/x`.
Edits to assume-unchanged and skip-worktree
files are invisible to `git status` and `git worktree remove` would delete
them; skip-worktree files a sparse checkout left absent do not count. Ignored
and hidden files are listed only for an otherwise clean worktree, since they
read the whole directory.

For a worktree not merged into the default branch, the review says where its
commits are. A detached HEAD whose commit no branch, remote-tracking branch or
tag contains is blocked with the command that saves it, such as
`git branch rescue/spike <commit>`; one that another ref reaches needs review.
A branch with commits not pushed to its upstream, or diverged from it, is
blocked; a branch whose commits are all pushed, or that has no upstream or a
gone upstream, needs review. Removing a worktree never deletes its branch.
Each removal's result carries the command that adds it back, such as
`git -C ~/src/app worktree add ~/src/app-fix fix` (or
`worktree add --detach <path> <commit>` for a detached HEAD); if its branch was
deleted in the same cleanup, run that branch's restore command first.
A stale record whose HEAD is a commit no ref reaches (for example work done on
a detached HEAD) is kept: pruning would leave that commit unreferenced. The
review names each such commit and the command that saves it, such as
`git branch rescue/spike <commit>`, wrapping long reasons instead of cutting
them off; run the commands, and the records become prunable.
Nothing is forced: gitperch never passes `--force` and never removes the main
worktree. gitperch checks for ignored files immediately before removal, but
files another program writes in the instant between that check and
`git worktree remove` cannot be protected.

"Merged" means the worktree's HEAD is reachable from `refs/remotes/<remote>/HEAD`
after the fresh fetch. There is no squash or rebase-merge detection: a branch
merged that way shows as not merged and is kept. A repository without a remote
uses local `main`, else `master`, labelled "local default". When the remote's
default branch is unknown, no worktree in the repository is eligible for
removal (stale records can still be pruned); run
`git remote set-head <remote> -a` yourself, because gitperch never sets it. A
failed fetch likewise makes no worktree eligible for removal, and the error is
shown among the kept items; stale records can still be pruned.

### Branches

Local branches are reviewed in the same list, one item per branch with its
short commit. A branch is eligible when its tip is reachable from the default
ref (`refs/remotes/<remote>/HEAD` after the fetch, or local `main`/`master`
without a remote), it is not the default branch itself, it is not a symbolic ref (an alias such
as `master` pointing at `main` is never listed), and no remaining worktree has
it checked out.

- A worktree holds its branch unless its record is stale (directory missing).
  A locked worktree holds its branch even when its directory is missing.
- A branch whose worktree removal you unticked, or whose removal fails, is
  skipped at run time ("checked out in ...").
- While any worktree in the repository has an operation in progress (rebase,
  merge, cherry-pick, revert, bisect) or cannot be inspected, merged branches
  are still listed but none is eligible.
- An unmerged branch whose upstream is gone is kept with
  "upstream gone but not merged into ... — squash merge?", since a squash or
  rebase merge cannot be detected. Other unmerged branches are not listed.

Deletion is a compare-and-delete at the commit you reviewed: if the branch has
moved since, it is skipped and nothing is lost. Afterwards the branch's
`branch.<name>` config section (its upstream settings) is removed. Each deleted
branch leaves a restore command in Diagnostics (`d`) under Batch results, for
example `git branch feat/old-login 9f3c2a7d41b86e05c1d2f3a4b5c6d7e8f9a0b1c2`;
the status line points to it with "d restore commands".
The same commands, naming the repository (`git -C <repository> branch …`) so
they work from any directory, are appended to
`$XDG_STATE_HOME/gitperch/cleanup.log` (by default
`~/.local/state/gitperch/cleanup.log`; one tab-separated line per deletion with
time, repository and branch as Go-quoted strings so they stay exact, commit and
command; the file is kept readable only by you) and printed again when the
dashboard closes. If the log cannot be written the deletion still stands, and
the result or the exit message says what was not recorded. When a repository
path or branch name has to be shown escaped (for example one containing a
zero-width non-joiner), the printed command says so and gives the
`git branch <name> <commit>` form to run inside the repository; the log's
quoted columns keep the exact names.
Names with shell-significant characters are single-quoted in that command, so it is
safe to paste. If a name contains unprintable characters, the command shows the
escaped name, says so, and the commit stays recoverable through its hash.

## Fetch

Select repositories explicitly and press `f`; fetch starts without a
confirmation, since it only updates remote-tracking refs. If no selected
repository can be fetched, the status line gives the reason instead. Fetch uses
the branch's configured upstream remote or the sole remote;
ambiguous targets are skipped. Ref mappings that could update local branches or
tags are rejected. Fetch prunes remote-tracking branches, disables tag fetching
and pruning, and does not recurse into submodules.

Before execution, HEAD, branch, common Git directory and configuration are
revalidated. Changed plans are skipped. Actions sharing a common Git directory,
including linked worktrees, run serially; separate repositories use at most
`action_workers`. Only one plan or batch can be active. Each target keeps its
own result, so a failure does not stop other targets. `q`/Ctrl+C during a batch
requests cancellation and waits for outcomes. Use `d` for full results; Esc in
normal browsing dismisses them. Successful fetch time is tracked for the session.
Timed-out/cancelled fetches may have partially updated tracking refs; inspect or
refresh afterward. Batches are best effort, with no rollback.

For a disposable local demo (no network or personal repositories):

```sh
demo_root=$(bash scripts/demo.sh)
bin/gitperch "$demo_root/workspace"
```

The script prints its new `gitperch-demo.*` directory (under `$TMPDIR`, else `/tmp`) and leaves it available
for inspection. It includes clean, behind, ahead, dirty, diverged, detached,
unborn, missing-upstream, failed-remote, and linked-worktree scenarios. Tracking
comparisons deliberately start stale for some rows; fetch reveals current state.

## Push and fast-forward pull

Select repositories with Space or `a`, then press `p` to push or `l` to
fast-forward pull. Gitperch first fetches the targets (concurrently, within
the same `action_workers` and shared-Git-directory limits as other actions),
compares the refreshed state, then opens one small popup listing each eligible repository with its
route (`main → origin/main`) and short commit, plus any skips and their reasons.
Enter executes; Esc cancels. When nothing is eligible, no popup opens and the
status line says why. Repositories skipped during fetch remain skipped. `d`
shows full per-repository outcomes after the batch.

Push sends only the reviewed existing local branch commit(s) to the displayed
destination. Dirty files do not prevent pushing commits already in the branch,
and the popup says uncommitted changes stay local. It requires a
known ahead-only comparison and a conventional upstream. Detached/unborn
branches, no upstream, behind or diverged histories, uncertain comparisons,
multiple push URLs, triangular push remotes, mirror/force settings, and
unsupported or ambiguous refspecs are skipped with reasons. Push does not
implicitly follow tags or recurse into submodules. Remote changes after the
final check can still make Git reject the push; gitperch does not force or
retry.

Fast-forward pull requires a clean index and worktree. It fetches first, then
offers the exact fetched commit for confirmation. The final integration is
fast-forward-only with autostash disabled. Dirty, conflicted, detached/unborn,
diverged, or operation-in-progress worktrees are skipped. Equal or ahead-only
branches have nothing to integrate. Pull updates tracked source files through
Git, but gitperch does not stage files or create commits.

Both workflows revalidate repository state and target configuration before the
final operation. Batches are best effort per repository, not transactions; a
failure does not roll back other repositories. If a push times out or is
cancelled after it starts, its remote outcome can be unknown. Gitperch will not
retry it automatically. Check the remote and local tracking status before
deciding whether to try again. External Git processes can race with gitperch
after revalidation, and configured Git hooks still run.

`action_timeout_seconds` defaults to 120 seconds and can be changed in the
TOML config. A deadline limits how long gitperch waits; it cannot guarantee
that a remote did not receive a push before the connection ended.

## Authentication and limits

Background Git commands have no interactive terminal or askpass prompt. Normal
Git credential helpers and SSH agents are available; authenticate in a normal
shell first. SSH runs in batch mode, and custom `GIT_SSH_COMMAND` overrides are
not used, so configure hosts and keys in SSH config. A failed authentication
appears as a per-repository action failure. Git hooks and helpers run with your
normal user permissions; gitperch is not a sandbox for untrusted repositories.

The dashboard does not stage, commit, stash, reset, clean, rebase, resolve
conflicts, create branches, configure upstreams, or force push. It does not
force-remove worktrees or delete unmerged work; the only branches it deletes
are fully merged ones, after review. It does not support triangular push workflows, multiple push URLs, arbitrary push
refspecs, or headless bulk mutation. See [plan.md](plan.md) for the full v0.1
scope and action policy.
