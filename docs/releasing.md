# Releasing

gitperch is released from version tags on `main`. Pushing a `vX.Y.Z` tag
runs the [release workflow](../.github/workflows/release.yml), which publishes
a GitHub release with Linux archives and checksums.

## Versions

Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Tags carry a `v` prefix (`v0.2.0`); `gitperch --version` and archive names
do not (`0.2.0`). While the version is `0.x`:

- bump the **minor** version for new features or any breaking change to the
  CLI, configuration, JSON schema or key bindings;
- bump the **patch** version for fixes only.

A tag with a pre-release suffix such as `v0.2.0-rc.1` is published as a
GitHub pre-release.

The version is not stored in a source file. Release builds stamp it with
`-ldflags "-X main.version=X.Y.Z"`, `make build` stamps the output of
`git describe` (for example `0.2.0-3-g1a2b3c4-dirty`), and
`go install github.com/mark-lvl/gitperch/cmd/gitperch@vX.Y.Z` reports the
module version Go records.

## Changelog

[CHANGELOG.md](../CHANGELOG.md) follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Every user-visible
change adds an entry under `## [Unreleased]` in the same pull request, grouped
under `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed` or `Security`.
Write entries for users, not as a commit log. The release script moves these
entries into the version's section, and that section becomes the GitHub
release notes.

## Cutting a release

1. Make sure `main` is up to date and CI is green, and read the `Unreleased`
   section: are the entries complete and is the version bump right?
2. On a clean `main`, run:

   ```sh
   scripts/release.sh 0.2.0
   ```

   It moves the `Unreleased` entries under `## [0.2.0] - <today>`, updates the
   comparison links, runs `make check`, then commits
   `chore(release): v0.2.0` and creates the annotated tag `v0.2.0`. It does
   not push anything, and it prints the commands to undo the commit and tag.
3. Publish:

   ```sh
   git push origin main v0.2.0
   ```

The release workflow then:

- checks that the tag is on `main` and that CHANGELOG.md has a section for it;
- runs `make check`;
- builds static `linux/amd64` and `linux/arm64` binaries with
  `scripts/build-release.sh`, packed with `LICENSE`, `README.md` and
  `CHANGELOG.md` into `gitperch_X.Y.Z_linux_<arch>.tar.gz`, plus
  `checksums.txt` (SHA-256);
- creates the GitHub release `vX.Y.Z` with the changelog section as its notes.

Archives are reproducible: rebuilding a tag with the same Go version gives the
same checksums. Build them locally with `make dist` (version from the current
tag) or `make dist VERSION=0.2.0`.

macOS and Windows archives are left out until those platforms are validated.

## When a release fails

If the workflow fails before publishing, fix the problem on `main` (the
`0.2.0` changelog section stays as it is), then move the tag to the fixed
commit and push it again:

```sh
git push origin :refs/tags/v0.2.0
git tag -f -a v0.2.0 -m "gitperch v0.2.0"
git push origin v0.2.0
```

Once a release is published, do not move or reuse its tag: the Go module proxy
caches a tagged version permanently once anyone fetches it. Publish a new
patch version instead.
