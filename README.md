# repodash

A terminal dashboard for multiple local Git repositories, targeting Linux and WSL2.
Implementation follows [plan.md](plan.md) in separately committed milestones.

## Development

Building from source requires Go 1.27.1; running a built binary requires the
system Git CLI, not Go. Bootstrap was performed with
Go 1.27.1 (official Linux amd64 archive, SHA-256
`63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445`)
and Git 2.43.0. The module name is local (`repodash`); no remote or license has
been chosen. macOS/Windows support is not yet validated.

```sh
make fmt test vet build
make race
go run ./cmd/repodash --help
go run ./cmd/repodash --version
```

`make build` writes `bin/repodash`. For this coding environment, the downloaded
toolchain lives in `/tmp/repodash-toolchain/go`; use
`PATH=/tmp/repodash-toolchain/go/bin:$PATH` and `GOCACHE=/tmp/repodash-gocache`
plus `GOPATH=/tmp/repodash-gopath` to reuse the downloaded dependency cache
when running the commands above. A normal installation of Go needs none of these overrides.

To build and install the local binary:

```sh
make build
./bin/repodash --help
go install ./cmd/repodash
```

For an automatically refreshed development command, run:

```sh
make install-dev
repodash /path/to/projects
```

This installs a launcher at `~/.local/bin/repodash`, accessible from any directory
when `~/.local/bin` is on `PATH`. Each launch incrementally rebuilds this checkout
into `bin/repodash` before starting it, preserving your current directory and
arguments. Build errors stop the launch and leave the previous binary intact.
An already running dashboard continues using its original build; quit and
relaunch to pick up edits. The checkout must stay at its installed location.

The launcher uses Go from `PATH`, or `GO=/path/to/go`. In this development
environment it also recognizes the toolchain and caches under `/tmp` described
above. If that temporary toolchain is removed, install Go or set `GO`.
`INSTALL_DIR=/another/directory make install-dev` changes the install location.
The installer refuses to overwrite an unrelated existing command. Remove the
installed symlink to uninstall; this does not remove the checkout or binary.

`make build` creates `bin/repodash`; `go install` places `repodash` in Go's
configured `GOBIN` (or `$(go env GOPATH)/bin`). Add that directory to `PATH` if
you want to invoke `repodash` from any directory. This is a local build
workflow; no published release artifact is provided.

`make release-linux` prepares a static, versioned Linux amd64 binary under
`dist/` and prints its SHA-256. `VERSION=0.1.0` is the default; this target does
not publish anything. See the [draft release notes](docs/release-notes-v0.1.0.md).

## Read-only status (M2)

```sh
go run ./cmd/repodash status .
go run ./cmd/repodash status --max-depth 4 /path/to/projects
go run ./cmd/repodash status --json /path/to/projects
```

The table shows independent changes, untracked entries, conflicts, branch, upstream,
and locally known ahead/behind counts. `?` means no valid comparison is available;
it never means synchronized. `changes:N` counts tracked status entries once even
when both the index and worktree changed. Status performs no remote fetch and
uses Git's no-optional-locks mode. JSON schema version 1 has deterministic repository
ordering, raw path identity, inspection timestamps, and per-repository errors.
Terminal output escapes control characters and redacts credentials in URLs.

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
Ctrl+C exits with 130 while browsing; in previews or active batches it requests
cancellation instead of exiting immediately.
Git command output is capped at 4 MiB per stream,
with a 15-second default deadline. Repository-routing environment variables are
removed. Background Git has closed stdin, disabled terminal/askpass authentication,
and SSH BatchMode. Normal credential helpers and SSH agents remain available;
authenticate in a normal shell first. Custom `GIT_SSH_COMMAND` overrides are not
used; put host/key settings in SSH config. Hooks/helpers still run as configured
by Git; this tool does not sandbox repositories.

## Workspaces (M3)

On Linux/WSL, config defaults to `$XDG_CONFIG_HOME/repodash/config.toml` or
`~/.config/repodash/config.toml`. Other platforms use Go's platform-specific
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
repodash status --workspace personal
repodash status --config /path/to/config.toml --json
repodash --config /path/to/config.toml status /explicit/root
```

Positional roots override workspace paths. Config-relative roots use the config
directory; command-line roots use the current directory. Only `~` and `~/...`
expand. Workers default to eight for status and two for actions and are capped
at 64. Invalid TOML, duplicate or unknown workspaces, negative depths,
nonpositive workers/timeouts, and duration overflow are rejected. Inspection
runs concurrently while rows retain name/path ordering. One inspection failure
does not stop other rows. Configuration uses pinned go-toml v2.4.3.

## Interactive dashboard (M4)

```sh
repodash /path/to/projects
repodash --workspace personal
repodash --no-color /path/to/projects
NO_COLOR=1 repodash /path/to/projects
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

Space selects repositories; `a` toggles all visible rows. `p`/`l` review push or
fast-forward pull for the selected set, or the highlighted repository when no
selection exists. `f` reviews fetch for explicitly selected repositories. All
network operations retain the existing review and confirmation safeguards.
Search/scope changes clear selection; refresh preserves visible selections and
the highlighted repository by path. Tab toggles All/Focus; the palette provides the other repository filters. `s` toggles attention/name ordering, `[ / ]` scroll preview files, and `r` refreshes.

