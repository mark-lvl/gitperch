#!/usr/bin/env bash
# Prints one CHANGELOG.md section, without its heading, for release notes.
# Usage: scripts/release-notes.sh X.Y.Z|Unreleased
set -euo pipefail

section=${1:?usage: scripts/release-notes.sh X.Y.Z|Unreleased}
changelog=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)/CHANGELOG.md

# Collect lines after "## [section]" up to the next heading or link
# definitions, then trim surrounding blank lines.
notes=$(awk -v heading="## [$section]" '
    index($0, heading) == 1 { found = 1; next }
    found && (/^## \[/ || /^\[[^]]+\]: /) { exit }
    found { print }
' "$changelog" | sed -e '/./,$!d')

if [[ -z ${notes//[[:space:]]/} ]]; then
    printf 'release-notes: CHANGELOG.md has no entries under [%s]\n' "$section" >&2
    exit 1
fi
printf '%s\n' "$notes"
