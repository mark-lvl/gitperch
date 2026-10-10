package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
)

// GitHubInfo is what gh reported for a row's branch: the upstream's GitHub
// repository and the pull requests whose head is that branch.
type GitHubInfo struct {
	Host         string        `json:"host"`
	Repository   string        `json:"repository"`    // owner/name as GitHub reports it
	Branch       string        `json:"branch"`        // the upstream branch on GitHub
	PullRequests []PullRequest `json:"pull_requests"` // most relevant first; [] when none
	CheckedAt    time.Time     `json:"checked_at,omitzero"`
	// Error says why the latest lookup failed; PullRequests are then those
	// read at CheckedAt.
	Error string `json:"error,omitempty"`
}

// PullRequest is one pull request of a row's branch.
type PullRequest struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	URL               string    `json:"url"`
	State             string    `json:"state"` // PullRequestOpen, PullRequestMerged or PullRequestClosed
	Draft             bool      `json:"draft,omitempty"`
	BaseRepository    string    `json:"base_repository"`
	Base              string    `json:"base"`
	IntoDefaultBranch bool      `json:"into_default_branch"`
	HeadOID           string    `json:"head_oid"`
	Review            string    `json:"review,omitempty"` // ReviewApproved, ReviewChangesRequested or ReviewRequired
	Checks            string    `json:"checks,omitempty"` // ChecksPassing, ChecksFailing or ChecksPending
	MergedAt          time.Time `json:"merged_at,omitzero"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Pull request facts as JSON and the interface name them.
const (
	PullRequestOpen   = "open"
	PullRequestMerged = "merged"
	PullRequestClosed = "closed"

	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewRequired         = "review_required"

	ChecksPassing = "passing"
	ChecksFailing = "failing"
	ChecksPending = "pending"
)

// pullRequestFrom maps GitHub's enum values to gitperch's. An unknown state
// reads as closed, so it never counts as open work or as a merge.
func pullRequestFrom(p github.PullRequest) PullRequest {
	pr := PullRequest{Number: p.Number, Title: p.Title, URL: p.URL, State: PullRequestClosed, Draft: p.Draft, BaseRepository: p.BaseRepository,
		Base: p.BaseRef, IntoDefaultBranch: p.IntoDefault, HeadOID: p.HeadOID, MergedAt: p.MergedAt, UpdatedAt: p.UpdatedAt}
	switch p.State {
	case "OPEN":
		pr.State = PullRequestOpen
	case "MERGED":
		pr.State = PullRequestMerged
	}
	switch p.Review {
	case "APPROVED":
		pr.Review = ReviewApproved
	case "CHANGES_REQUESTED":
		pr.Review = ReviewChangesRequested
	case "REVIEW_REQUIRED":
		pr.Review = ReviewRequired
	}
	switch p.Checks {
	case "SUCCESS":
		pr.Checks = ChecksPassing
	case "FAILURE", "ERROR":
		pr.Checks = ChecksFailing
	case "PENDING", "EXPECTED":
		pr.Checks = ChecksPending
	}
	return pr
}

// CurrentPullRequest is the first listed pull request, an open one when any
// is open; nil without GitHub data or pull requests.
func (r Row) CurrentPullRequest() *PullRequest {
	if r.GitHub == nil || len(r.GitHub.PullRequests) == 0 {
		return nil
	}
	return &r.GitHub.PullRequests[0]
}

// MergeVerdict says whether GitHub proves a branch's work merged although Git
// cannot show it: Proven when a pull request merged into its repository's
// default branch had exactly the branch tip as its head. Otherwise Note
// explains PullRequest, the pull request that decided; both are empty when
// the branch has no pull requests.
type MergeVerdict struct {
	PullRequest *PullRequest
	Proven      bool
	Note        string
	// Failed reports that gh could not answer; Note says why.
	Failed bool
	// RestoreFrom is where a proven branch's commits can be fetched again
	// once deleted; see restoreSource.
	RestoreFrom string
}

// mergeVerdict decides in this order: an open pull request (unproven: work
// continues), a pull request merged into the default branch whose head is
// tip (proven), another merged one (unproven, saying why), a closed one.
func mergeVerdict(prs []PullRequest, tip string) MergeVerdict {
	find := func(match func(PullRequest) bool) *PullRequest {
		for i := range prs {
			if match(prs[i]) {
				return &prs[i]
			}
		}
		return nil
	}
	if pr := find(func(p PullRequest) bool { return p.State == PullRequestOpen }); pr != nil {
		return MergeVerdict{PullRequest: pr, Note: fmt.Sprintf("PR #%d is still open", pr.Number)}
	}
	if pr := find(func(p PullRequest) bool {
		return p.State == PullRequestMerged && p.IntoDefaultBranch && tip != "" && p.HeadOID == tip
	}); pr != nil {
		return MergeVerdict{PullRequest: pr, Proven: true}
	}
	if pr := find(func(p PullRequest) bool { return p.State == PullRequestMerged }); pr != nil {
		note := fmt.Sprintf("PR #%d merged into %s, not the default branch", pr.Number, gitcli.SafeText(pr.Base))
		if pr.IntoDefaultBranch {
			note = fmt.Sprintf("PR #%d merged at %s; this branch is at %s", pr.Number, shortOID(pr.HeadOID), shortOID(tip))
		}
		return MergeVerdict{PullRequest: pr, Note: note}
	}
	if pr := find(func(p PullRequest) bool { return p.State == PullRequestClosed }); pr != nil {
		return MergeVerdict{PullRequest: pr, Note: fmt.Sprintf("PR #%d was closed without merging", pr.Number)}
	}
	return MergeVerdict{}
}

// MergedPullRequest is GitHub's proof that the checked-out branch is merged:
// the proven pull request for HEAD, so everything on the branch went in
// through it and nothing newer exists locally. Nil otherwise.
func (r Row) MergedPullRequest() *PullRequest {
	s := r.Status
	if r.GitHub == nil || s.Detached || s.Unborn || s.Branch == "" || s.HeadOID == "" {
		return nil
	}
	if v := mergeVerdict(r.GitHub.PullRequests, s.HeadOID); v.Proven {
		return v.PullRequest
	}
	return nil
}

// MergeEvidence asks GitHub now, bypassing the cache, for a verdict on each
// branch whose upstream is on GitHub. A failed lookup gives that repository's
// branches Failed verdicts; branches without pull requests get none.
func (g *GitHub) MergeEvidence(ctx context.Context, path string, branches []gitcli.Branch) map[string]MergeVerdict {
	verdicts := map[string]MergeVerdict{}
	targets := g.targets(ctx, path, branches)
	heads := map[github.Repo][]string{}
	for _, t := range targets {
		heads[t.repo] = append(heads[t.repo], t.head)
	}
	if len(heads) == 0 {
		return verdicts
	}
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return verdicts
	}
	defer func() { <-g.slots }()
	answers := g.queryAll(ctx, heads)
	for _, b := range branches {
		t, ok := targets[b.Name]
		if !ok {
			continue
		}
		a := answers[t.repo]
		if a.err != nil {
			verdicts[b.Name] = MergeVerdict{Failed: true, Note: githubError(t.repo, a.err)}
			continue
		}
		v := mergeVerdict(a.prs[t.head], b.OID)
		if v.PullRequest == nil {
			continue
		}
		if v.Proven {
			v.RestoreFrom = restoreSource(t.remote, a.name, *v.PullRequest)
		}
		verdicts[b.Name] = v
	}
	return verdicts
}

// restoreSource is where a deleted squash-merged branch's commits can be
// fetched again from GitHub's pull request ref: the upstream remote when the
// pull request lives in its repository, else the base repository's URL, as
// for a fork's pull request into its parent.
func restoreSource(remote, repository string, pr PullRequest) string {
	if strings.EqualFold(pr.BaseRepository, repository) {
		return remote
	}
	return strings.TrimSuffix(pr.URL, fmt.Sprintf("/pull/%d", pr.Number))
}

// GitHub lookup cadence; see GitHub.Due.
const (
	// GitHubTTL is how long a lookup's result is reused.
	GitHubTTL = 5 * time.Minute
	// GitHubMinGap is the youngest result a forced recheck replaces.
	GitHubMinGap = 30 * time.Second
	// githubSlots bounds concurrent gh processes.
	githubSlots = 2
)

// GitHubGit is the local Git surface pull request lookups read.
type GitHubGit interface {
	LocalBranches(context.Context, string) ([]gitcli.Branch, error)
	RemoteURL(context.Context, string, string) (string, error)
}

// PullRequestClient answers pull request queries; github.Client is one.
type PullRequestClient interface {
	PullRequests(context.Context, github.Repo, []string) (github.Lookup, error)
}

type githubEntry struct {
	info    *GitHubInfo // nil: the branch has no GitHub upstream
	checked time.Time   // when the latest lookup finished
}

// GitHub looks up the pull requests of rows' branches through gh and caches
// them per row path and branch. It is safe for concurrent use.
type GitHub struct {
	git    GitHubGit
	client PullRequestClient
	hosts  []string
	slots  chan struct{}
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]githubEntry
	pending map[string]bool // groups being looked up
}

// NewGitHub returns a service that asks client about branches whose upstream
// is on github.com or one of hosts.
func NewGitHub(git GitHubGit, client PullRequestClient, hosts []string) *GitHub {
	return &GitHub{git: git, client: client, hosts: slices.Clone(hosts), slots: make(chan struct{}, githubSlots), now: time.Now,
		entries: map[string]githubEntry{}, pending: map[string]bool{}}
}

func githubKey(r Row) string { return r.Path + "\x00" + r.Status.Branch }

// githubGroup shares one branch listing per repository.
func githubGroup(r Row) string {
	if r.Status.CommonDir != "" {
		return r.Status.CommonDir
	}
	return r.Path
}

// lookupCandidate reports whether a row's branch can have pull requests: an
// inspected worktree on a branch with an upstream.
func lookupCandidate(r Row) bool {
	s := r.Status
	return r.Selectable() && s.Error == "" && !s.Detached && !s.Unborn && s.Branch != "" && s.Upstream != ""
}

// Due returns the candidate rows whose lookup is missing or older than
// GitHubTTL, or, when forced, older than GitHubMinGap, grouped by repository.
// Returned groups stay pending until Lookup finishes them, so no group is
// looked up twice at once. Entries of rows no longer present are dropped.
func (g *GitHub) Due(rows []Row, force bool) [][]Row {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	present := map[string]bool{}
	groups := map[string][]Row{}
	var order []string
	for _, row := range rows {
		if !lookupCandidate(row) {
			continue
		}
		key, group := githubKey(row), githubGroup(row)
		present[key] = true
		if g.pending[group] {
			continue
		}
		if entry, ok := g.entries[key]; ok {
			age := now.Sub(entry.checked)
			if age < GitHubTTL && !(force && age >= GitHubMinGap) {
				continue
			}
		}
		if _, seen := groups[group]; !seen {
			order = append(order, group)
		}
		groups[group] = append(groups[group], row)
	}
	for key := range g.entries {
		if !present[key] {
			delete(g.entries, key)
		}
	}
	due := make([][]Row, 0, len(order))
	for _, group := range order {
		g.pending[group] = true
		due = append(due, groups[group])
	}
	return due
}

// Lookup refreshes the entries of one group returned by Due. Failures are
// recorded on the rows' entries, never returned; a cancelled lookup records
// nothing.
func (g *GitHub) Lookup(ctx context.Context, group []Row) {
	if len(group) == 0 {
		return
	}
	defer func() {
		g.mu.Lock()
		delete(g.pending, githubGroup(group[0]))
		g.mu.Unlock()
	}()
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	results := g.lookup(ctx, group)
	<-g.slots
	if ctx.Err() != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, row := range group {
		key := githubKey(row)
		info := results[key]
		if old := g.entries[key].info; info != nil && info.Error != "" && old != nil && strings.EqualFold(old.Repository, info.Repository) && old.Branch == info.Branch {
			// On error info.Repository is the remote URL's spelling; keep
			// GitHub's own name for the repository.
			info.Repository, info.PullRequests, info.CheckedAt = old.Repository, old.PullRequests, old.CheckedAt
		}
		g.entries[key] = githubEntry{info: info, checked: now}
	}
}

// LookupAll looks up every candidate row now, for gitperch status --github.
func (g *GitHub) LookupAll(ctx context.Context, rows []Row) {
	var wg sync.WaitGroup
	for _, group := range g.Due(rows, true) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Lookup(ctx, group)
		}()
	}
	wg.Wait()
}

// Annotate sets each row's GitHub field from the cache, without I/O.
func (g *GitHub) Annotate(rows []Row) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range rows {
		rows[i].GitHub = nil
		if !lookupCandidate(rows[i]) {
			continue
		}
		if entry, ok := g.entries[githubKey(rows[i])]; ok && entry.info != nil {
			info := *entry.info
			info.PullRequests = slices.Clone(info.PullRequests)
			rows[i].GitHub = &info
		}
	}
}

// githubTarget is a branch's upstream on GitHub.
type githubTarget struct {
	repo   github.Repo
	remote string // the upstream's remote name
	head   string // the upstream branch on GitHub
}

// targets maps branches to their upstream's GitHub repository and branch.
// Branches without an upstream, or whose remote is not on a GitHub host, are
// left out.
func (g *GitHub) targets(ctx context.Context, path string, branches []gitcli.Branch) map[string]githubTarget {
	targets := map[string]githubTarget{}
	urls := map[string]string{}
	for _, b := range branches {
		head, ok := strings.CutPrefix(b.RemoteRef, "refs/heads/")
		if b.Remote == "" || b.Remote == "." || !ok || head == "" {
			continue
		}
		url, seen := urls[b.Remote]
		if !seen {
			url, _ = g.git.RemoteURL(ctx, path, b.Remote) // unreadable: not GitHub
			urls[b.Remote] = url
		}
		if repo, ok := github.ParseRemote(url, g.hosts); ok {
			targets[b.Name] = githubTarget{repo: repo, remote: b.Remote, head: head}
		}
	}
	return targets
}

// query asks GitHub about heads of repo, github.MaxHeads at a time. It
// returns the repository's name as GitHub reports it and the pull requests by
// head. Callers hold one of the service's slots.
func (g *GitHub) query(ctx context.Context, repo github.Repo, heads []string) (string, map[string][]PullRequest, error) {
	heads = slices.Clone(heads)
	sort.Strings(heads)
	heads = slices.Compact(heads)
	name, prs := repo.FullName(), map[string][]PullRequest{}
	for start := 0; start < len(heads); start += github.MaxHeads {
		chunk := heads[start:min(len(heads), start+github.MaxHeads)]
		lookup, err := g.client.PullRequests(ctx, repo, chunk)
		if err != nil {
			return name, nil, err
		}
		if lookup.Repository != "" {
			name = lookup.Repository
		}
		for _, head := range chunk {
			list := []PullRequest{}
			for _, p := range lookup.Heads[head] {
				list = append(list, pullRequestFrom(p))
			}
			prs[head] = list
		}
	}
	return name, prs, nil
}

// repoAnswer is one GitHub repository's answer to a pull request query.
type repoAnswer struct {
	name string
	prs  map[string][]PullRequest
	err  error
}

// queryAll asks each GitHub repository about its heads. Callers hold one of
// the service's slots.
func (g *GitHub) queryAll(ctx context.Context, heads map[github.Repo][]string) map[github.Repo]repoAnswer {
	answers := map[github.Repo]repoAnswer{}
	for repo, list := range heads {
		name, prs, err := g.query(ctx, repo, list)
		answers[repo] = repoAnswer{name, prs, err}
	}
	return answers
}

// lookup answers one group: it reads the repository's branches once, maps
// rows to GitHub, and asks once per GitHub repository.
func (g *GitHub) lookup(ctx context.Context, group []Row) map[string]*GitHubInfo {
	results := map[string]*GitHubInfo{}
	branches, err := g.git.LocalBranches(ctx, group[0].Path)
	if err != nil {
		for _, row := range group {
			results[githubKey(row)] = &GitHubInfo{PullRequests: []PullRequest{}, Error: "GitHub lookup failed: could not read branch upstreams: " + gitcli.SafeText(err.Error())}
		}
		return results
	}
	targets := g.targets(ctx, group[0].Path, branches)
	heads := map[github.Repo][]string{}
	for _, row := range group {
		if t, ok := targets[row.Status.Branch]; ok {
			heads[t.repo] = append(heads[t.repo], t.head)
		}
	}
	answers := g.queryAll(ctx, heads)
	for _, row := range group {
		t, ok := targets[row.Status.Branch]
		if !ok {
			continue // no GitHub upstream: a nil entry
		}
		a := answers[t.repo]
		info := &GitHubInfo{Host: t.repo.Host, Repository: a.name, Branch: t.head, PullRequests: []PullRequest{}}
		if a.err != nil {
			info.Error = githubError(t.repo, a.err)
		} else {
			info.PullRequests, info.CheckedAt = a.prs[t.head], g.now()
		}
		results[githubKey(row)] = info
	}
	return results
}

// githubError states a failed lookup for display.
func githubError(repo github.Repo, err error) string {
	host, name := gitcli.SafeText(repo.Host), gitcli.SafeText(repo.FullName())
	switch {
	case errors.Is(err, github.ErrNotAuthenticated):
		return "gh is not logged in to " + host + " — run gh auth login"
	case errors.Is(err, github.ErrRepositoryNotFound):
		return "GitHub repository " + name + " not found or not accessible with gh's login"
	case errors.Is(err, github.ErrRateLimited):
		return "GitHub API rate limit reached; gitperch checks again later"
	}
	return "GitHub lookup failed: " + gitcli.SafeText(err.Error())
}
