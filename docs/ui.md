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
branch metadata, and one thin outer frame. A single header line carries the
workspace, repository and attention badges, and a clock. The second line is a
quiet rule that names an active search or scope and the scroll range only when
they apply. The highlighted row has an accent bar; selected repositories carry
a small filled marker. Folder icons and status labels share the status color.
Tracking counts follow the status (`● changed ↑3`) unless the status already is
the tracking state (`↑ 3 commits`, `↓ 4 behind`). Zero tracking counts stay hidden.

The highlighted repository's preview is a bordered card: name, branch → upstream
and last activity, then its status line and changed files with colored status
chips. The dashboard fills the terminal height: the list starts at the top and
the preview card stays docked just above the key hints. On wide terminals the
card is sized for the last 10 commits beside the changed files; more than 10
changed files make it taller, up to the rows the list leaves free minus a blank
margin below the list. With a long list it keeps about a third of the screen.
Remaining spare rows sit between the list and the card. Key hints are filled keycaps on the bottom row; the least important ones drop
first on narrow terminals, and only keys that work for the highlighted
repository appear.

Columns follow three terminal tiers:

| Width | Columns | Preview |
| --- | --- | --- |
| Wide, 120+ | Repo · Branch · Status · Δ · Updated | Changed files beside recent commits with ages |
| Medium, 80–119 | Repo · Status · Δ | Stacked; the branch moves to the card |
| Narrow, < 80 | Row number · Repo · Status · Δ | Stacked; the upstream is dropped first |

**Updated** is the modification time of the worktree's HEAD reflog: the last
commit, checkout, pull or reset, read without starting another Git process. It
shows `—` when the reflog is absent. Default branches (`main`, `master`) are
dimmed so feature branches stand out. Below 20 rows the preview disappears
before list rows. The recommended minimum is 60×12; smaller
workspace screens show a resize message. Documents and reviews remain scrollable.
The command palette floats over the current workspace: a search box, then an
icon, title and short description per command, with an Enter badge on the
chosen one. It grows only enough to hold its results.

Path and branch truncation preserve filenames and useful branch prefixes/tails.
ANSI cell measurement handles wide characters and colored strings. Bubble Tea
converts theme colors to the detected terminal profile, including 256/16-color
terminals. `--no-color`, `NO_COLOR`, and ASCII icons remain supported.

## Keyboard

| Keys | Action |
| --- | --- |
| ↑/↓ or j/k | Navigate repositories |
| PgUp/PgDn, Home/End | Page or jump through the list |
| 1–9 | Jump to that visible row; narrow layouts show the numbers |
| Enter | Open repository Overview; while searching, open the selected result |
| d | Open Changes, including the tracked patch |
| Tab / Shift+Tab | Toggle All / Focus; in details, cycle sections |
| [ / ] | Scroll files in the selected repository preview |
| : or Ctrl+K | Fuzzy command palette; arrows choose, Enter runs, Esc closes |
| / | Search repository name, path, or branch; arrows navigate results |
| Esc | Clear/close search, return from details/help, cancel the confirmation popup |
| Space | Toggle path-based bulk selection |
| a | Select/deselect all visible repositories (existing binding) |
| p / l | Push / fast-forward pull selection or highlighted repository; one confirmation popup |
| f | Fetch explicitly selected repositories (no confirmation) |
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
Worktree. From 70 columns the sections are a side column, with the changed-file
count beside Changes; the content starts with the keys available for that
repository. Commits show their age. Overview shows a compact sample; Changes reveals every changed file and
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
`go build -o bin/gitperch ./cmd/gitperch`. Launch `bin/gitperch /path/to/workspace`.
To deliberately update reviewed captures and regenerate the PNGs (the latter
needs optional development packages `pillow` and `pyte`):

```sh
ansi=$(mktemp -d)
UPDATE_RENDERS=1 GITPERCH_ANSI_DIR="$ansi" go test ./internal/tui -run 'TestWorkspaceRenderCaptures|TestOverlayRenderCaptures'
python scripts/render-captures.py "$ansi"
```

Without `GITPERCH_ANSI_DIR` only the text captures are updated. These packages are not
application dependencies.

## Deliberately unavailable

Agents, semantic agent messages/waiting states, tests, pull requests and tasks have
no real backend integration here, so no fabricated counters, columns, tabs or
buttons appear. The reference mockup's AGENT column, agent badge and Agent tab,
Commit, Open pull request, Sync all and Start agent are therefore absent, and `a`
and `s` keep their existing select-all and sort bindings. The detail-loader and command registry are the seams for later
integration. Stage/commit/conflict resolution are available through the real shell
or optional LazyGit, not new embedded mutation workflows. Line counts are fetched
only for the highlighted repository; the table retains existing cheap Git status
and tracking data. No unsupported editor-launch configuration is guessed.

Terminal snapshots and a local PTY check do not establish Windows Terminal or
specific Nerd Font compatibility; the [manual smoke guide](tui-smoke.md) remains a
useful checklist.
