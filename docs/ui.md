# Mission control

The workspace answers what needs attention, what the selected repository contains,
and what can be done next. The Git backend and synchronization safeguards are
unchanged. There is no production demo data.

The compact header counts repositories and attention. Attention covers failed
inspection/operations, conflicts, interrupted Git operations, divergence, local
changes, unknown tracking, and pending commits. Healthy repositories stay quiet.
The table shows one primary status plus independent ahead/behind counts, using
locally known refs. Fetch checks remote state. Untracked files have no line count;
tracked counts sum staged and unstaged changes rather than claiming a net diff.

## Responsive layout

At 120+ columns and 20+ rows, a single compact attention panel accompanies the
repository list. At narrower sizes the list and selected preview stack vertically.
Branch columns appear when the list has at least 90 columns; otherwise the preview
provides branch/upstream context. Below 20 rows the preview disappears before list
rows. The recommended minimum is 60×12; smaller workspace screens show a resize
message. Documents and reviews remain scrollable with fixed titles and footers.

Path and branch truncation preserve filenames and useful branch prefixes/tails.
ANSI cell measurement handles wide characters and colored strings. Colors use the
terminal's basic palette, and every status also has text/symbols. `--no-color` and
`NO_COLOR` are supported.

## Keyboard

| Keys | Action |
| --- | --- |
| ↑/↓ or j/k | Navigate repositories |
| PgUp/PgDn, Home/End | Page or jump through the list |
| Enter | Open repository Overview; while searching, open the selected result |
| d | Open Changes, including the tracked patch |
| Tab / Shift+Tab | Cycle repository scopes; in details, cycle sections |
| : or Ctrl+K | Fuzzy command palette; arrows choose, Enter runs, Esc closes |
| / | Search repository name, path, or branch; arrows navigate results |
| Esc | Clear/close search, return from details/help, cancel reviews |
| Space | Toggle path-based bulk selection |
| a | Select/deselect all visible repositories (existing binding) |
| p / l | Review push / fast-forward pull for selection, or highlighted repository |
| f | Review fetch for explicitly selected repositories |
| o | Open an interactive shell in the selected worktree |
| g | Open LazyGit; palette lists it only when installed |
| r | Refresh workspace and invalidate cached details |
| s | Toggle name/attention sorting while preserving highlighted path |
| ? | Scrollable help grouped by context |
| q / Ctrl+C | Quit / interrupt; during operations request cancellation and wait |

The palette exposes Focus / All as a direct toggle, as well as the less-common
selection, ordering, shell, fetch, and detail commands. Unsupported push/pull
shortcuts are omitted based on known state; final plans always revalidate. Bulk
plans may include ineligible repositories and explicitly explain skips.

Repository details provide Overview, Changes, Commits (eight recent commits), and
Worktree. Preview reads are asynchronous, canceled when navigating to another
uncached repository, protected against stale results, and cached until refresh.
Rendering launches no Git commands. Full Git errors, discovery warnings, and
operation results remain accessible in details. No automatic rebase or repair runs.

Fetch uses one confirmation. Push/pull review the fetch scope first, then require
another confirmation of the exact synchronization plan. Reviews summarize all
selected targets; progress/results appear in the repository rows and details.
Actions dispatched from the palette explicitly select the highlighted repository
when there is no existing selection. Search/scope changes clear selection to avoid
operating on hidden targets; refresh retains existing visible selections by path.

## Configuration

Add these settings to the existing configuration file:

```toml
[ui]
icons = "unicode"       # default; also "ascii" or "nerd"
default_focus = false   # start in the attention scope when true
```

Terminal palette colors follow the emulator's theme. No additional theme file,
automatic refresh timer, or unsafe confirmation bypass is introduced.

## Render captures and validation

The deterministic captures use test fixtures, rendered by the production view:

- [Wide, 160×45](captures/workspace-160x45.txt)
- [Medium, 110×35](captures/workspace-110x35.txt)
- [Narrow, 78×28](captures/workspace-78x28.txt)
- [Minimum width, 60×20](captures/workspace-60x20.txt)

Run `go test ./...`, `go vet ./...`, `go test -race ./...`, and
`go build -o bin/repodash ./cmd/repodash`. Launch `bin/repodash /path/to/workspace`.
To deliberately update reviewed captures:
`UPDATE_RENDERS=1 go test ./internal/tui -run TestWorkspaceRenderCaptures`.

## Deliberately unavailable

Agents, semantic agent messages/waiting states, tests, pull requests and tasks have
no real backend integration here, so no fabricated counters, columns, tabs or
buttons appear. The detail-loader and command registry are the seams for later
integration. Stage/commit/conflict resolution are available through the real shell
or optional LazyGit, not new embedded mutation workflows. Line counts are fetched
only for the highlighted repository; the table retains existing cheap Git status
and tracking data. No unsupported editor-launch configuration is guessed.

Terminal snapshots and a local PTY check do not establish Windows Terminal or
specific Nerd Font compatibility; the [manual smoke guide](tui-smoke.md) remains a
useful checklist, with Enter now opening details and `o` opening a shell.
