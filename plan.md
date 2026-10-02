# repodash — implementation plan

Status: implementation in progress. The user requested all milestones, overriding the first-session M1 stopping point. Checked items below record verified work.

## 1. Goal

Build a small terminal dashboard for managing multiple local Git repositories. Discover repositories, understand their state, select several, fetch/push/fast-forward pull, and open LazyGit or a shell for deeper work.

Primary environment: Ubuntu in WSL2 on Windows. Prioritize a dependable Linux/WSL release; keep Go code portable and add macOS/Windows validation before advertising support.

Success means I can open repodash over my projects directory, see which projects need attention, and synchronize selected repositories without manually visiting each directory.

## 2. Instructions for the coding agent

- Read this plan and any existing AGENTS.md before changing files. Inspect the existing repository and preserve unrelated changes.
- Implement milestones in order. Start with M0 and M1 in the first session; stop at their acceptance gate unless asked to continue.
- Keep domain logic independent of the TUI. Use small packages and functions; avoid speculative frameworks or plugin systems.
- Use the installed Git CLI through argument arrays, never shell-interpolated Git commands.
- Add focused tests for parsing, discovery, action eligibility, and actual Git behavior. Use temporary repositories and local bare remotes; never test mutations against personal or work remotes.
- Treat unchecked boxes as unfinished. Update this file after each completed milestone with commands run, outcomes, and remaining limitations.
- Verify current official dependency APIs when introducing them. Pin the selected versions in go.mod/go.sum; do not mix Bubble Tea v1 examples into v2 code.
- Do not create a remote repository, publish releases, or push this project unless requested. Do not invent a GitHub owner or a license choice.
- At each handoff report what works, how to run it, what was verified, and the next milestone.

## 3. Scope

### v0.1 includes

- Configurable workspaces and root directories.
- Recursive discovery with depth limits, ignored directories, and symlink safety.
- Ordinary repositories and `.git` files used by linked worktrees.
- Branch, dirty state, conflicts, ahead/behind, upstream, and per-repository errors.
- Bounded concurrent inspection with stable ordering.
- A read-only `status` command and an interactive TUI.
- Navigation, filtering, selection, refresh, and actionable error details.
- Previewed fetch, push, and fast-forward pull with per-repository results.
- Launching LazyGit or a child shell in the highlighted repository.

### Excluded from v0.1

Staging, commits, diffs, conflict resolution, rebasing, force push, deletion, automatic stashing, branch creation, initial upstream setup, cloning, GitHub/GitLab APIs, PR management, credential management, AI integration, embedded terminals, databases, daemons, and automatic background synchronization.

Bare repositories are not dashboard targets. Submodules contribute to their parent status but are not recursively managed. A nested repository can be included by explicitly configuring it as a root.

## 4. Technology and structure

Use Go, the system Git executable, and TOML configuration. Add Bubble Tea v2, Lip Gloss v2, and only the Bubbles v2 components actually needed when reaching M4. Use the standard flag package initially; no CLI framework is necessary.

Use `github.com/pelletier/go-toml/v2` for configuration. Choose a supported Go toolchain compatible with pinned dependencies at bootstrap. If no repository URL is known, use the local module path `repodash`; change it before publishing.

Create files when their milestone needs them, rather than leaving empty placeholders.

```text
repodash/
  cmd/repodash/main.go
  internal/
    repository/       # domain types, independent status dimensions
    config/           # TOML loading, defaults, path normalization
    discovery/        # filesystem traversal only
    git/              # CLI runner, parsing, metadata, Git operations
    app/              # inspection pool, action plans, eligibility, execution
    tui/              # model, update, view, keys, styles
  testdata/git-status/
  .github/workflows/ci.yml
  .gitignore
  Makefile
  README.md
  plan.md
  go.mod
  go.sum
```

Tests live beside implementation files. Keep CLI/TUI dependent on the application layer; keep discovery independent of Git execution. Use a small Git service interface where tests need substitution, not interfaces for every type.

## 5. Configuration and commands

Linux/WSL default: `$XDG_CONFIG_HOME/repodash/config.toml`, falling back to `~/.config/repodash/config.toml`. Use platform-appropriate configuration directories elsewhere. Support an explicit `--config` path.

