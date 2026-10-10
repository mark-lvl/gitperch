# Development guide

## Requirements

- Go 1.27.1 or newer to build from source (see `go.mod`).
- The Git CLI at runtime; development and validation used Git 2.43.0.
- Linux, WSL2 or macOS; CI tests Linux and macOS on Apple Silicon and Intel.
  Windows builds are expected to compile but are not yet validated.
- `make dist` uses GNU tar for reproducible archives, so build release
  archives on Linux.
- Optional: [LazyGit](https://github.com/jesseduffield/lazygit) for the
  in-dashboard LazyGit launcher, the [GitHub CLI](https://cli.github.com) for
  pull request status, and Python with `pillow` and `pyte` to regenerate PNG
  render captures.

## Common tasks

```sh
make fmt          # gofmt every package
make test         # go test ./...
make race         # go test -race ./...
make vet          # go vet ./...
make build        # writes bin/gitperch
make check        # everything CI runs: fmt-check test race vet build
```

Run the binary from source with:

```sh
go run ./cmd/gitperch --help
go run ./cmd/gitperch --version
```

`go install ./cmd/gitperch` places `gitperch` in Go's `GOBIN` (or
`$(go env GOPATH)/bin`). Add that directory to `PATH` to call it from anywhere.

## Auto-rebuilding launcher

For day-to-day development, install a launcher that rebuilds the checkout
before every start:

```sh
make install-dev
gitperch /path/to/projects
```

This installs a symlink at `~/.local/bin/gitperch`. Each launch incrementally
rebuilds this checkout into `bin/gitperch`, preserving your current directory
and arguments. Build errors stop the launch and leave the previous binary
intact. An already running dashboard keeps its original build; quit and
relaunch to pick up edits. The checkout must stay where it was installed.

The launcher uses Go from `PATH`, or `GO=/path/to/go`. It also accepts a
privately owned toolchain under `/tmp/gitperch-toolchain` for sandboxed
environments without a system Go. `INSTALL_DIR=/another/directory make
install-dev` changes the install location. The installer refuses to overwrite
an unrelated existing command. Remove the symlink to uninstall; this does not
remove the checkout or binary.

## Demo workspace

`scripts/demo.sh` builds a disposable set of repositories with local bare
remotes (no network, no personal repositories):

```sh
demo_root=$(bash scripts/demo.sh)
bin/gitperch "$demo_root/workspace"
```

It covers clean, behind, ahead, dirty, diverged, detached, unborn,
missing-upstream, failed-remote and linked-worktree scenarios.

## Tests

- Tests that mutate Git state use temporary repositories and local bare
  remotes. Never point tests at personal or work remotes.
- Tests never run the real `gh` or contact GitHub (CI runners have gh
  installed): they write fake `gh` executables or use in-memory fakes.
- `testdata/git-status` holds real porcelain v2 output; set
  `GITPERCH_UPDATE_FIXTURES=1` to recapture it.
- TUI render captures live in `docs/captures`. See
  [Render captures](ui.md#render-captures-and-validation) to update them.
- [tui-smoke.md](tui-smoke.md) is the manual terminal smoke checklist.

## Releases

Record user-visible changes under `Unreleased` in
[CHANGELOG.md](../CHANGELOG.md) as you make them. `make dist` builds the
release archives and `checksums.txt` under `dist/` without publishing
anything. [releasing.md](releasing.md) covers versioning and how to cut a
release.

## Project layout

```text
cmd/gitperch/        CLI entry point, flags and TUI bootstrap
internal/app/        status loading, action plans and execution
internal/config/     TOML configuration and workspace resolution
internal/discovery/  repository discovery with depth and symlink safety
internal/git/        Git CLI runner, porcelain parsing and sync commands
internal/github/     gh runner, remote URL parsing and pull request queries
internal/repository/ repository identity and status model
internal/tui/        Bubble Tea v2 interface
scripts/             demo, dev launcher, capture rendering and release tooling
testdata/            recorded Git output used by tests
docs/                user guides, design notes, render captures and logo files
```

Domain logic stays independent of the TUI, and Git and gh are always invoked
through argument arrays, never through a shell.
