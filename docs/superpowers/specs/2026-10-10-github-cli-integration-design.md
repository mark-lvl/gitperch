# GitHub CLI integration: pull requests, merge evidence and browser links

Status: approved design; implementation plan in
`docs/superpowers/plans/2026-10-10-github-cli-integration.md`.
Date: 2026-10-10.

## Goal

Agents usually land work through pull requests, often squash- or
rebase-merged, and leave the branch and its worktree behind. gitperch sees only
Git, so such work reads as `not merged`, `upstream gone — squash merge?` or
`! unknown refs`, and the interface guide lists pull requests as deliberately
unavailable for lack of a real integration. When the GitHub CLI (`gh`) is
installed and logged in, gitperch should use it to show each branch's pull
request with its review and CI state, and to accept a pull request merged with
exactly the local commit as proof of merge in the worktree lifecycle and in
Clean up.

Success: on a workspace of agent worktrees, the dashboard shows which branches
have open pull requests with failing checks or requested changes and which were
merged, and one reviewed Clean up removes squash-merged worktrees and branches.
gitperch never handles credentials, never guesses, and behaves exactly as today
for users without gh.

## Decisions

| Topic | Decision |
| --- | --- |
| Mechanism | gitperch resolves each branch's GitHub repository and remote branch from Git; gh answers one fixed GraphQL query per repository (`gh api graphql`). gh never runs inside a scanned repository. |
| Activation | Automatic when `gh` is on `PATH`, Git is 2.36 or newer and `[github] enabled` is not `false`. "Not logged in" is reported per row, never fatal. |
| `status` | Offline and byte-for-byte unchanged by default. `--github` adds lookups and a `github` object per row; JSON schema version stays 1. |
| Cadence (TUI) | Background lookups after each snapshot. A result is reused for 5 minutes; a manual refresh, a returning shell or LazyGit, and a finished batch force a recheck, at most every 30 seconds per row. At most two gh processes run at once. Automatic refresh never forces. |
| Attention | New medium reasons `pr_checks_failing`, `pr_changes_requested` and `pr_merged`. An open pull request waiting for reviewers stays low. |
| Merge evidence | A pull request merged into its repository's default branch, whose head is the branch's upstream and whose head commit is exactly the local commit. |
| Clean up | Evidence from a fresh lookup (never the cache) makes squash- and rebase-merged worktrees and branches eligible; every other check still applies. |
| Hosts | `github.com` (`www.github.com` and `ssh.github.com` map to it) plus `[github] hosts`. SSH host aliases are not resolved. |
| GitHub mutations | None. Browser opening runs `gh browse`. |

## Delivery phases

1. Foundation and read-only pull request status: TUI, `status --github`,
   configuration, `pr_checks_failing` and `pr_changes_requested`.
2. Merge evidence: `pr_merged`, the lifecycle signal `merged_pull_request` and
   the `✓ merged #42` status.
3. Clean up: squash- and rebase-merged items become eligible.
4. Open in the browser (`b`).

Each phase ends with `make check`, updated render captures, docs and a
CHANGELOG entry. Phases 2 to 4 depend on phase 1; phase 3 depends on phase 2.

## Phase 1: foundation and read-only status

### gh layer (`internal/github`, new)

The only package that runs gh.

