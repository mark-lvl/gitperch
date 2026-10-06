# WSL2 dashboard smoke check

Run in a WSL2 terminal after `make build`. Use the local demo for every
mutation check; it creates disposable worktrees and a bare remote under `/tmp`
and performs no network access.

1. Create and open the demo:

   ```sh
   demo_root=$(bash scripts/demo.sh)
   printf '%s\n' "$demo_root"
   bin/gitperch --no-color "$demo_root/workspace"
   ```

   Confirm workspace counts, paths, branches, independent worktree and sync
   columns, and local-ref comparison wording. An absent upstream must not show
   synchronized.
2. Move with `j`/`k` and arrows. Select with Space, then toggle all visible rows
   with `a`. Confirm the selected count and contextual action shortcuts. Page
   through the list with PgUp/PgDn and Home/End. Use Tab and Shift+Tab to cycle
   quick views; confirm changing views clears selection. Press `s` and verify
   attention ordering while the highlight retains its repository identity.
3. Enter `/`, type a name/path/branch filter and confirm selection clears. Try
   no matches, Backspace, Enter and Esc. Hidden rows must not stay selected.
4. Press `r` repeatedly. Confirm the UI responds while refreshing, results retain
   path identity and the latest refresh wins. A spinner turns beside the
   refresh label while it runs and stops once results arrive; during fetch,
   push and pull it also turns in the footer, the wide header and each running
   row. With `[ui] icons = "ascii"` it shows `|/-\` instead.
5. Show help with `?` and scroll to the end; return with Esc. Use `d` and
   arrows or PgUp/PgDn to inspect full diagnostics and root warnings,
   including long errors. Inspect the highlighted repository's error details when
   using an invalid `.git` candidate. Try an empty directory and a missing root.
6. Press Enter and confirm repository details open; Esc returns. Press `o` to
   start a child shell. Check `pwd`, type `exit`, and confirm the
   dashboard restores and refreshes. The parent shell stays in its original
   directory. The child shell runs as your user.
7. Press `g` with LazyGit absent: confirm an actionable message. With LazyGit
   installed, open and quit it, then confirm restore/refresh. A temporary test
   executable can verify terminal lifecycle, but does not verify LazyGit itself.
8. Resize above and below 120 columns; confirm the selected preview places
   changed files beside or above recent commits. Resize to a narrow and short terminal; test long names and
   branches. Check guidance for changed, behind, detached, and unknown rows. Confirm
   no panics or terminal control characters from repository data.
9. Quit with `q`, relaunch and quit with Ctrl+C. Confirm the terminal remains
   usable. Repeat with `NO_COLOR=1`.
10. Test fast-forward pull on `behind`: select only that row with Space and
    press `l`. Gitperch fetches without asking, then floats a small popup over
    the workspace naming the repository, `origin/main → main` and the short
    commit. Press Esc to cancel, then repeat and press Enter. Confirm `peer.txt`
    appears in the worktree and the refreshed status is current. Press `l` on a
    clean, up-to-date row: no popup opens and the status line says why.
11. Open the dashboard again and test push on `ahead`: select only that row,
    press `p`, check the popup's `main → origin/main` route and commit, then
    press Enter. Confirm the remote's `main` points to the local commit:

    ```sh
    git --git-dir="$demo_root/remotes/origin.git" rev-parse refs/heads/main
    git -C "$demo_root/workspace/ahead" rev-parse HEAD
    ```

    The two OIDs should match. Refresh the dashboard and confirm ahead is zero.
12. To check dirty-but-pushable behavior after step 11, create another commit
    in the now-synchronized `ahead` row and leave a separate worktree edit:

    ```sh
    printf 'another local commit\n' > "$demo_root/workspace/ahead/another.txt"
    git -C "$demo_root/workspace/ahead" add another.txt
    git -C "$demo_root/workspace/ahead" -c commit.gpgsign=false commit -m 'another local advance'
    printf 'still uncommitted\n' >> "$demo_root/workspace/ahead/tracked.txt"
    ```

    Push `ahead`; the final preview should say uncommitted changes are excluded,
    while showing the outgoing commit. Test `dirty` for pull and confirm it is
    skipped because the worktree is not clean. Select `diverged` for either
    operation and confirm the final preview explains why it cannot safely
    proceed. Also check `no-upstream`, `detached`, `unborn`, and `broken-remote`;
    each should remain visibly skipped with a reason. Do not confirm an
    operation unless its exact target is the one intended.
13. Press Esc from both the fetch-scope preview and final preview in separate
    runs; verify neither next stage occurs. Verify Enter on the first screen
    only fetches the reviewed scope. Try selecting multiple rows and confirm
    the target count and independent results after mixed eligible/skipped or
    failed targets.
14. Redirect default-command output to a file and confirm it explains `status`.
    Run `bin/gitperch status --json .` redirected and confirm valid JSON instead.

Record exact commands, kernel/terminal context, results and untested behavior
in the implementation log when asked to update it; a pseudo-terminal run is
distinct from a Windows Terminal check. Do not describe an unperformed manual
check as passed.
