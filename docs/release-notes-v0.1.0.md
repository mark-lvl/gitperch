# gitperch v0.1.0 — draft release notes

This file contains prepared local release notes. It does not announce a
published release. Local milestone gates passed; remote CI has not been run.

gitperch is a terminal dashboard for discovering local Git repositories,
reviewing their status, and synchronizing selected worktrees. The current
implementation targets Linux and WSL2. macOS and Windows have not been
validated. Development and local validation used Go 1.27.1 and Git 2.43.0.

## Included

- Recursive workspace discovery, TOML configuration, plain status output, and
  JSON status schema version 1.
- Interactive navigation, filtering, explicit selection, diagnostics, refresh,
  child shell, and LazyGit launch.
- Fetch preview and execution, plus two-stage push and fast-forward pull. Push
  and pull first show a fetch scope; after fetch and refreshed comparison, a
  second preview shows the exact commit and target before execution.
- Push of reviewed existing commits to a conventional unambiguous upstream;
  uncommitted changes are excluded. Pull requires a clean worktree and only
  integrates the reviewed commit by fast-forward.

## Configuration and installation

The config file defaults to `$XDG_CONFIG_HOME/gitperch/config.toml` on
Linux/WSL2, falling back to `~/.config/gitperch/config.toml`. Configurable
settings are `default_workspace`, `status_workers`, `action_workers`,
`status_timeout_seconds`, `action_timeout_seconds`, and per-workspace `name`,
`paths`, `max_depth`, and `ignore_dirs`. An absent default config scans the
current directory. See the [usage guide](usage.md) for a TOML example and command details.

Build and run from a source checkout:

```sh
make build
make release-linux
./bin/gitperch --help
./bin/gitperch /path/to/projects
```

There are no remote installation instructions: the module has no known
publication URL and no release artifact is being distributed.
The locally prepared static Linux amd64 artifact is
`dist/gitperch_0.1.0_linux_amd64`, built with Go 1.27.1, with SHA-256
`74db00624fca6675f67c460ac4039fc104022828d3d5f4a616048bb38bdf22e1`.

Formatting, tests (including a fresh race-enabled run), vet, and both development
and release builds passed. The disposable WSL demo exercised discovery, status,
selection, reviewed fetch, two-stage push/pull, and refreshed results. The release
binary's version, JSON status, failed-batch exit code 1 and Ctrl+C exit code 130
were also checked.

## Safety and limitations

Fetch, push, and pull show their scope before execution. Push does not force,
implicitly follow tags, or push submodules. Pull does not stash, merge
divergent history, or rebase. Unsupported or ambiguous targets are skipped with
reasons. Batches are best effort and have no rollback. A push timeout or
cancellation can leave its remote outcome unknown; gitperch does not retry it
automatically. External Git processes and configured hooks can still affect a
repository.

Staging, committing, stashing, reset/clean, conflict resolution, rebase, force
push, branch/upstream setup, cloning, headless bulk mutation, and triangular
push workflows are outside this version's scope. Normal Git credential helpers
and SSH agents are supported; background commands cannot prompt interactively.

The project owner, license, repository publication, and release publication
remain undecided. Manual WSL validation includes a scripted controlling-terminal
check of LazyGit 0.65.1 launch/quit/return, child-shell directory behavior,
terminal resizing from 80x24 to 30x10 and back, and Ctrl+C. A physical Windows
Terminal check remains unperformed.