- `Runner{Executable, Timeout, OutputLimit}` with
  `Run(ctx, args...) (Output, error)`, hardened like the Git runner:
  - argument arrays through `exec.CommandContext`, the status deadline
    (default 15 seconds), closed stdin, a 4 MiB capture per stream
    (`ErrOutputLimit`) and `WaitDelay`;
  - working directory `os.TempDir()`, so gh never reads a scanned
    repository's Git configuration;
  - environment: `git.ChildEnvironment`, minus `GH_FORCE_TTY`,
    `CLICOLOR_FORCE`, `GH_DEBUG`, `DEBUG`, `GH_REPO`, `GH_HOST`, `GH_PAGER`
    and `PAGER`, plus `GH_PROMPT_DISABLED=1`, `GH_NO_UPDATE_NOTIFIER=1`,
    `GH_NO_EXTENSION_UPDATE_NOTIFIER=1`, `GH_SPINNER_DISABLED=1`,
    `NO_COLOR=1` and `CLICOLOR=0`. Tokens (`GH_TOKEN`, `GITHUB_TOKEN`,
    `GH_ENTERPRISE_TOKEN`), `GH_CONFIG_DIR`, proxies and `BROWSER` pass
    through unchanged;
  - exit status 4 (gh's "authentication required") becomes
    `ErrNotAuthenticated`; other failures carry the first line of stderr
    through `SafeText`.
- `Available() bool`: `exec.LookPath` of `Executable`, default `gh`.
- `ParseRemote(url string, hosts []string) (Repo, bool)` returns
  `Repo{Host, Owner, Name}` for `https://`, `http://`, `ssh://`, `git://` and
  scp-like `user@host:owner/name` URLs. Userinfo and ports are dropped, `.git`
  and a trailing `/` trimmed, the host lower-cased, and `www.github.com` and
  `ssh.github.com` map to `github.com`. Only `github.com` and configured hosts
  are accepted, and the path must be exactly `owner/name` (letters, digits,
  `.`, `_`, `-`; not `.` or `..`). Anything else, including local paths, is not
  GitHub.
- `Client{Runner}.PullRequests(ctx, repo Repo, heads []string) (Lookup, error)`
  for 1 to 20 heads:
  - runs `gh api graphql --hostname=<host> -f query=<query> -f owner=<owner>
    -f name=<name> -f h0=<head> …`. The query text is built from the head
    count alone (aliases `h0…hN`, variables `$h0…$hN`), so names travel only
    in `-f` raw fields. `-F` is never used: it reads a file for `@value` and
    expands `{owner}`-style placeholders;
  - the query reads the repository's `nameWithOwner` and
    `defaultBranchRef`, and for each head
    `pullRequests(headRefName: $hI, first: 10, orderBy: UPDATED_AT DESC)` on
    the repository and on its `parent`, because a fork's pull requests live in
    the parent. Each pull request brings `number`, `title`, `url`, `state`,
    `isDraft`, `baseRefName`, `headRefName`, `headRefOid`,
    `headRepository.nameWithOwner`, `baseRepository.nameWithOwner`,
    `mergedAt`, `updatedAt`, `reviewDecision` and its last commit's
    `statusCheckRollup.state`;
  - a pull request is kept for head `h` only when its `headRefName` is `h` and
    its head repository is the queried repository (case-insensitive), so the
    same branch name in someone else's fork never matches. Results are deduped
    by base repository and number, sorted open first and then by `updatedAt`,
    newest first, and capped at 5;
  - `IntoDefault` is true when the base branch is the base repository's default
    branch;
  - stdout is parsed even after a failing exit, since gh prints GraphQL error
    bodies. No `repository` with error type `NOT_FOUND` gives
    `ErrRepositoryNotFound`, `RATE_LIMITED` gives `ErrRateLimited`, and
    output that does not parse returns the run error.

### Git layer

- `Branch` gains `Remote` and `RemoteRef`, read in `LocalBranches` from
  `%(upstream:remotename)` and `%(upstream:remoteref)`. Both come from the
  branch's configuration, so they survive a deleted remote branch.
- New read `Runner.RemoteURL(ctx, path, remote)`: `git remote get-url
  <remote>`, which applies `insteadOf` rewrites. Empty names and names starting
  with `-` are rejected. It joins the read-only guarantees and `Service`.

### App layer (`internal/app/github.go`, new)

The JSON field names below are the schema:

```go
type GitHubInfo struct {
	Host         string        `json:"host"`
	Repository   string        `json:"repository"`    // owner/name as GitHub reports it
	Branch       string        `json:"branch"`        // the upstream branch on GitHub
	PullRequests []PullRequest `json:"pull_requests"` // most relevant first; [] when none
	CheckedAt    time.Time     `json:"checked_at,omitzero"`
	Error        string        `json:"error,omitempty"` // latest lookup failed; PullRequests are from CheckedAt
}

type PullRequest struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	URL               string    `json:"url"`
	State             string    `json:"state"` // open, merged, closed
	Draft             bool      `json:"draft,omitempty"`
	BaseRepository    string    `json:"base_repository"`
	Base              string    `json:"base"`
	IntoDefaultBranch bool      `json:"into_default_branch"`
	HeadOID           string    `json:"head_oid"`
	Review            string    `json:"review,omitempty"` // approved, changes_requested, review_required
	Checks            string    `json:"checks,omitempty"` // passing, failing, pending
	MergedAt          time.Time `json:"merged_at,omitzero"`
	UpdatedAt         time.Time `json:"updated_at"`
}
```

Checks map from the rollup state: `SUCCESS` is passing, `FAILURE` and `ERROR`
are failing, `PENDING` and `EXPECTED` are pending, no rollup is empty.

`Row` gains `GitHub *GitHubInfo` (`json:"github,omitempty"`): nil when no
lookup applies or none has finished. `Row.CurrentPullRequest()` is the first
listed pull request, which is an open one when any is open.

The `GitHub` service is shared by the TUI, `status --github` and Clean up:

- `NewGitHub(git GitHubGit, client PullRequestClient, hosts []string)`.
  `GitHubGit` is `LocalBranches` plus `RemoteURL`; `PullRequestClient` is
  satisfied by `github.Client`.
- Its cache is keyed by row path and branch and holds the last `GitHubInfo`
  (nil for "no GitHub upstream") and when it was attempted.
- `Due(rows, force) [][]Row` picks lookup candidates (selectable, inspected,
  on a branch with an upstream), groups them by common Git directory, and
  returns the groups with an entry that is missing, older than 5 minutes or,
  when forced, older than 30 seconds. Returned groups are marked pending, so a
  group is never looked up twice at once. `Due` also drops cache entries for
  rows no longer present.
- `Lookup(ctx, group)` waits for one of two slots, reads `LocalBranches` once
  for the group and `RemoteURL` once per remote, and maps each row to a
  GitHub repository and remote branch with `ParseRemote`; a row without one
  gets a nil entry. It queries each GitHub repository in chunks of 20 heads
  and stores the results. A failure stores `Error` (safe text) and keeps the
  previous pull requests and `CheckedAt` for the same repository and branch.
- `LookupAll(ctx, rows)` runs a forced `Due` and every lookup, for
  `status --github`.
- `Annotate(rows)` sets each row's `GitHub` from the cache without I/O.

### Attention

| Reason | Level | When | Describe |
| --- | --- | --- | --- |
| `pr_checks_failing` | medium | The current pull request is open and its checks fail | `PR #42 checks failing` |
| `pr_changes_requested` | medium | The current pull request is open and its review decision is changes requested | `PR #42: changes requested` |

Both sort after `behind_upstream` and before `tracking_unknown`. Data from an
earlier lookup still counts after a failed recheck; the interface shows its
age.

### TUI

- `EnableGitHub(*app.GitHub)`; without it the dashboard behaves exactly as
  today.
- After each snapshot the model annotates rows and dispatches the `Due` groups
  as commands. Each returns a `githubMsg`, on which the model re-annotates,
  keeps the highlighted path and drops selected rows that are no longer
  visible, as `applySnapshot` does. `force` is set by `r`, the palette's
  Refresh, a returning shell or LazyGit, and a finished batch.
- While lookups run, the header activity reads `checking GitHub` with the
  spinner, without making the dashboard busy: keys and automatic refresh work
  as usual. Quitting cancels them.
- Status column, after the Git states (failed, conflict, operation, changed,
  diverged, locked, detached commits, lifecycle) and after ahead and behind:
  `× checks #42` and `! changes #42` in amber, and `✓ PR #42` in the success
  color for an open pull request without either. Glyphs come from the icon
  set, so ASCII mode shows `! checks #42` and `ok PR #42`.
- Preview card: one line under the header when the row has a pull request or
  a GitHub error, such as `PR #42 · open · checks failing · changes requested
  · 3h ago`, `PR #42 · merged into main · 2d ago` or `GitHub: gh is not logged
  in to github.com — run gh auth login`. Data from an earlier lookup adds
  `· checked 12m ago`. The card grows by that line; the smallest card (six
  rows) drops it so that one changed file still shows, and branches without a
  pull request get none.
- Details Overview: a "Pull request" section with the state, title (marked
  draft when it is), `base ← head` with the base repository, checks, review,
  updated or merged age and URL, up to three earlier pull requests of the
  branch, when it was checked and any error. When GitHub has none:
  `none for <branch> on <repository>`.
- Next step, when Git has nothing more urgent: `PR #42 checks are failing on
  GitHub.` and `Reviewers requested changes on PR #42.` Phase 4 appends
  `Open it with b.`

### CLI and configuration

```toml
[github]
enabled = true                  # default; false turns off gh use everywhere
hosts = ["github.example.com"]  # GitHub Enterprise Server hosts besides github.com
```

- `config.GitHub{Enabled, Hosts}`. Hosts must be bare host names (no scheme,
  path, port or whitespace) and are stored lower-case.
- `--github` works with `status` only; otherwise "--github requires status"
  and exit 2. Before scanning it exits 2, naming the cause, when GitHub use is
  disabled, gh is not on `PATH` or Git is older than 2.36.
- With `--github`, `status` runs `LookupAll` and `Annotate` after `Load`. The
  table's STATE column adds `pr #42 open`, `checks failing`,
  `changes requested` or `github: <error>`, and a second footer line says the
  pull request data came from GitHub through gh. Lookup errors never change the
  exit code; stderr gets one line counting failed lookups.
- Without `--github` the output is unchanged and gh never runs.
- `cmd/gitperch/tui.go` creates the service only when GitHub use is enabled,
  Git is 2.36 or newer and `Available()` is true.

## Phase 2: merge evidence

One rule, `mergeVerdict(pullRequests, tip)`, serves this phase and Clean up.
It decides in this order:

1. an open pull request for the branch: unproven, `PR #45 is still open`;
2. a pull request merged into the default branch whose head commit is the
   tip: proven;
3. another merged pull request: unproven,
   `PR #42 merged at abc1234; this branch is at def5678` or
   `PR #42 merged into release/1.2, not the default branch`;
4. a closed pull request: unproven, `PR #42 was closed without merging`;
5. none: no verdict.

`Row.MergedPullRequest()` is the proven pull request for the row's HEAD while
the row is on a branch (neither detached nor unborn), else nil: everything on
the branch went in through that pull request and nothing newer exists locally.

- Lifecycle: when the ancestry check does not show the worktree merged,
  evidence counts as merged with the new signal `merged_pull_request` in place
  of `not_merged` or `merge_unknown`, so a clean, quiet linked worktree becomes
  `likely_finished`. The signal reads `merged via PR #42 into main`, and
  `worktree_finished` uses the same wording.
- Attention: `pr_merged`, medium, sorted before `worktree_finished`, when
  evidence holds and the lifecycle state is not `likely_finished`, so it is
  never counted twice. It reads
  `PR #42 merged into main; nothing newer on this branch`.
- Status column: `✓ merged #42` in the success color, after the lifecycle
  labels and before detached, no upstream and unknown refs, so it replaces
  `! unknown refs` once GitHub has deleted the remote branch.
- Next step: in a main worktree `PR #42 is merged. Switch to main in your
  shell, then review the branch with Clean up (c).`; in a linked worktree
  `PR #42 is merged into main. Review it with Clean up (c).`
- JSON lifecycle signals add `merged_pull_request` after `merged`.

## Phase 3: Clean up for squash- and rebase-merged work

`Actions.SetGitHub(*GitHub)`; without it Clean up is unchanged.

### Fresh evidence

In `planCleanupGroup`, after the preflight fetch has resolved the default
ref, and before worktrees are assessed:

1. List local branches once and check each against the default ref once;
   the branch pass reuses both.
2. Candidates are unmerged branches with an upstream.
3. `GitHub.MergeEvidence(ctx, path, candidates)` asks GitHub now, without
   reading or updating the cache, and returns per branch a
   `MergeVerdict{PullRequest, Proven, Note, Failed, RestoreFrom}` decided by
   `mergeVerdict` against the branch tip:
   - `PullRequest` is the evidence when `Proven`, otherwise the pull request
     `Note` explains; nil without a verdict;
   - `Failed` is set when the lookup failed, with the dashboard's message
     for it as `Note`, such as
     `gh is not logged in to github.com — run gh auth login`;
   - `RestoreFrom` is the upstream remote's name when the evidence's base
     repository is the upstream's repository, otherwise the base repository's
     URL.

### New cleanup codes

| Code | Status | Text |
| --- | --- | --- |
| `merged_pull_request` | allowed | `merged via PR #42 into main on GitHub` |
| `pull_request_unproven` | review | The verdict's note |
| `github_check_failed` | review | The verdict's note |

### Worktrees

For a worktree on a branch that is not merged by ancestry,
`assessIntegration` adds `merged_pull_request` instead of `not_merged` when
the branch's verdict is proven for HEAD, and then skips
`upstream gone — squash merge?`. Unpushed and diverged checks against a live
upstream still run and still block. Otherwise the reasons are today's, plus
`pull_request_unproven` or `github_check_failed` with the verdict's note.
Every earlier check (lock, operation, conflicts, changes, untracked, ignored
and hidden files) is unchanged.

### Branches

The branch pass checks, in order:

1. merged by ancestry, as today;
2. a proven verdict for the tip: blocked by an operation in progress or a
   remaining checkout exactly like a merged branch, otherwise eligible with
   `merged_pull_request`;
3. upstream gone, as today, plus the verdict's note;
4. an unproven verdict about a merged pull request: listed for review;
5. otherwise the branch is not listed, as today.

`CleanupItem` gains `PullRequest *PullRequest` and `RestoreFrom string`.

### Revalidation

- Branch: the same checks as today (checkouts, operations, symbolic ref,
  commit). For an evidence item the ancestry recheck becomes "the tip still
  equals the pull request's head". GitHub is not asked again: a merged pull
  request stays merged, and `update-ref`'s compare-and-delete pins the commit.
- Worktree: `assessWorktree` runs with the item's evidence, so HEAD must still
  equal the pull request's head.

### Restore

Once a squash-merged branch's ref is deleted its commits are unreachable, and
`git gc` may drop them; GitHub keeps them as `refs/pull/<N>/head`. The restore
command of an evidence deletion (in the result, the restore log and the exit
summary) becomes one pasteable line, with names quoted as today:

```sh
git -C <repo> branch feat/x <oid> || git -C <repo> fetch <restore-from> refs/pull/42/head:refs/heads/feat/x
```

The result message keeps its `restore: git branch` prefix.

## Phase 4: open in the browser

- `github.Runner.BrowseCommand(repo, number, branch) (*exec.Cmd, error)`
  builds `gh browse <number> --repo <host/owner/name>` for a pull request, on
  its base repository; otherwise `gh browse --repo <…> --branch=<branch>` when
  the upstream tracking ref exists, else `gh browse --repo <…>`. It uses the
  runner's working directory and environment and honours `BROWSER` and
  `GH_BROWSER`.
- `b` in the workspace and in details, and the palette commands
  `Open pull request #42 in browser` or `Open owner/name on GitHub`, run it
  through `tea.ExecProcess`, so terminal browsers work. On return the status
  line says `Opened in the browser` or shows the error; nothing refreshes. Without
  GitHub data: `No GitHub repository for this branch`.
- Footer hint `b PR` when the highlighted row has a pull request, a help line,
  and the phase 1 next steps gain `Open it with b.`

## Error handling

| Situation | Behavior |
| --- | --- |
| gh missing, `[github] enabled = false`, Git older than 2.36 | TUI: the feature is off, with no warning. `status --github`: exit 2 with the cause. |
| Not logged in (exit 4) | `gh is not logged in to <host> — run gh auth login` |
| Repository not found or no access | `GitHub repository owner/name not found or not accessible with gh's login` |
| Rate limited | `GitHub API rate limit reached; gitperch checks again later` |
| Timeout, cancellation, output limit, unparseable output | `GitHub lookup failed: <reason>` |

Errors are cached like results (5 minutes; forced rechecks at most every 30
seconds), so failures never cause a retry storm, and earlier pull requests stay
on screen with their age. GitHub errors never raise attention, never add
workspace warnings and never change an exit code. In Clean up a failed lookup
leaves every item as Git alone judges it, plus `github_check_failed` on items
kept for review.

## Security

- gh runs only through `internal/github`'s runner, with argument arrays.
  Owner, repository and branch names appear only as `-f` values or in
  `--flag=value` form; query text never contains them.
- gh never runs inside a scanned repository, and its environment disables
  prompts, pagers, forced terminal output, colors and debug logging.
- Only `github.com` and configured hosts are contacted.
- Titles, URLs, branch names and errors from GitHub pass through `SafeText`
  before display.
- gitperch never reads, stores or prints a token.
- What is sent to GitHub, repository owners, names and remote branch names, is
  documented.

## Documentation

- README: feature bullet, `b` in the keys table, `[github]` in the
  configuration example, and the Clean up sentence under "Conservative by
  design".
- `docs/usage.md`: a "GitHub pull requests" section (activation, matching,
  cadence, data sent, limits), `status --github`, the new attention reasons
  and lifecycle signal, the Clean up evidence rule and restore commands, and
  "creating, merging or commenting on pull requests" in the list of things
  gitperch does not do.
- `docs/ui.md`: status labels, preview line, details section, `b` and
  captures. "Deliberately unavailable" keeps pull request creation and
  merging, agents, tests and tasks.
- `docs/plan.md`: a scope update note next to the 2026-10-06 one.
- `docs/development.md`: gh as an optional runtime tool; tests use fake gh
  executables.
- `AGENTS.md` and `CONTRIBUTING.md`: gh, like Git, runs only through its runner
  with argument arrays, and tests never run the real gh.

## Testing

Tests never run the real gh: CI runners have one installed. Git tests keep
using temporary repositories and local bare remotes.

- `internal/github`, with fake gh executables written per test that record
  argv (NUL-separated), working directory and environment, print a fixture and
  exit with a chosen status:
  - `api graphql --hostname`, only `-f` fields, query text free of names
    (including a branch named like `@etc/passwd` and an owner containing
    `{owner}`), the working directory, blocked and required environment
    variables;
  - exit 4, `NOT_FOUND`, `RATE_LIMITED`, unparseable output, timeout, output
    limit, more than 20 heads;
  - response fixtures: a same-repository pull request, a fork's pull request
    in the parent, the same branch name from another fork (ignored), a deleted
    head repository (`null`, ignored), drafts, every rollup and review state,
    no rollup, duplicates across repository and parent, ordering and the cap
    of 5, `IntoDefault`;
  - `ParseRemote` accepted and rejected URLs.
- `internal/git`: the new branch fields with live, gone and no upstream;
  `RemoteURL` with `insteadOf`; the read-only guarantee.
- `internal/app`, with fake Git and client: due, TTL, force, minimum gap,
  pending and pruning; grouping by common directory; chunks of 20; nil entries
  for non-GitHub remotes; failed rechecks keeping earlier pull requests;
  `CurrentPullRequest` and the `MergedPullRequest` matrix (exact commit,
  default base, an open pull request suppressing it, detached HEAD); attention
  reasons and order; the lifecycle signal; JSON with `github`; table markers;
  existing `status` goldens unchanged.
- Clean up, with local bare remotes and a fake client: a squash-merged clean
  worktree and its branch removed in one batch; an extra local commit, a pull
  request into another branch, an open one and a closed one kept for review
  with their text; a failed lookup adding `github_check_failed`; a dirty
  worktree with evidence still blocked; a branch moved after review skipped;
  an evidence item passing revalidation without ancestry; the restore command
  and log line with the `refs/pull` fallback, using the base repository's URL
  for a fork; every existing cleanup test passing unchanged without GitHub.
- TUI: status labels, preview line and card height, the details section,
  results keeping the highlight and dropping hidden selections, `force` set
  only by the manual triggers, quitting cancelling lookups, `b` building the
  expected command or explaining missing data, and render captures with
  GitHub fixtures.
- CLI: `--github` errors (dashboard mode, disabled, gh missing through a
  `PATH` override, old Git), table and JSON with a fake gh first on `PATH`,
  exit codes.

## Out of scope

- Creating, merging, approving or commenting on pull requests, and any other
  GitHub mutation.
- GitLab, Bitbucket and other forges.
- Resolving SSH host aliases from `~/.ssh/config`.
- Triangular setups, where the push remote differs from the upstream: their
  pull requests may not be found.
- CI status of the default branch, review-request inboxes and notifications.
- Deleting remote branches.
- Headless Clean up.
