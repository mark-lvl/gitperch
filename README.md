# repodash

A terminal dashboard for multiple local Git repositories, targeting Linux and WSL2.
Implementation follows [plan.md](plan.md) in separately committed milestones.

## Development

Go 1.27.1 and the system Git CLI are required. Bootstrap was performed with
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
when running the commands above. A normal installation of Go needs neither override.

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
Bubble Tea v2.0.10 and Lip Gloss v2.0.6 power the dashboard. Filtering and selection
are small model operations, so no Bubbles components are needed.

| Key | Behavior |
| --- | --- |
| j/k, arrows | Move highlight |
| Space | Toggle selection |
| a | Select/deselect all visible rows |
| / | Edit name/path/branch filter; changing it clears selection |
| Esc | Leave filtering / clear filter |
| r | Refresh local status |
| f | Preview selected repositories for fetch |
| Enter | Open a child shell in the highlighted worktree |
| g | Open LazyGit in the highlighted worktree |
| ? | Toggle help |
| d | Read full repository diagnostics and all root warnings; j/k scroll |
| q, Ctrl+C | Quit |

Child tools temporarily take over the terminal; the dashboard restores and
refreshes after exit. The child shell does not change the parent shell's
directory. Missing LazyGit is reported visibly. Highlighted errors and root
warnings remain visible. [WSL smoke checklist](docs/tui-smoke.md) documents manual
checks. Push and pull will be added in M6.

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
