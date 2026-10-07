<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
  <img src="docs/assets/logo-light.svg" alt="gitperch logo: a songbird perched on a branch of commits" width="200">
</picture>

# gitperch

**Keep agent-touched repositories and worktrees visible, synchronized and safe.**

[![CI](https://github.com/mark-lvl/gitperch/actions/workflows/ci.yml/badge.svg)](https://github.com/mark-lvl/gitperch/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/mark-lvl/gitperch.svg)](https://pkg.go.dev/github.com/mark-lvl/gitperch)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

![gitperch workspace view](docs/captures/workspace-110x35.png)

</div>

gitperch is a terminal dashboard for keeping an eye on your Git repositories
and worktrees while coding agents are busy changing them, or whenever you
juggle more of them than you can track in your head. It shows what needs
attention, lets you inspect what changed, safely syncs repositories and cleans
up finished worktrees, all without visiting each directory to run
`git status`.

```sh
gitperch ~/projects                   # interactive dashboard
gitperch status ~/projects            # plain table, no TUI
gitperch status --json ~/projects     # JSON for scripts and agents
```

[Install](#installation) · [Keys](#using-the-dashboard) · [Configuration](#configuration) · [Docs](#documentation)

## Why gitperch exists

Coding agents such as Claude Code, Codex, OpenCode, Aider and Cursor make it
easy to work on several things at once. They also make it easy to lose track
of what they left behind. After a few parallel sessions you may have
uncommitted changes, unpushed commits, branches behind their upstream,
half-finished merges or rebases, and old worktrees whose task ended long ago.

None of that is hard to inspect one repository at a time. The hard part is
keeping the whole picture in your head. gitperch answers questions like:

- Which repositories need my attention, and what did the agents leave behind?
- Is anything uncommitted, unpushed, behind or diverged?
- Which worktrees are still useful, and which can go?
- Can this repository be updated safely, and did anything change since I reviewed it?

It discovers every repository and linked worktree under your project roots,
inspects them concurrently and ranks them so conflicts and unfinished work rise
above what is already clean. The dashboard is less a directory browser and more
an **attention list**.

## Conservative by design

Repositories can change between the moment you look and the moment you act,
especially with several terminals, editors and agents touching the same
project. Every operation that changes state follows the same path:

```text
inspect → plan → review → revalidate → execute → refresh
```

- **Push** sends only the commit and destination you reviewed, not whatever
  `HEAD` points to later.
- **Pull** is fast-forward-only; it never creates a merge or rebase.
- **Worktree cleanup** removes only clean worktrees already merged into the
  default branch. Local changes, ignored files (such as `.env`), files hidden
  from `git status`, operations in progress, locks and anything that changed
  since your review all keep a worktree in place.
- gitperch never commits, stashes, resets, rebases, force-pushes or
  force-removes. When it cannot be confident an action is safe, it stops and
  tells you why.

## Features

- **Workspace discovery**: recursive scan of one or more roots with depth
  limits, ignored directories, symlink safety and linked-worktree support.
- **Attention-first overview**: changes, untracked files, conflicts, branch,
  upstream and ahead/behind counts for every repository, sorted by what needs
  you first.
- **First-class worktrees**: every linked worktree, including nested,
  outside-root and stale ones, grouped under its repository.
- **Reviewed cleanup**: prune stale worktree records and remove clean, merged
  worktrees and local branches.
- **Repository details**: overview, changed files, recent commits and worktree
  state, plus a diff view.
- **Safe synchronization**: fetch, push and fast-forward-only pull across a
  selection, with per-repository results.
- **Fast navigation**: fuzzy command palette (`:` / `Ctrl+K`), filtering,
  focus mode and multi-select.
- **Scriptable status**: `gitperch status` prints a plain table or versioned
  JSON for scripts, CI and agent tooling.
- **Hardened Git execution**: no shell interpolation, bounded output and
  deadlines, no interactive credential prompts, control characters escaped and
  credentials in URLs redacted.

## Where gitperch fits

gitperch does not replace Git, lazygit or your shell. For staging, interactive
rebases or conflict surgery it opens a shell or lazygit in the repository and
refreshes when you return. Nor is it an agent orchestrator: it observes the Git
state agents leave behind rather than controlling them. Use your favourite
agent to create the work, then gitperch to answer: _what state did all of that
leave my repositories in?_

| Tool | Interface | What it is for |
| --- | --- | --- |
| **gitperch** | TUI + plain/JSON status | Overseeing many repositories and worktrees; fetch, push, fast-forward-only pull and cleanup after review |
| [lazygit](https://github.com/jesseduffield/lazygit) | TUI | Working inside one repository: staging, committing, rebasing |
| [gita](https://github.com/nosarthur/gita) | CLI | Side-by-side status of registered repositories and running commands across them |
| [myrepos](https://myrepos.branchable.com/) (`mr`) | CLI | Running commands across repositories listed in a config file, for several VCSs |
| [multi-git-status](https://github.com/fboender/multi-git-status) | CLI | One-shot report of uncommitted, unpushed and unpulled changes under a directory |

Unlike the multi-repository runners, gitperch needs no per-repository
registration and understands linked worktrees.

## Installation

gitperch requires the `git` CLI at runtime. Linux and WSL2 are the supported
platforms today; macOS and Windows are not yet validated.

Prebuilt Linux binaries (amd64 and arm64) are attached to each
[GitHub release](https://github.com/mark-lvl/gitperch/releases). Download the
archive for your architecture and `checksums.txt`, then:

```sh
sha256sum --check --ignore-missing checksums.txt
tar -xzf gitperch_*_linux_amd64.tar.gz
install -m 0755 gitperch_*_linux_amd64/gitperch ~/.local/bin/
```

With Go 1.27.1 or newer:

```sh
go install github.com/mark-lvl/gitperch/cmd/gitperch@latest
```

From source:

```sh
git clone https://github.com/mark-lvl/gitperch.git
cd gitperch
make build          # writes bin/gitperch
```

## Using the dashboard

| Key | Action |
| --- | --- |
| `↑`/`↓`, `j`/`k` | Move |
| `Enter` | Repository details |
| `d` | Changes / diff |
| `Space`, `a` | Select one / all visible |
| `f` | Fetch selected |
| `p` / `l` | Push / fast-forward pull (after one confirmation) |
| `c` | Review worktree and branch cleanup |
| `/` | Filter by name, path or branch |
| `:` or `Ctrl+K` | Command palette |
| `o` | Open a shell in the repository |
| `r` | Refresh |
| `?` | Help |
| `q` | Quit |

Try it without touching your own repositories:

```sh
demo_root=$(bash scripts/demo.sh)
bin/gitperch "$demo_root/workspace"
```

## Configuration

gitperch reads `$XDG_CONFIG_HOME/gitperch/config.toml` (or
`~/.config/gitperch/config.toml`). Without a config file it scans the current
directory.

```toml
default_workspace = "agents"

[[workspace]]
name = "agents"
paths = ["~/projects", "~/agent-worktrees"]
max_depth = 4
ignore_dirs = ["node_modules", "vendor", "target", ".cache", ".next", "dist", "build"]

[ui]
icons = "unicode"     # or "ascii", "nerd"
refresh_seconds = 30  # automatic local status refresh; 0 turns it off
```

```sh
gitperch --workspace agents
```

See the [usage guide](docs/usage.md) for every option, exit code and safety
rule.

## Documentation

- [Usage guide](docs/usage.md): commands, configuration, fetch/push/pull rules,
  authentication and limits
- [Interface guide](docs/ui.md): layouts, key bindings and render captures
- [Development guide](docs/development.md): building, testing and project layout
- [Releasing](docs/releasing.md): versioning, changelog and the release workflow
- [Changelog](CHANGELOG.md): changes in each version
- [Design plan](docs/plan.md) and [mission control notes](docs/mission-control.md)

## Roadmap

gitperch currently shows what Git knows. Agent-aware views are the next step,
and will only be added on top of real integrations, not guessed from
heuristics:

- Show which agent session is active in which repository or worktree.
- Surface "waiting for review" and "agent finished" states next to Git status.
- Run project checks (tests, linters) from the dashboard.
- Validated macOS and Windows support, with release binaries for both
  (Linux binaries are already published).

Ideas and use cases are welcome in
[GitHub Discussions](https://github.com/mark-lvl/gitperch/discussions)
or as a [feature request](https://github.com/mark-lvl/gitperch/issues/new/choose).

## Contributing

Contributions are welcome, including ones written with AI agents. Read
[CONTRIBUTING.md](CONTRIBUTING.md) to get started; agents should also follow
[AGENTS.md](AGENTS.md). Everyone taking part is expected to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

To report a vulnerability, follow the [security policy](SECURITY.md) instead
of opening a public issue.

## License

gitperch is released under the [MIT License](LICENSE).