Colors supplement status symbols and text, using the terminal palette. Existing
TOML configuration accepts `[ui]` with `icons = "unicode"` (also `ascii` or `nerd`)
and `default_focus = false`. Agent/test/task actions are omitted because there is
no real integration yet. See the [interface guide](docs/ui.md) for all bindings,
configuration, limitations, and wide/medium/narrow render captures.

Child tools temporarily take over the terminal; the dashboard restores and
refreshes after exit. The child shell does not change the parent shell's
directory. Missing LazyGit is reported visibly. Git errors, discovery warnings,
and operation results remain accessible in repository details. `q` quits and
Ctrl+C interrupts; during operations they request cancellation and wait.

## Fetch preview and execution (M5)

Select repositories explicitly, press `f`, review targets with j/k (PgUp/PgDn
scroll long details), then Enter confirms or Esc cancels. The preview names the
worktree, exact remote URL and configured tracking refspecs, eligibility and
reason. Fetch uses the branch's configured upstream remote or the sole remote;
ambiguous targets are skipped. Ref mappings that could update local branches or
tags are rejected. Fetch prunes remote-tracking branches, disables tag fetching
and pruning, and does not recurse into submodules.

After confirmation, HEAD, branch, common Git directory and configuration are
revalidated. Changed plans are skipped. Actions sharing a common Git directory,
including linked worktrees, run serially; separate repositories use at most
`action_workers`. Only one preview or batch can be active. Each target keeps its
own result, so a failure does not stop other targets. `q`/Ctrl+C during a batch
requests cancellation and waits for outcomes. Use `d` for full results; Esc in
normal browsing dismisses them. Successful fetch time is tracked for the session.
Timed-out/cancelled fetches may have partially updated tracking refs; inspect or
refresh afterward. Batches are best effort, with no rollback.

For a disposable local demo (no network or personal repositories):

```sh
demo_root=$(bash scripts/demo.sh)
bin/repodash "$demo_root/workspace"
```

The script prints its new `/tmp/repodash-demo.*` directory and leaves it available
for inspection. It includes clean, behind, ahead, dirty, diverged, detached,
unborn, missing-upstream, failed-remote, and linked-worktree scenarios. Tracking
comparisons deliberately start stale for some rows; fetch reveals current state.

## Push and fast-forward pull (M6)

Select repositories with Space or `a`, then press `p` to push or `l` to
fast-forward pull. Both actions use two confirmations. The first screen is an
immutable Fetch scope preview: review the eligible fetch targets, then press
Enter to fetch those targets. Repodash compares refreshed state and presents a
second, final preview showing the exact push branch/commit or pull commit. Press
Enter again to execute; Esc cancels either preview. Repositories skipped during
fetch remain skipped. `d` shows full per-repository outcomes after the batch.

Push sends only the reviewed existing local branch commit(s) to the displayed
destination. Dirty files do not prevent pushing commits already in the branch,
and the preview explicitly says uncommitted changes are excluded. It requires a
known ahead-only comparison and a conventional upstream. Detached/unborn
branches, no upstream, behind or diverged histories, uncertain comparisons,
multiple push URLs, triangular push remotes, mirror/force settings, and
unsupported or ambiguous refspecs are skipped with reasons. Push does not
implicitly follow tags or recurse into submodules. Remote changes after the
final check can still make Git reject the push; repodash does not force or
retry.

Fast-forward pull requires a clean index and worktree. It fetches first, then
offers the exact fetched commit for review. The final integration is
fast-forward-only with autostash disabled. Dirty, conflicted, detached/unborn,
diverged, or operation-in-progress worktrees are skipped. Equal or ahead-only
branches have nothing to integrate. Pull updates tracked source files through
Git, but repodash does not stage files or create commits.

Both workflows revalidate repository state and target configuration before the
final operation. Batches are best effort per repository, not transactions; a
failure does not roll back other repositories. If a push times out or is
cancelled after it starts, its remote outcome can be unknown. Repodash will not
retry it automatically. Check the remote and local tracking status before
deciding whether to try again. External Git processes can race with repodash
after revalidation, and configured Git hooks still run.

`action_timeout_seconds` defaults to 120 seconds and can be changed in the
TOML config. A deadline limits how long repodash waits; it cannot guarantee
that a remote did not receive a push before the connection ended.

## Authentication and limits

Background Git commands have no interactive terminal or askpass prompt. Normal
Git credential helpers and SSH agents are available; authenticate in a normal
shell first. SSH runs in batch mode, and custom `GIT_SSH_COMMAND` overrides are
not used, so configure hosts and keys in SSH config. A failed authentication
appears as a per-repository action failure. Git hooks and helpers run with your
normal user permissions; repodash is not a sandbox for untrusted repositories.

The dashboard does not stage, commit, stash, reset, clean, rebase, resolve
conflicts, create branches, configure upstreams, or force push. It does not
support triangular push workflows, multiple push URLs, arbitrary push
refspecs, or headless bulk mutation. See [plan.md](plan.md) for the full v0.1
scope and action policy.