```toml
default_workspace = "personal"
status_workers = 8
action_workers = 2
status_timeout_seconds = 15
action_timeout_seconds = 120

[[workspace]]
name = "personal"
paths = ["~/projects", "~/experiments"]
max_depth = 4
ignore_dirs = ["node_modules", "vendor", "target", ".cache", ".next", "dist", "build"]

[[workspace]]
name = "work"
paths = ["~/work"]
max_depth = 4
```

Semantics:

- Explicit positional roots override workspace paths. `--workspace NAME` selects a configured workspace; otherwise use `default_workspace`.
- With no config and no positional roots, scan the current directory. Never silently scan the entire home directory.
- Expand only `~` and `~/...`; do not perform arbitrary shell expansion. Resolve relative config paths against the config file directory and CLI paths against the current directory.
- Reject invalid TOML, unknown workspace names, duplicate names, negative depths, nonpositive worker counts, and nonpositive timeouts with useful messages.
- Cap worker counts at a documented reasonable maximum. Missing/inaccessible roots produce visible warnings while valid roots continue.

Target commands:

```bash
repodash                          # TUI over configured workspace or cwd
repodash ~/projects               # TUI over an explicit root
repodash --workspace work
repodash status ~/projects        # plain read-only table
repodash status --json ~/projects # stable machine-readable output
repodash --help
repodash --version
```

Keep mutation commands inside the TUI for v0.1. Headless bulk mutation is deferred until interactive behavior is proven. Without a terminal, the default command should explain how to run `status`.

## 6. Domain and discovery behavior

Represent facts independently: a repository can be dirty AND ahead AND behind. Do not use one mutually exclusive enum for all status.

Repository identity is its normalized absolute worktree path, not its basename. Store its display name separately. Status should include branch, HEAD OID, upstream, whether ahead/behind is known, ahead/behind counts, changed-entry count, untracked count, conflict count, detached/unborn flags, inspection time, and any error. Add resolved common Git directory and operation-in-progress metadata when implementing actions.

Discovery contract:

1. Treat each configured root as depth zero; `max_depth = 0` inspects that root only.
2. Detect `.git` directories and regular `.git` files as candidates. Git inspection later validates them.
3. Stop descending beneath a discovered repository and never walk its `.git` contents.
4. Do not follow directory symlinks. Report explicitly supplied symlink roots as skipped in v0.1.
5. Apply ignore rules to descendant directory basenames. An explicitly configured root remains eligible even if its basename is ignored.
6. Deduplicate overlapping roots by normalized absolute path. Keep distinct worktrees as distinct rows.
7. Sort deterministically by display name, then absolute path. Show enough relative path to distinguish duplicate names.
8. Return partial discoveries plus warnings for inaccessible paths. Honor cancellation.

## 7. Git inspection

Use one primary status command per candidate:

```bash
git --no-optional-locks -C PATH status --porcelain=v2 --branch -z --untracked-files=all --ignore-submodules=none
```

Parse the documented NUL-delimited format rather than whitespace-splitting filenames. Account for ordinary entries, renames/copies with an additional path field, unmerged entries, untracked entries, and branch headers. Ignore unknown headers for forward compatibility; reject malformed known records visibly.

Count each changed status entry once even if both index and working tree changed. Use `changes:N` rather than claiming every entry is an ordinary modified file. Treat dirty submodules as dirty. Handle unborn branches, detached HEAD, missing/deleted upstreams, and filenames containing spaces, tabs, Unicode, or newlines.

Ahead/behind reflects locally known tracking refs, not a live remote check. Label it accordingly and track the last successful fetch for the session. Missing comparison information must not be presented as synchronized.

Get remote/branch configuration through Git when needed for action planning; never infer a remote name by splitting an upstream display string. Additional metadata calls are acceptable where correctness requires them.

Execution requirements:

