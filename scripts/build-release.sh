#!/usr/bin/env bash
# Builds versioned release archives, install.sh and checksums.txt under dist/.
# Uses GNU tar options for reproducible archives, so run it on Linux.
# Usage: scripts/build-release.sh X.Y.Z
set -euo pipefail

version=${1:?usage: scripts/build-release.sh X.Y.Z}
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
    printf 'build-release: %q is not a semantic version like 1.2.3\n' "$version" >&2
    exit 2
fi
go_command=${GO:-go}
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

# Validated platforms; see README.md#installation.
targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64)
# Archive timestamps come from the release commit so rebuilds are identical.
mtime=$(git log -1 --format=%ct)

rm -rf dist
mkdir -p dist
for target in "${targets[@]}"; do
    name=gitperch_${version}_${target%/*}_${target#*/}
    stage=dist/$name
    mkdir -p "$stage"
    CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} "$go_command" build \
        -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$version" \
        -o "$stage/gitperch" ./cmd/gitperch
    cp LICENSE README.md CHANGELOG.md "$stage/"
    tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$mtime" \
        -C dist -cf - "$name" | gzip -n >"dist/$name.tar.gz"
    rm -rf "$stage"
done
cp scripts/install.sh dist/install.sh
(cd dist && sha256sum -- *.tar.gz install.sh >checksums.txt && cat checksums.txt)
