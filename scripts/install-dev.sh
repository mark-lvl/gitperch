#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
install_dir=${INSTALL_DIR:-"$HOME/.local/bin"}
launcher="$script_dir/gitperch-dev"
command_path="$install_dir/gitperch"

# Do not replace another installation inadvertently.
if [[ -e "$command_path" || -L "$command_path" ]]; then
    if [[ ! -L "$command_path" || $(readlink -f -- "$command_path") != "$launcher" ]]; then
        printf 'gitperch: %s already exists and belongs to another installation.\n' "$command_path" >&2
        if [[ -L "$command_path" && $(readlink -- "$command_path") == */scripts/gitperch-dev ]]; then
            # Typically a launcher from a checkout that has since moved.
            printf 'It points to %s; remove it with: rm %q\n' "$(readlink -- "$command_path")" "$command_path" >&2
        fi
        exit 1
    fi
fi

# Build and validate before installing the command.
"$launcher" --version
mkdir -p -- "$install_dir"
ln -sfn -- "$launcher" "$command_path"
printf 'Installed %s → %s\n' "$command_path" "$launcher"
case ":$PATH:" in
    *":$install_dir:"*) ;;
    *) printf 'Add %s to PATH to use gitperch from any directory.\n' "$install_dir" ;;
esac