- Use context cancellation, command timeouts, bounded output capture, and separate stdout/stderr.
- Prevent inherited repository-routing variables such as GIT_DIR, GIT_WORK_TREE, and GIT_INDEX_FILE from redirecting an operation; preserve normal credential and SSH-agent configuration.
- Background commands must not consume the TUI's input or hang on authentication prompts. Configure noninteractive behavior, document supported SSH/helper behavior, and report how to authenticate in a normal shell.
- Sanitize terminal control characters in displayed names and diagnostics; redact credentials in URLs.
- A failed repository inspection must not stop other repositories.

## 8. Action planning and safety

Application pipeline: selected paths → refresh/preflight → immutable preview → confirmation → revalidation → execution → refreshed results.

Every preview shows repository path, action, exact remote/branch target, eligible/skipped state, and reason. Push and pull require confirmation. Fetch also shows its scope before execution; fetching can prune tracking refs and is not a read-only operation.

All batch operations are best-effort per repository, not a cross-repository transaction. Never promise rollback. Never automatically retry a timed-out push: its remote outcome may be unknown.

### Fetch

Fetch the configured upstream remote; if absent, use the sole configured remote. If several remain ambiguous, skip with a reason. Use prune for remote-tracking branches, disable tag pruning, and avoid fetching submodules. Reject unusual fetch refspecs that would update local branches or tags in v0.1. Show the remote in the preview.

### Push

- Support a conventional branch with one upstream and an unambiguous push destination. Skip detached/unborn branches, missing upstreams, conflicts, operations in progress, unknown comparisons, or inspection failures.
- If a push remote or destination differs from the upstream, skip with an explanation in v0.1; triangular workflows are deferred.
- Refresh the target remote before preparing the final preview. Require ahead > 0 and behind = 0.
- Show the push URL and full destination branch. Reject multiple push URLs, mirror mode, force-configured refspecs, or ambiguous mappings.
- Push exactly the selected local branch to its previewed destination using an explicit non-force refspec. Do not use a bare `git push` that could select additional configured refs. Disable implicit tag following and recursive submodule pushes.
- Revalidate HEAD, branch and target config after confirmation. Skip changed plans. A subsequent remote race must result in normal non-fast-forward rejection.
- Dirty files alone do not prevent pushing existing commits, but the preview must state that uncommitted changes are excluded.

### Fast-forward pull

- Require a clean index/worktree, no conflicts or operation in progress, a normal branch, and a valid unambiguous upstream.
- Fetch the upstream, re-inspect, then preview the exact commit to integrate. Diverged histories are skipped; equal or ahead-only branches need no integration.
- Implement as fetch plus explicit fast-forward-only integration of the fetched commit, with autostash disabled. This provides the intended `pull --ff-only` behavior while fixing the target for review.
- Revalidate the worktree, branch, HEAD and config immediately before integration. Never merge divergent history, rebase, stash, reset, clean, or change upstream configuration.

Pull updates tracked source files through Git. Repodash does not directly edit source files or create commits itself. Ordinary Git hooks and credential helpers still apply; this tool is not a sandbox for untrusted repositories.

Serialize actions sharing a common Git directory, including linked worktrees. Prevent overlapping batches within the application. External tools can still race with repodash; detect changed state and report Git failures without attempting destructive recovery.

## 9. TUI behavior

Show repository/path, branch, changes, ahead, behind, upstream, and operation state. Use independent markers for dirty, conflicts, detached, no upstream, and error. A green synchronized indicator requires a known zero/zero comparison and a clean worktree.

| Key | Action |
| --- | --- |
| j/k or arrows | Move highlight |
| Space | Toggle selection |
| a | Select/deselect all visible rows |
| / | Filter by name/path/branch |
| Esc | Leave filter or cancel preview |
| r | Refresh local status |
| f / p / l | Preview fetch / push / pull |
| Enter | Launch child shell; confirm when in a preview |
| g | Launch LazyGit in highlighted repository |
| ? | Help |
| q | Quit, or request cancellation if a batch is running |

Require explicit selection for batch actions. Applying a filter clears selection, preventing hidden repositories from being mutated. Display target count clearly.

Keep the event loop responsive. Use application events for progress: queued, running, succeeded, skipped, failed, cancelled, or outcome unknown. Keep results and short diagnostics visible until dismissed. Preserve stable row ordering and selection by path during refresh. Ignore stale refresh responses using a request generation ID.

