# Instructions for AI coding agents

This file guides AI agents (Claude Code, Codex, Cursor, Aider, ...) working in
this repository. Human contributors should read [CONTRIBUTING.md](CONTRIBUTING.md).

## Project

gitperch is a Go terminal dashboard (Bubble Tea v2, Lip Gloss v2) that gives an
overview of many local Git repositories and safely fetches, pushes and
fast-forward pulls them. Module path: `github.com/mark-lvl/gitperch`.

## Commands

```sh
make check        # fmt-check, test, race, vet, build (mirrors CI); run before finishing
make test         # go test ./...
go run ./cmd/gitperch status --json .
```

## Rules

- Read the relevant code and docs before editing; preserve unrelated changes.
- Keep domain logic in `internal/app`, `internal/git`, `internal/discovery` and
  `internal/config`; the TUI (`internal/tui`) only renders and dispatches.
- Invoke Git only through the runner in `internal/git` with argument arrays.
  Never interpolate into shell strings.
- Never add Git mutations beyond fetch, push of reviewed commits and
  fast-forward-only pull without an agreed design. No force push, reset,
  stash, clean, commit or rebase.
- Tests that touch Git must use temporary repositories and local bare remotes.
  Never use personal or network remotes.
- Do not show fabricated data in the UI (for example agent status without a
  real integration).
- After TUI changes, update render captures as described in
  `docs/ui.md#render-captures-and-validation`.
- Add an entry under `Unreleased` in `CHANGELOG.md` for user-visible changes.
- Do not bump Go module dependencies, create releases or tags, push, or change
  the license unless asked. Releases follow `docs/releasing.md`.
- Use Conventional Commit messages.
