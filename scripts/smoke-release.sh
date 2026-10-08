#!/usr/bin/env bash
# Installs the release in DIST with its own install.sh, the way users do, and
# checks the installed binary against the scripts/demo.sh scenarios.
# Needs curl (the installer reads file:// URLs), jq and file; never uses the
# network or the caller's Git and gitperch configuration.
# Usage: scripts/smoke-release.sh DIST X.Y.Z
set -euo pipefail

usage='usage: scripts/smoke-release.sh DIST X.Y.Z'
dist=${1:?$usage}
version=${2:?$usage}
dist=$(cd -- "$dist" && pwd -P)
scripts=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)

fail() {
    printf 'smoke-release: %s\n' "$*" >&2
    exit 1
}
step() { printf '==> %s\n' "$*"; }

work=$(mktemp -d "${TMPDIR:-/tmp}/gitperch-smoke.XXXXXX")
demo_root=
trap 'rm -rf -- "$work" ${demo_root:+"$demo_root"}' EXIT
mkdir -p "$work/home"
export HOME=$work/home GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
unset XDG_CONFIG_HOME XDG_STATE_HOME GITPERCH_VERSION GITPERCH_DOWNLOAD_URL INSTALL_DIR

step "Checksums"
if command -v sha256sum >/dev/null 2>&1; then
    (cd -- "$dist" && sha256sum -c checksums.txt)
else
    (cd -- "$dist" && shasum -a 256 -c checksums.txt)
fi

step "Install the latest release with install.sh"
# Same layout as https://github.com/mark-lvl/gitperch/releases.
releases=$work/releases
mkdir -p "$releases/download/v$version" "$releases/latest/download"
cp -- "$dist"/*.tar.gz "$dist/checksums.txt" "$dist/install.sh" "$releases/download/v$version/"
cp -- "$dist/checksums.txt" "$dist/install.sh" "$releases/latest/download/"
# Piped like the README's curl ... | sh.
GITPERCH_DOWNLOAD_URL=file://$releases INSTALL_DIR=$work/bin \
    sh <"$releases/latest/download/install.sh"
gitperch=$work/bin/gitperch
[ -x "$gitperch" ] || fail "install.sh did not install $gitperch"

step "Native binary for $(uname -s)/$(uname -m)"
case $(uname -m) in
    x86_64 | amd64) want='x86[-_]64' ;;
    arm64 | aarch64) want='arm64|aarch64' ;;
    *) fail "no release archive for $(uname -m)" ;;
esac
file -b "$gitperch"
file -b "$gitperch" | grep -Eq "$want" || fail "installed binary does not match $(uname -m)"

step "gitperch --version"
reported=$("$gitperch" --version)
printf '%s\n' "$reported"
[ "$reported" = "gitperch $version" ] || fail "expected 'gitperch $version'"

step "gitperch --help"
"$gitperch" --help >"$work/help.txt" 2>&1 || fail "--help exited $?: $(cat "$work/help.txt")"
grep -q 'gitperch status' "$work/help.txt" || fail "--help does not mention the status command"

step "Demo workspace"
demo_root=$(bash "$scripts/demo.sh")
workspace=$demo_root/workspace
# status reads local tracking refs only; fetch from the local bare remote so
# the behind and diverged clones see the peer commit.
for repo in behind diverged; do
    git -C "$workspace/$repo" fetch -q origin
done

step "gitperch status"
"$gitperch" status "$workspace" | tee "$work/status.txt"
for repo in ahead behind broken-remote clean detached dirty diverged linked no-upstream unborn; do
    grep -q "^$repo / " "$work/status.txt" || fail "status does not list $repo"
done

step "gitperch status --json"
"$gitperch" status --json "$workspace" >"$work/status.json"
jq -e '.schema_version == 1 and .warnings == []' "$work/status.json" >/dev/null ||
    fail "unexpected schema_version or warnings: $(jq -c '{schema_version, warnings}' "$work/status.json")"
jq -r '.repositories | sort_by(.name)[] | [
    .name,
    "branch=\(.status.branch)",
    "upstream=\(.status.upstream)",
    "ahead=\(.status.ahead)",
    "behind=\(.status.behind)",
    "changes=\(.status.changes)",
    "detached=\(.status.detached)",
    "unborn=\(.status.unborn)",
    "main=\(.worktree.main_path | split("/") | last)",
    "linked=\(.worktree.linked)",
    "attention=\(.attention.level)"
] | join(" ")' "$work/status.json" >"$work/actual.txt"
cat >"$work/expected.txt" <<'EOF'
ahead branch=main upstream=origin/main ahead=1 behind=0 changes=0 detached=false unborn=false main=ahead linked=false attention=high
behind branch=main upstream=origin/main ahead=0 behind=1 changes=0 detached=false unborn=false main=behind linked=false attention=medium
broken-remote branch=main upstream= ahead=0 behind=0 changes=0 detached=false unborn=true main=broken-remote linked=false attention=medium
clean branch=main upstream=origin/main ahead=0 behind=0 changes=0 detached=false unborn=false main=clean linked=false attention=low
detached branch= upstream= ahead=0 behind=0 changes=0 detached=true unborn=false main=detached linked=false attention=low
dirty branch=main upstream=origin/main ahead=0 behind=0 changes=1 detached=false unborn=false main=dirty linked=false attention=high
diverged branch=main upstream=origin/main ahead=1 behind=1 changes=0 detached=false unborn=false main=diverged linked=false attention=high
linked branch=linked upstream= ahead=0 behind=0 changes=0 detached=false unborn=false main=clean linked=true attention=medium
no-upstream branch=main upstream= ahead=0 behind=0 changes=0 detached=false unborn=false main=no-upstream linked=false attention=medium
unborn branch=main upstream= ahead=0 behind=0 changes=0 detached=false unborn=true main=unborn linked=false attention=medium
EOF
diff -u "$work/expected.txt" "$work/actual.txt" || fail "status --json does not match the demo scenarios"

step "Release $version passed on $(uname -s)/$(uname -m)"