Suspend/release the terminal while a child shell or LazyGit runs; restore and refresh on exit. A child shell does not change the parent shell's directory. Handle missing LazyGit gracefully. Support resize, narrow terminals, empty lists, long branch names, and a no-color mode.

## 10. Milestones and acceptance gates

### M0 — Bootstrap

- [x] Inspect workspace, instructions, Go and Git availability.
- [x] Initialize a Go module and minimal command with help/version support.
- [x] Add README, .gitignore, and Makefile targets: fmt, test, vet, build.
- [x] Add CI for formatting, tests, vet, and build with a pinned supported Go version.
- [x] Record the chosen versions and local run commands.

Gate: `go test ./...`, `go vet ./...`, and `go build ./cmd/repodash` succeed. No TUI dependencies or mutation behavior yet.

### M1 — Domain and discovery

- [x] Implement domain identity and scanner options/results.
- [x] Implement traversal rules from section 6.
- [x] Add temporary-directory tests for .git directories/files, depth boundaries, ignored paths, nested repositories, overlapping roots, duplicate basenames, symlinks, missing roots, and cancellation.
- [x] Make `repodash status ROOT` print discovered paths, clearly labeled as discovery-only until M2.

Gate: deterministic, tested discovery with partial-error reporting. First coding session ends here.

### M2 — Git status and plain CLI

- [x] Implement CLI runner with cancellation and timeouts.
- [x] Implement porcelain-v2 parser and complete status model.
- [x] Store real fixture outputs, including NUL delimiters, from temporary Git repositories.
- [x] Add parser tests for normal, staged+unstaged, renamed, conflicted, untracked, detached, unborn, dirty-submodule, and unusual-filename cases.
- [x] Produce a readable status table and JSON output, with per-repository errors.

Gate: results match Git in disposable repositories. Unknown upstream comparisons are not marked synchronized. `status` performs no network access.

### M3 — Workspaces and concurrent inspection

- [x] Add TOML loading, validation, defaults, and CLI overrides.
- [x] Add an eight-worker default inspection pool, cancellation, and stable sorting.
- [x] Test worker limits and slow/failing repositories with a fake Git service.
- [x] Define exit codes: 0 completed, 1 partial/operation failure, 2 invocation/config failure, 130 interrupted.

Gate: scan and inspect a generated multi-repository workspace without exceeding concurrency limits. Race detector passes. Record a local timing observation; avoid hardware-specific performance promises.

### M4 — Read-only TUI

- [x] Introduce pinned Bubble Tea/Lip Gloss dependencies and required Bubbles components.
- [x] Implement table, navigation, selection, filter, help, refresh, resize, and error detail.
- [x] Implement child-shell and LazyGit launching.
- [x] Test selection/filter rules, empty states, and stale-response rejection.

Gate: manually exercise the TUI in WSL2, including tool launch/return and Ctrl+C. If WSL2 is unavailable, record that verification as pending rather than claiming it passed.

### M5 — Preview and fetch

- [x] Implement explicit action plans, eligibility reasons, confirmation, progress and results.
- [x] Resolve remote targets and implement bounded fetch execution.
- [x] Serialize common Git directories and implement cancellation/timeouts.
- [x] Test with local bare remotes and linked worktrees.

Gate: one failure does not stop other eligible repositories; previews match executed targets; ambiguous configurations skip visibly.

### M6 — Safe push and fast-forward pull

- [x] Implement the policies in section 8 without shortcuts around target resolution.
- [x] Add local-remote integration tests for successful push, dirty-but-pushable state, no upstream, behind/diverged history, detached/unborn branches, clean fast-forward, dirty pull refusal, and operation-in-progress refusal.
- [x] Test config traps: multiple push URLs, differing push remote, mirror/force settings, implicit tags, and unusual refspecs.
- [x] Test state changes after preview, remote advancement, timeout/cancellation reporting, and worktree serialization.

Gate: no unrelated branch or tag changes; no force push, automatic stash, merge commit, or rebase; failed/skipped targets remain explained. Tests use only disposable repositories.

