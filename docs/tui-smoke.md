# WSL2 read-only dashboard smoke check

Run in a WSL2 terminal after `make build`. Use a disposable repository or the
project itself for read-only checks. Mutations are not part of this M4 checklist.

1. Run `bin/repodash --no-color .`. Confirm path, branch, independent status
   markers and local-ref comparison wording. An absent upstream must not show
   synchronized.
2. Move with `j`/`k` and arrows. Select with Space, then toggle all visible rows
   with `a`. Confirm the selected count.
3. Enter `/`, type a name/path/branch filter and confirm selection clears. Try
   no matches, Backspace, Enter and Esc. Hidden rows must not stay selected.
4. Press `r` repeatedly. Confirm the UI responds while refreshing, results retain
   path identity and the latest refresh wins.
5. Show help with `?`; use `d` and j/k to inspect full diagnostics and root warnings,
   including long errors. Inspect the highlighted repository's error details when
   using an invalid `.git` candidate. Try an empty directory and a missing root.
6. Press Enter to start a child shell. Check `pwd`, type `exit`, and confirm the
   dashboard restores and refreshes. The parent shell stays in its original
   directory. The child shell runs as your user.
7. Press `g` with LazyGit absent: confirm an actionable message. With LazyGit
   installed, open and quit it, then confirm restore/refresh. A temporary test
   executable can verify terminal lifecycle, but does not verify LazyGit itself.
8. Resize to a narrow and short terminal; test long names and branches. Confirm
   no panics or terminal control characters from repository data.
9. Quit with `q`, relaunch and quit with Ctrl+C. Confirm the terminal remains
   usable. Repeat with `NO_COLOR=1`.
10. Redirect default-command output to a file and confirm it explains `status`.
    Run `bin/repodash status --json .` redirected and confirm valid JSON instead.

Record exact commands, kernel/terminal context, results and untested behavior
in `plan.md`; a pseudo-terminal run is distinct from a Windows Terminal check.
