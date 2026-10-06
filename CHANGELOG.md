# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/mark-lvl/gitperch/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mark-lvl/gitperch/releases/tag/v0.1.0
