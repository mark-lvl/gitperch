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

The workspace uses a restrained dark canvas, cyan selection accents, magenta
branch metadata, small status badges, and one thin outer frame. Content follows
the repository rows: a small workspace never creates a large empty gap above its
preview. Unselected rows have no permanent checkbox; selected repositories carry
a small filled marker. Zero tracking counts stay hidden.

At 120+ columns, the selected preview can place changed files beside real recent
commits. Smaller layouts stack content vertically. The table drops branch and
tracking columns when its content width is below 80 columns. Below 20 rows the
preview disappears before list rows. The recommended minimum is 60×12; smaller
workspace screens show a resize message. Documents and reviews remain scrollable.
The command palette floats over the current workspace and includes short command
descriptions. It grows only enough to hold its results.

Path and branch truncation preserve filenames and useful branch prefixes/tails.
ANSI cell measurement handles wide characters and colored strings. Bubble Tea
converts theme colors to the detected terminal profile, including 256/16-color
terminals. `--no-color`, `NO_COLOR`, and ASCII icons remain supported.

## Keyboard

| Keys | Action |
| --- | --- |
| ↑/↓ or j/k | Navigate repositories |
| PgUp/PgDn, Home/End | Page or jump through the list |
| Enter | Open repository Overview; while searching, open the selected result |
| d | Open Changes, including the tracked patch |
| Tab / Shift+Tab | Toggle All / Focus; in details, cycle sections |
| [ / ] | Scroll files in the selected repository preview |
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

The palette exposes Changed/Ahead/Behind/Issues filters and Focus / All, plus the less-common
selection, ordering, shell, fetch, and detail commands. Unsupported push/pull
shortcuts are omitted based on known state; final plans always revalidate. Bulk
plans may include ineligible repositories and explicitly explain skips.

Repository details provide Overview, Changes, Commits (eight recent commits), and
Worktree. Overview shows a compact sample; Changes reveals every changed file and
the colored patch. Preview reads are asynchronous, canceled when navigating to
another uncached repository, protected against stale results, and cached until
refresh. Full patches load only while Changes is open, cancel on leaving, and
keep only the most recently opened repository's patch in memory.
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

Colors degrade to the emulator's supported color profile. No additional theme file,
automatic refresh timer, or unsafe confirmation bypass is introduced.

## Render captures and validation

The deterministic captures use test fixtures, rendered by the production view.
PNG captures are rasterized from its actual ANSI output:

- [Wide, 160×45](captures/workspace-160x45.png) · [text](captures/workspace-160x45.txt)
- [Medium, 110×35](captures/workspace-110x35.png) · [text](captures/workspace-110x35.txt)
- [Narrow, 78×28](captures/workspace-78x28.png) · [text](captures/workspace-78x28.txt)
- [Minimum width, 60×20](captures/workspace-60x20.png) · [text](captures/workspace-60x20.txt)
- [Command palette](captures/palette-110x35.png)
- [Repository overview](captures/details-110x35.png)

Run `go test ./...`, `go vet ./...`, `go test -race ./...`, and
`go build -o bin/repodash ./cmd/repodash`. Launch `bin/repodash /path/to/workspace`.
To deliberately update reviewed captures and regenerate the PNGs (the latter
needs optional development packages `pillow` and `pyte`):

```sh
ansi=$(mktemp -d)
UPDATE_RENDERS=1 REPODASH_ANSI_DIR="$ansi" go test ./internal/tui -run 'TestWorkspaceRenderCaptures|TestOverlayRenderCaptures'
python scripts/render-captures.py "$ansi"
```

Without `REPODASH_ANSI_DIR` only the text captures are updated. These packages are not
application dependencies.

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
useful checklist.
