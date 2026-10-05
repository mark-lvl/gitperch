# Mission control implementation

## Existing architecture

The CLI resolves TOML configuration and discovery roots in `cmd/repodash`.
`app.Load` discovers repositories and calls `app.Inspect`, which uses a bounded
worker pool and the existing deadline-limited Git runner. `app.Row` carries a
repository identity (absolute path), independent Git status dimensions, and last
successful fetch time. No agent/process tracking or workspace task domain exists.

The Bubble Tea v2 model owns refresh cancellation/generations, path-based bulk
selection, filtering, highlight/scroll position, and action progress. Lip Gloss
and ANSI cell measurements render the terminal. Fetch/push/fast-forward pull
already use `app.Actions` plans, confirmations, revalidation and progress events.
Push/pull first fetch a reviewed scope, then require a second confirmation.
These backend safeguards remain in place.

## Incremental implementation

1. Document the architecture and preserve the existing checkout changes.
2. Add semantic terminal colors/icons, UI settings, shared responsive geometry,
   a quiet repository table and selected-repository preview.
3. Add contextual commands, fuzzy action filtering, keyboard search, progressive
   repository details, and focus navigation. Reuse existing action plans.
4. Validate layout/state behavior, generate deterministic textual captures, and
   document operation and intentionally unavailable capabilities.

Rendering never launches Git processes. Preview/detail reads use a separate
cancelable asynchronous loader and cache; repository identity remains its path.
Wide terminals may show a compact attention panel, medium/narrow terminals use a
vertical list and preview. Height constraints remove previews before list rows.
No agent counts, fake messages, task buttons, or unavailable integrations appear.
Future agent integration should supply real metadata through the state/update
layer before adding commands or columns.

The checkout contained uncommitted UI scaffolding before this task, including
`model.go`, `actions.go`, `view.go`, and `view_test.go`. Redesign commits incorporate
that scaffolding where the new UI depends on it; unrelated edits stay unstaged.

## Completed validation

The implementation retains bounded asynchronous workspace inspection and guarded
Git actions, adding only on-demand read models to the runner. The UI now has
responsive repository/preview layout, a single optional attention panel, semantic
ANSI colors and icon modes, contextual fuzzy commands, keyboard filtering,
path-stable selection, progressive details, and existing confirmed bulk workflows.
There is no agent/task backend to surface; unsupported integrations remain absent.

Deterministic render captures cover 160×45, 110×35, 78×28, and 60×20. Tests cover
layout/columns, short heights, attention ordering, refresh identity, Unicode
truncation, all icon modes, fuzzy filtering, contextual actions, search, selection,
overlay precedence, asynchronous stale-result rejection, and read-only real Git
file/patch/history inspection (including staged/unstaged and unborn/rename cases).

Validation: `make check` (tests, vet, build) and `go test -race ./...`. A local Linux
PTY smoke run against the disposable demo exercised navigation, palette, search,
repository sections, help, six sizes from 160×45 to 45×8, quit, and alternate-screen
restoration. Windows Terminal and specific installed Nerd Fonts were not tested.
The linked design reference was inaccessible; the written UX specification guided
the implementation. Full usage and captures are in `docs/ui.md`.
