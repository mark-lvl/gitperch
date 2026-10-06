#!/usr/bin/env bash
# Creates disposable Git scenarios with a local bare remote; never uses network.
set -euo pipefail
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_CONFIG GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
demo_root=$(mktemp -d "${TMPDIR:-/tmp}/gitperch-demo.XXXXXX")
# Remove a half-built demo on failure; success keeps it for inspection.
trap 'rm -rf -- "$demo_root"' EXIT
mkdir -p "$demo_root/remotes" "$demo_root/workspace"
git init -q --bare -b main "$demo_root/remotes/origin.git"
git init -q -b main "$demo_root/seed"
git -C "$demo_root/seed" config user.name 'Gitperch Demo'
git -C "$demo_root/seed" config user.email 'demo@example.invalid'
printf 'base\n' > "$demo_root/seed/tracked.txt"
git -C "$demo_root/seed" add tracked.txt
git -C "$demo_root/seed" -c commit.gpgsign=false commit -qm base
git -C "$demo_root/seed" remote add origin "$demo_root/remotes/origin.git"
git -C "$demo_root/seed" push -q -u origin main
for demo_name in clean behind ahead dirty diverged no-upstream detached; do
  git clone -q "$demo_root/remotes/origin.git" "$demo_root/workspace/$demo_name"
  git -C "$demo_root/workspace/$demo_name" config user.name 'Gitperch Demo'
  git -C "$demo_root/workspace/$demo_name" config user.email 'demo@example.invalid'
done
printf 'peer update\n' > "$demo_root/seed/peer.txt"
git -C "$demo_root/seed" add peer.txt
git -C "$demo_root/seed" -c commit.gpgsign=false commit -qm 'peer advance'
git -C "$demo_root/seed" push -q origin main
for demo_name in clean ahead; do
  git -C "$demo_root/workspace/$demo_name" fetch -q origin
  git -C "$demo_root/workspace/$demo_name" merge -q --ff-only origin/main
done
for demo_name in ahead diverged; do
  printf 'local commit\n' > "$demo_root/workspace/$demo_name/local.txt"
  git -C "$demo_root/workspace/$demo_name" add local.txt
  git -C "$demo_root/workspace/$demo_name" -c commit.gpgsign=false commit -qm 'local advance'
done
printf 'uncommitted change\n' > "$demo_root/workspace/dirty/tracked.txt"
git -C "$demo_root/workspace/no-upstream" branch --unset-upstream
git -C "$demo_root/workspace/detached" checkout -q --detach
git init -q -b main "$demo_root/workspace/unborn"
git init -q -b main "$demo_root/workspace/broken-remote"
git -C "$demo_root/workspace/broken-remote" remote add origin "$demo_root/remotes/missing.git"
git -C "$demo_root/workspace/clean" worktree add -q -b linked "$demo_root/workspace/linked"
trap - EXIT
printf '%s\n' "$demo_root"
