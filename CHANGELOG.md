# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The workspace groups linked worktrees under their main repository, including
  worktrees nested inside a repository, outside the scanned roots, or whose
  directory no longer exists. `→`/`←` expand and collapse a group, a collapsed
  group's badge counts stale worktrees and hidden ones that need attention, and
  the details Worktree section lists the group.
- Clean up (`c`) reviews and removes stale worktree records and clean linked
  worktrees already merged into the remote default branch. Dirty, locked,
  unmerged worktrees, worktrees with ignored files and worktrees with
  assume-unchanged or present skip-worktree files are kept with the reason
  shown; a stale record whose commit no branch or tag reaches is kept too, with
  the `git branch` command that saves it; nothing is forced.
- Clean up also deletes local branches fully merged into the remote default
  branch, only at the commit you reviewed; Diagnostics (`d`) lists a
  `git branch <name> <commit>` command to restore each one. The commands are also
  appended to `$XDG_STATE_HOME/gitperch/cleanup.log` (by default
  `~/.local/state/gitperch/cleanup.log`) and
  printed when the dashboard closes. Worktree and branch cleanup need Git 2.36
  or newer.
- `gitperch status --json` reports a `worktree` object per repository (schema
  version 1, additive).

## [0.1.1] - 2026-10-06

### Changed

- Repository details load much faster for large change sets: matching line
  statistics to 20,000 changed files dropped from about 650 ms to 2 ms.
- Scrolling a large diff in the Changes section stays responsive: the patch is
  wrapped once per terminal width instead of on every frame and keypress
  (about 12 ms to under 1 ms per step for a 20,000-line patch).
- Push and pull fetch their targets concurrently before the confirmation
  popup, up to `action_workers` at a time and still one at a time per shared
  Git directory, instead of one repository after another.
- Each status inspection starts one fewer Git process: a single `rev-parse`
  now resolves both the common and per-worktree Git directories.

### Fixed

- The search filter and command palette accept capital letters and other
  shifted characters instead of silently dropping them.
- Starting an action during a status refresh no longer stops automatic
  refresh when the action ends without running (cancelled, nothing eligible
  or confirmation dismissed); the interrupted refresh now resumes.
- A rejected push now reports Git's reason, such as
  `[rejected] (fetch first) for refs/heads/main`, so the result and the
  "remote history needs review" guidance appear even when Git's advice hints
  are turned off.
- A repository whose detached HEAD sits on a tag named like a local branch
  with an upstream is no longer shown as failed ("malformed porcelain-v2
  record"); it shows as detached with no upstream.
- While an action is being prepared, the footer shows "Preparing…" instead of
  "Batch running" with the previous batch's counts (for example "2/1
  finished").
- Cancelling an action while it is being prepared reports "Preview preparation
  cancelled" instead of the raw "context canceled".
- `gitperch -- status` opens the dashboard on a directory named `status`
  instead of running the `status` command; `--` now ends option and
  subcommand parsing.

## [0.1.0] - 2026-10-06

### Changed

- Renamed the project from `repodash` to **gitperch** and positioned it as a Git
  dashboard for overseeing repositories used in AI agentic development.
- The Go module path is now `github.com/mark-lvl/gitperch`, the binary
  is `gitperch`, and the config file moved to `~/.config/gitperch/config.toml`
  (previously `~/.config/repodash/config.toml`).
- Detailed usage documentation moved from the README to `docs/usage.md`, and
  development notes to `docs/development.md`.

### Added

- Recursive workspace discovery, TOML configuration, plain status output and
  JSON status schema version 1.
- Interactive dashboard with navigation, filtering, selection, command
  palette, repository details, diagnostics, child shell and LazyGit launch.
- Fetch, push of reviewed commits and fast-forward-only pull with plans,
  confirmation and revalidation.
- Prebuilt Linux amd64 and arm64 archives with SHA-256 checksums, published
  to GitHub releases from version tags. `gitperch --version` also reports the
  module version for `go install ...@vX.Y.Z` builds.
- A logo: a songbird perched on a line of commits, in `docs/assets/` and at
  the top of the README. The dashboard draws it with text characters above the
  message while the first scan runs and when no repositories are found.
- The dashboard refreshes local status automatically while idle, every 30
  seconds by default (`[ui] refresh_seconds`, `0` disables). The footer now
  shows the `r` manual refresh key.
- The dashboard fills the terminal height, keeping the selected repository
  preview and the key hints anchored to the bottom. The preview shows the last
  10 commits beside the changed files and grows to fit more than 10 changed
  files when the terminal has room.
- MIT license, contributing guide, code of conduct, security policy, agent
  instructions (`AGENTS.md`), issue and pull request templates, and Dependabot
  configuration.

[Unreleased]: https://github.com/mark-lvl/gitperch/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/mark-lvl/gitperch/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mark-lvl/gitperch/releases/tag/v0.1.0
