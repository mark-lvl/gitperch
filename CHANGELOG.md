# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Renamed the project from `repodash` to **gitperch** and positioned it as a Git
  dashboard for overseeing repositories used in AI agentic development.
- The Go module path is now `github.com/markkaghazgarian/gitperch`, the binary
  is `gitperch`, and the config file moved to `~/.config/gitperch/config.toml`
  (previously `~/.config/repodash/config.toml`).
- Detailed usage documentation moved from the README to `docs/usage.md`, and
  development notes to `docs/development.md`.

### Added

- MIT license, contributing guide, code of conduct, security policy, agent
  instructions (`AGENTS.md`), issue and pull request templates, and Dependabot
  configuration.

## [0.1.0] - Unreleased draft

First feature-complete version, prepared locally and not yet published. See
the [draft release notes](docs/release-notes-v0.1.0.md).

### Added

- Recursive workspace discovery, TOML configuration, plain status output and
  JSON status schema version 1.
- Interactive dashboard with navigation, filtering, selection, command
  palette, repository details, diagnostics, child shell and LazyGit launch.
- Fetch, push of reviewed commits and fast-forward-only pull with plans,
  confirmation and revalidation.

[Unreleased]: https://github.com/markkaghazgarian/gitperch/commits/main
[0.1.0]: docs/release-notes-v0.1.0.md
