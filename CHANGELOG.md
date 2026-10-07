# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `gitperch status --json` reports an `attention` object per repository with a
  level (`critical`, `high`, `medium`, `low`) and the reasons behind it, such as
  `conflicts` or `unpushed_commits`; the plain table gains an `ATTENTION`
  column. Repository details list the reasons, for example
  `2 commits ahead of origin/main`.
- Linked worktrees get a lifecycle inferred from their Git state: `blocked`,
  `in_progress`, `active`, `likely_finished` (clean, merged into the default
  branch and quiet for a day), `idle` (clean and quiet for 14 days) or
  `unknown`. The status column shows `✓ finished?` or `idle 21d`, the details
  Worktree section lists the signals behind the state, and both raise medium
  attention. JSON adds a `lifecycle` object and `worktree.integration`. It is a
  suggestion only: Clean up still decides removal against a fresh fetch.
- Clean up explains each decision. Kept items show a status (`blocked`,
  `unknown`, `review` or `failed`) and every reason found, such as
  `1 uncommitted file; 1 untracked file; not merged into origin/main`, and the
  highlighted item lists the checks it passed. Unmerged worktrees now say
  whether their commits are unpushed, diverged, published, or reachable only
  from a detached HEAD (with the `git branch rescue/…` command that saves
  them).
- A removed worktree's result includes the command that adds it back, such as
  `git -C <repository> worktree add <path> <branch>`, and the status line
  points to it.

### Changed

- Attention order ranks repositories by level, then name: conflicts,
  interrupted operations and failures first, then uncommitted, untracked or
  unpushed work and diverged branches, then repositories behind or without an
  upstream. Unpushed commits now rank with uncommitted changes rather than
  with being behind.
- A detached HEAD needs attention only when no branch, remote-tracking branch
  or tag contains its commit; otherwise it shows a muted `detached` status.
- The header's attention chip turns red when a repository needs intervention.
- Clean up skips a worktree whose checked-out branch changed since review,
  even at the same commit, and asks you to review again. Worktree reasons
  read like attention reasons: `dirty (3 files)` is now
  `2 uncommitted files; 1 untracked file`.

- A new logo: a songbird perched on a branch of commits that forks. The
  README, icon and the mark the dashboard draws while the first scan runs and
  when no repositories are found all use it. The original artwork is
  `docs/assets/logo.png`.
- Pasting text into search or the command palette types it, without control
  characters or line breaks; before, a paste was ignored.
- Checking whether a detached HEAD's commit is on a branch or tag takes one
  history walk instead of one per ref, which is much faster with many tags.

### Fixed

- A repository with more than about 100,000 changed or untracked files no
  longer shows `Git output exceeded capture limit` and no longer blocks fetch,
  push and pull.
- A worktree whose status cannot be read is listed once, not twice.
- Clean up keeps a branch that a stale worktree record still names when the
  record is not pruned first (prune blocked, not chosen or failed). It also
  says why no branches were considered when the default branch is unknown,
  instead of `Nothing to clean up`.
- Push accepts `remote.<name>.push = HEAD`. Pull and push accept duplicate
  fetch refspecs that map the upstream to the same tracking ref.
- `gitperch status` no longer prints `no upstream` for detached, unborn or
  failed repositories.
- The list no longer leaves rows hidden above empty space after it shrinks or
  the terminal grows. The Clean up review and the push/pull popup keep the
  highlighted item and their footer on screen. Collapsing a group in a search
  or scope keeps the selection of worktrees still shown. With
  `icons = "ascii"`, lines showing `->` keep their right border.

## [0.2.0] - 2026-10-07

### Added

- The workspace groups linked worktrees under their main repository, including
  worktrees nested inside a repository, outside the scanned roots, or whose
  directory no longer exists. `→`/`←` expand and collapse a group, a collapsed
  group's badge counts stale worktrees and hidden ones that need attention, and
  the details Worktree section lists the group.
- Clean up (`c`) reviews and removes stale worktree records and clean linked
  worktrees already merged into the remote default branch. Dirty, locked and
  unmerged worktrees, and worktrees with ignored, assume-unchanged or present
  skip-worktree files, are kept with the reason shown; a stale record whose
  commit no branch or tag reaches is kept too, with the `git branch` command
  that saves it. Nothing is forced.
- Clean up also deletes local branches fully merged into the remote default
  branch, only at the commit you reviewed. Diagnostics (`d`) lists a
  `git branch <name> <commit>` command to restore each one; the commands are
  also appended to `$XDG_STATE_HOME/gitperch/cleanup.log` (by default
  `~/.local/state/gitperch/cleanup.log`) and printed when the dashboard closes.
- `gitperch status --json` reports a `worktree` object per repository (schema
  version 1, additive).
- Worktree grouping and cleanup need Git 2.36 or newer; with older Git they are
  off and everything else works as before.

### Changed

- The repository icon is a filled `▰` in a color of its own, derived from the
  repository's path and shared by its worktrees, preview card and Clean up
  heading, instead of the status color; status keeps its color in the status
  column. The palette stays distinguishable with color-vision deficiency and
  apart from status colors.

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

[Unreleased]: https://github.com/mark-lvl/gitperch/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/mark-lvl/gitperch/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/mark-lvl/gitperch/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mark-lvl/gitperch/releases/tag/v0.1.0
