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
