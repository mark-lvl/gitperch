#!/usr/bin/env bash
# Prepares a release locally: moves the Unreleased changelog entries into a
# dated version section, runs the checks, then commits and tags. Nothing is
# pushed; pushing the tag starts the GitHub release workflow.
# Usage: scripts/release.sh X.Y.Z
set -euo pipefail

die() {
    printf 'release: %s\n' "$*" >&2
    exit 1
}

version=${1:?usage: scripts/release.sh X.Y.Z}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] ||
    die "$version is not a semantic version like 1.2.3"
tag=v$version
repo_url=https://github.com/mark-lvl/gitperch
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

branch=$(git symbolic-ref --quiet --short HEAD) || die "HEAD is detached; switch to main"
[[ $branch == main ]] || die "releases are cut from main, not $branch"
[[ -z $(git status --porcelain) ]] || die "the working tree has uncommitted changes"
! git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null || die "tag $tag already exists"
! grep -q "^## \[$version\]" CHANGELOG.md || die "CHANGELOG.md already has a [$version] section"
scripts/release-notes.sh Unreleased >/dev/null || die "add the release's changes under [Unreleased] first"

previous=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
if [[ -n $previous ]]; then
    version_url=$repo_url/compare/$previous...$tag
else
    version_url=$repo_url/releases/tag/$tag
fi
sed -i \
    -e "s|^## \[Unreleased\]\$|## [Unreleased]\n\n## [$version] - $(date -u +%F)|" \
    -e "s|^\[Unreleased\]: .*|[Unreleased]: $repo_url/compare/$tag...HEAD\n[$version]: $version_url|" \
    CHANGELOG.md
grep -q "^## \[$version\] - " CHANGELOG.md && grep -q "^\[$version\]: " CHANGELOG.md ||
    die "could not update CHANGELOG.md; check its [Unreleased] heading and link"

make check
git commit --quiet -m "chore(release): $tag" -- CHANGELOG.md
git tag -a "$tag" -m "gitperch $tag"

printf '\nCreated commit and tag %s. Release notes:\n\n' "$tag"
scripts/release-notes.sh "$version"
printf '\nPublish it with:\n  git push origin main %s\n' "$tag"
printf 'Undo it before pushing with:\n  git tag -d %s && git reset --keep HEAD~1\n' "$tag"