### M7 — v0.1 release readiness

- [ ] Document installation, configuration, keys, safety boundaries, auth troubleshooting, and limitations.
- [ ] Add a reproducible demo workspace and manual WSL smoke checklist.
- [ ] Run formatting, test, race, vet, and build gates.
- [ ] Build Linux amd64 release artifact first. Expand platform CI before claiming macOS/Windows support.
- [ ] Add release automation only when useful; license selection and publication require an explicit project decision.

Gate: the complete discover → inspect → select → preview → synchronize → inspect workflow works. Prepare release notes; do not publish automatically.

## 11. First-session prompt

Paste this into Codex with this file at the project root:

> Read plan.md and any AGENTS.md. Implement only M0 and M1 for repodash. Inspect the workspace first and preserve existing work. Bootstrap the Go module, minimal CLI, README, Makefile and CI, then implement the UI-independent repository identity and discovery layer. Support .git directories and files, configurable roots and maximum depth, ignored directories, deterministic sorting, duplicate-root handling, symlink safety, cancellation, and partial errors. Add focused tests using temporary directories. Make `go run ./cmd/repodash status .` list discovered repository paths, clearly labeled discovery-only. Do not add the TUI, Git mutations, or speculative packages. Run formatting, tests, vet and build; update milestone checkboxes only for verified work. Finish with run instructions, test results, and the next milestone. Do not create or push a remote repository.

For subsequent sessions:

> Read plan.md and inspect the current implementation. Implement the next incomplete milestone, preserve the safety policies and scope, run its acceptance checks, and update this plan with verified progress and remaining limitations.

## 12. Deferred backlog

After real daily use, consider worktree discovery/management, launching coding agents, named workspace switching, headless action commands, triangular push workflows, optional nested/submodule management, remote URL opening, and richer sorting. Add only features supported by a concrete workflow problem.

## 13. Official references

Consult these when implementing the relevant milestone; verify exact installed-version behavior:

