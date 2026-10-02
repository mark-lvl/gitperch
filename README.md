# repodash

A terminal dashboard for multiple local Git repositories, targeting Linux and WSL2.
Implementation follows [plan.md](plan.md) in separately committed milestones.

## Development

Go 1.27.1 and the system Git CLI are required. Bootstrap was performed with
Go 1.27.1 (official Linux amd64 archive, SHA-256
`63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445`)
and Git 2.43.0. The module name is local (`repodash`); no remote or license has
been chosen. macOS/Windows support is not yet validated.

```sh
make fmt test vet build
make race
go run ./cmd/repodash --help
go run ./cmd/repodash --version
```

`make build` writes `bin/repodash`. For this coding environment, the downloaded
toolchain lives in `/tmp/repodash-toolchain/go`; use
`PATH=/tmp/repodash-toolchain/go/bin:$PATH` and `GOCACHE=/tmp/repodash-gocache`
when running the commands above. A normal installation of Go needs neither override.

## Discovery (M1)

```sh
go run ./cmd/repodash status .
go run ./cmd/repodash status --max-depth 4 /path/to/projects
```

This milestone prints **discovery only**, with quoted absolute paths to distinguish
duplicate names and escape terminal controls. Root depth is zero. Scanning stops
at a repository; explicitly supplied nested repositories are still eligible.
Both `.git` directories and regular worktree `.git` files are candidates.
Bare repositories are not candidates. Directory symlinks and roots reached via
symlink ancestors are skipped. Descendants named `node_modules`, `vendor`,
`target`, `.cache`, `.next`, `dist`, or `build` are ignored.

Missing or inaccessible roots print warnings while valid roots continue.
Exit codes: 0 completed, 1 partial failure, 2 invalid invocation, 130 interrupted.
No Git mutations or TUI yet.
