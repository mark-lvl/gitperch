# Contributing to gitperch

Thanks for your interest in gitperch! Bug reports, ideas, documentation fixes
and code are all welcome.

## Ground rules

- Be kind. This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
- Report security problems privately as described in [SECURITY.md](SECURITY.md).
- For anything larger than a small fix, open an issue first so we can agree on
  the approach before you spend time on it.

## Reporting bugs and requesting features

Use the [issue templates](https://github.com/mark-lvl/gitperch/issues/new/choose).
For bugs include your OS (and whether it is WSL2), terminal, `git --version`,
`gitperch --version`, what you did, what you expected and what happened.
`gitperch status --json` output for the affected repository is very helpful;
redact paths or remote URLs you do not want to share.

## Development setup

You need Go 1.27.1+ and Git. Then:

```sh
git clone https://github.com/mark-lvl/gitperch.git
cd gitperch
make check      # fmt-check, test, race, vet, build: the same as CI
```

The [development guide](docs/development.md) covers the project layout, the
demo workspace, render captures and the auto-rebuilding launcher.

## Making changes

1. Fork the repository and create a branch from `main`.
2. Keep changes focused; one logical change per pull request.
3. Add or update tests. Parsing, discovery, action eligibility and real Git
   behavior all need coverage. Tests that mutate Git must use temporary
   repositories and local bare remotes, never real remotes.
4. Update documentation (`README.md`, `docs/`) and add an entry under
   `Unreleased` in [CHANGELOG.md](CHANGELOG.md) for user-visible changes.
5. If you change the TUI, update the render captures (see
   [docs/ui.md](docs/ui.md#render-captures-and-validation)).
6. Run `make check` and make sure it passes.
7. Open a pull request and fill in the template.

## Design principles

- **Safety first.** gitperch must never surprise the user with a Git mutation.
  Every action is planned, shown, confirmed where needed and revalidated right
  before it runs. No force pushes, resets, stashes or commits.
- **Honest UI.** Do not display data gitperch cannot actually observe. Agent,
  task and test views need real integrations, not placeholders.
- **Domain logic independent of the TUI.** Keep Git and planning code in
  `internal/` packages that the TUI only calls.
- **Git and gh via argument arrays.** Never build shell command strings. Tests use fake gh executables and never run the real gh.
- **Small and dependable.** Avoid speculative frameworks and new dependencies;
  pin any dependency you add in `go.mod`/`go.sum`.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/), for example
`feat(tui): ...`, `fix(git): ...`, `docs: ...`, `test: ...`, `chore: ...`.
Write the subject in the imperative mood and keep it under about 72 characters.

## Contributions made with AI agents

AI-assisted contributions are welcome; this is a tool for agentic development
after all. You remain responsible for every line you submit: review it,
run `make check`, and make sure tests exercise the behavior. Agents working in
this repository should follow [AGENTS.md](AGENTS.md).

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