- [Git status and porcelain v2](https://git-scm.com/docs/git-status)
- [Git push and refspec behavior](https://git-scm.com/docs/git-push)
- [Git fetch](https://git-scm.com/docs/git-fetch)
- [Git merge and fast-forward options](https://git-scm.com/docs/git-merge)
- [Git configuration](https://git-scm.com/docs/git-config)
- [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- [Bubbles](https://github.com/charmbracelet/bubbles)
- [Lip Gloss](https://github.com/charmbracelet/lipgloss)
- [go-toml](https://github.com/pelletier/go-toml)

## 14. Implementation log

| Milestone | Date | Verification | Remaining limitations |
| --- | --- | --- | --- |
| Planning | — | Plan prepared; no application code implemented | M0–M7 pending |
| M0 | 2026-10-02 | Empty workspace inspected; no AGENTS.md found. Go 1.27.1 downloaded from go.dev with matching official SHA-256; Git 2.43.0. `make fmt test vet build` passed. Help/version and invalid invocation tests passed. | CI added but remote CI not run; Linux local validation only. M1–M7 pending. |
| M1 | 2026-10-02 | Focused domain/discovery tests and vet passed; full test/vet/build passed. `go run ./cmd/repodash status .` reports the absolute worktree path with discovery-only label. Depth 0/1/2, ignores, explicit nested/ignored roots, overlap, duplicates, symlinks, missing/non-directory/permission-denied roots, invalid depth and cancellation covered. | Filesystem candidates await Git validation in M2. Root paths through symlink ancestors are also skipped. WSL manual validation pending. |
| M2 | 2026-10-02 | `make fmt test vet build` passed. Binary NUL fixtures captured with Git 2.43.0 and parsed alongside fresh disposable repositories; rename/conflict/submodule/unusual-name cases passed. Local bare remote verifies known comparison and deleted-upstream unknown state. Runner tests verify env isolation, cancellation, deadline, and capture overflow. Table/JSON failure isolation tested. Official Git status format consulted. | Status reflects local refs only; network actions and TUI pending. Authentication requires helpers/agents; arbitrary Git SSH command overrides disabled. |
| M3 | 2026-10-02 | `make fmt test race vet build` passed. Fake service tests cover concurrency bound, 64-worker cap, slow/failing repos, cancellation and sorting. CLI generated 24 disposable repositories, exercised TOML/default workspace/positional override and exit codes. One local WSL2 discover+inspect+JSON observation: 24.7 ms (not a performance guarantee). go-toml v2.4.3 API verified and pinned. | TUI and network actions remain. CI has not run remotely; WSL2 kernel detected locally. |
| M2/M3 regressions | 2026-10-02 | Additional race-tested scenarios prove dirty inspection preserves index bytes/mtime, unavailable remote does not affect local status, invalid/nested Git metadata stays visibly failed, and inherited config routing is stripped. Explicit config tilde expansion and empty CLI roots tested. | 386 config test binary compiles but cannot execute in this container (`bad system call`). No 32-bit support claim. |
| M4 | 2026-10-02 | `make fmt test race vet build` passed. Bubble Tea v2.0.10/Lip Gloss v2.0.6 actual APIs consulted and pinned; no Bubbles component needed. Model tests cover selection/filter clearing, path identity, stale loads, quit cancellation, Ctrl+C in filter/help, empty/narrow/safe views, scrollable full diagnostics, missing LazyGit and child-return refresh. WSL2 PTY exercised selection/filter/help/details/refresh, shell `pwd` and exit/restore, missing LazyGit, q from help, Ctrl+C from filter and NO_COLOR. | Actual LazyGit is absent; its successful launch/return remains untested (ExecProcess shell lifecycle passed). PTY child shells lack job control in this test harness; Windows Terminal physical resize/interactive job control not claimed. Network actions remain M5/M6. |
| M5 | 2026-10-02 | `make fmt test race vet build` passed. Local bare-remote and fake-service tests verify immutable executable previews, explicit selection, single active batch, common-dir serialization, mixed success/failure, changed plans, ambiguous/unusual refspec skips, cancellation/deadline events, and tag-pruning overrides. TUI tests pass 20 race runs. WSL2 PTY selected 10 demo repos, reviewed 9 eligible targets plus a skip, confirmed fetch, observed 8 successes/1 missing-remote failure/1 no-remote skip, and refreshed behind/diverged counts. `scripts/demo.sh` creates these disposable scenarios. Official Git fetch/config docs consulted. | M6 push/pull remain. External processes can still race after revalidation; cancelled fetch may partially update refs. Successful actual LazyGit launch remains untested. |
| M6 | 2026-10-02 | `make fmt test race vet build` passed. Disposable bare-remote tests verify conventional target resolution, dirty push exclusion, exact-OID push/FF pull, refused unsafe states and target config, remote-race non-force rejection, changed plans, unrelated branch/annotated-tag protection, shared-worktree serialization, and timeout/cancellation/output-limit uncertainty without retry. TUI two-confirmation tests pass 25 race runs. WSL2 PTY on `/tmp/repodash-demo.EynfaP` verified behind pull and ahead push: HEAD/remote unchanged before second confirmation, then exactly equal to reviewed OIDs. README and smoke checklist updated. | Fetch preflight uses a separate reviewed scope and confirmation; final push/pull needs a second confirmation. External tools/hooks can still race; no rollback. Linux/WSL only; M7 release artifact and final audit pending. |
| M4 follow-up | 2026-10-02 | Official LazyGit 0.65.1 Linux amd64 archive SHA-256 `02beacbcda0fa342e50ae3480ba8147307353af3fb28e1d5f790e02329c201a6` verified against its release checksums. `script -q -e -c 'bin/repodash --no-color /tmp/repodash-demo.EynfaP/workspace/behind' /dev/null` with a temporary PATH/XDG config opened LazyGit, showed the correct worktree, quit/restored/refreshed successfully. Child shell `pwd`, exit, actual terminal sizes 80×24→30×10→80×24 via `stty`, and Ctrl+C cleanup passed. | Direct harness PTY lacks a controlling `/dev/tty`; using `script` resolved this without an application change. Physical Windows Terminal interaction remains untested; no claim of Windows/macOS support. |
