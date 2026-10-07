#!/bin/sh
# Installs a gitperch release binary on Linux, WSL or macOS.
#
#   curl -fsSL https://github.com/mark-lvl/gitperch/releases/latest/download/install.sh | sh
#
# Environment:
#   GITPERCH_VERSION       release to install, such as 0.4.0 (default: latest)
#   INSTALL_DIR            target directory (default: ~/.local/bin)
#   GITPERCH_DOWNLOAD_URL  releases base URL (default: the GitHub releases page)
#
# The archive is verified against the release's checksums.txt and the binary is
# run once before it replaces an existing installation. Shell startup files are
# never edited.
set -eu

die() {
    printf 'gitperch install: %s\n' "$*" >&2
    exit 1
}

# fetch URL FILE
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$2" "$1"
    else
        die "curl or wget is required"
    fi
}

# sha256 FILE prints the file's SHA-256 digest.
sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sum=$(sha256sum -- "$1")
    elif command -v shasum >/dev/null 2>&1; then
        sum=$(shasum -a 256 -- "$1")
    else
        die "sha256sum or shasum is required to verify the download"
    fi
    printf '%s\n' "${sum%% *}"
}

# Sets os and arch to the release archive's platform names.
detect_platform() {
    kernel=$(uname -s)
    case $kernel in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        MINGW* | MSYS* | CYGWIN*) die "Windows is not supported; run this installer inside WSL" ;;
        *) die "unsupported operating system: $kernel" ;;
    esac
    machine=$(uname -m)
    case $machine in
        x86_64 | amd64) arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
        *) die "no prebuilt binary for $machine; install with: go install github.com/mark-lvl/gitperch/cmd/gitperch@latest" ;;
    esac
    # A shell running under Rosetta on Apple Silicon reports x86_64.
    if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
        [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
        arch=arm64
    fi
}

main() {
    detect_platform
    base=${GITPERCH_DOWNLOAD_URL:-https://github.com/mark-lvl/gitperch/releases}
    base=${base%/}
    version=${GITPERCH_VERSION:-}
    version=${version#v}
    case $version in
        *[!0-9A-Za-z.-]*) die "GITPERCH_VERSION $GITPERCH_VERSION is not a version like 0.4.0" ;;
    esac
    install_dir=${INSTALL_DIR:-$HOME/.local/bin}
    target=$install_dir/gitperch
    # Never replace a symlink, such as the development launcher from
    # scripts/install-dev.sh, or anything that is not a plain file.
    if [ -L "$target" ] || { [ -e "$target" ] && [ ! -f "$target" ]; }; then
        die "$target is a symlink or not a regular file; remove it or set INSTALL_DIR"
    fi

    tmp=$(mktemp -d "${TMPDIR:-/tmp}/gitperch-install.XXXXXX")
    staged=
    trap 'rm -rf -- "$tmp" ${staged:+"$staged"}' EXIT
    trap 'exit 1' HUP INT TERM

    if [ -n "$version" ]; then
        release=v$version
        sums_url=$base/download/v$version/checksums.txt
    else
        release=latest
        sums_url=$base/latest/download/checksums.txt
    fi
    fetch "$sums_url" "$tmp/checksums.txt" || die "could not download $sums_url"

    # checksums.txt names each archive gitperch_<version>_<os>_<arch>.tar.gz,
    # which also tells us the latest version without the GitHub API.
    suffix=_${os}_${arch}.tar.gz
    line=$(grep -E "^[0-9a-f]{64}  gitperch_[0-9][0-9A-Za-z.-]*_${os}_${arch}\.tar\.gz\$" "$tmp/checksums.txt" | head -n 1 || true)
    [ -n "$line" ] || die "release $release has no $os/$arch archive"
    expected=${line%% *}
    archive=${line##* }
    version=${archive#gitperch_}
    version=${version%"$suffix"}

    url=$base/download/v$version/$archive
    printf 'Downloading gitperch %s for %s/%s\n' "$version" "$os" "$arch"
    fetch "$url" "$tmp/$archive" || die "could not download $url"
    [ "$(sha256 "$tmp/$archive")" = "$expected" ] || die "checksum mismatch for $archive; nothing was installed"

    tar -xzf "$tmp/$archive" -C "$tmp"
    binary=$tmp/gitperch_${version}_${os}_${arch}/gitperch
    [ -f "$binary" ] || die "$archive does not contain gitperch"
    reported=$("$binary" --version 2>&1) || die "the downloaded binary does not run on this system: $reported"

    mkdir -p -- "$install_dir"
    staged=$install_dir/.gitperch.install.$$
    cp -- "$binary" "$staged"
    chmod 0755 "$staged"
    mv -f -- "$staged" "$target"
    staged=
    printf 'Installed %s to %s\n' "$reported" "$target"

    command -v git >/dev/null 2>&1 ||
        printf 'gitperch needs git (2.36 or newer for worktree features); install it before use.\n'
    case ":$PATH:" in
        *":$install_dir:"*)
            found=$(command -v gitperch 2>/dev/null || true)
            if [ -n "$found" ] && [ "$found" != "$target" ]; then
                printf 'Warning: %s comes before %s in PATH.\n' "$found" "$target"
            fi
            ;;
        *)
            case ${SHELL:-} in
                */zsh) rc=.zshrc ;;
                */bash) rc=.bashrc ;;
                *) rc=.profile ;;
            esac
            printf 'Add %s to PATH to run gitperch from any directory, for example:\n' "$install_dir"
            printf "  echo 'export PATH=\"%s:\$PATH\"' >> ~/%s\n" "$install_dir" "$rc"
            ;;
    esac
}

# Called last so that a partially downloaded script does nothing.
main "$@"
