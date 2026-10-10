package app

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// fakeGitHubGit serves branch upstreams and remote URLs from memory.
type fakeGitHubGit struct {
	branches []gitcli.Branch
	urls     map[string]string // remote name to URL
	err      error
}

func (f fakeGitHubGit) LocalBranches(context.Context, string) ([]gitcli.Branch, error) {
	return f.branches, f.err
}

func (f fakeGitHubGit) RemoteURL(_ context.Context, _ string, remote string) (string, error) {
	if url, ok := f.urls[remote]; ok {
		return url, nil
	}
	return "", errors.New("no such remote")
}

// fakePulls answers pull request queries from memory and records each
// query's heads.
type fakePulls struct {
	mu    sync.Mutex
	prs   map[string][]github.PullRequest // by head
	name  string                          // the repository name to report; the queried one when empty
	err   error
	calls [][]string
}

func (f *fakePulls) PullRequests(_ context.Context, repo github.Repo, heads []string) (github.Lookup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(heads))
	if f.err != nil {
		return github.Lookup{}, f.err
	}
	lookup := github.Lookup{Repository: cmp.Or(f.name, repo.FullName()), Heads: map[string][]github.PullRequest{}}
	for _, head := range heads {
		lookup.Heads[head] = f.prs[head]
	}
	return lookup, nil
}

func (f *fakePulls) queries() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

const headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// ghRow is an inspected worktree of /repo on branch, tracking origin.
func ghRow(path, branch string) Row {
	return Row{Repository: repository.Repository{Name: path[strings.LastIndex(path, "/")+1:], Path: path},
		Status: repository.Status{Branch: branch, Upstream: "origin/" + branch, ComparisonKnown: true, CommonDir: "/repo/.git", HeadOID: headA}}
}

// upstream is a local branch whose upstream is remote's branch head.
func upstream(name, remote, head string) gitcli.Branch {
	return gitcli.Branch{Name: name, OID: headA, Remote: remote, RemoteRef: "refs/heads/" + head}
}

var githubOrigin = map[string]string{"origin": "git@github.com:acme/widgets.git", "gitlab": "https://gitlab.com/acme/widgets.git"}

func TestGitHubLookupAnnotatesRows(t *testing.T) {
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a"), upstream("feat/b", "origin", "topic-b"), upstream("lab", "gitlab", "lab")}, urls: githubOrigin}
	pulls := &fakePulls{prs: map[string][]github.PullRequest{"feat/a": {{Number: 42, State: "OPEN", Review: "CHANGES_REQUESTED", Checks: "FAILURE", BaseRef: "main", IntoDefault: true}}}}
	gh := NewGitHub(git, pulls, nil)
	noUpstream := Row{Repository: repository.Repository{Name: "local", Path: "/repo-local"}, Status: repository.Status{Branch: "local", CommonDir: "/repo/.git"}}
	rows := []Row{ghRow("/repo", "feat/a"), ghRow("/repo-b", "feat/b"), ghRow("/repo-lab", "lab"), noUpstream}
	due := gh.Due(rows, false)
	if len(due) != 1 || len(due[0]) != 3 {
		t.Fatalf("due: %+v", due)
	}
	gh.Lookup(context.Background(), due[0])
	if calls := pulls.queries(); len(calls) != 1 || !slices.Equal(calls[0], []string{"feat/a", "topic-b"}) {
		t.Fatalf("queries: %q", calls)
	}
	gh.Annotate(rows)
	a := rows[0].GitHub
	if a == nil || a.Host != "github.com" || a.Repository != "acme/widgets" || a.Branch != "feat/a" || a.CheckedAt.IsZero() || a.Error != "" {
		t.Fatalf("feat/a: %+v", a)
	}
	if pr := rows[0].CurrentPullRequest(); pr == nil || pr.Number != 42 || pr.State != PullRequestOpen || pr.Review != ReviewChangesRequested || pr.Checks != ChecksFailing || !pr.IntoDefaultBranch {
		t.Fatalf("current pull request: %+v", pr)
	}
	if b := rows[1].GitHub; b == nil || b.Branch != "topic-b" || b.PullRequests == nil || len(b.PullRequests) != 0 || rows[1].CurrentPullRequest() != nil {
		t.Fatalf("feat/b: %+v", b)
	}
	if rows[2].GitHub != nil || rows[3].GitHub != nil {
		t.Fatalf("non-GitHub rows annotated: %+v / %+v", rows[2].GitHub, rows[3].GitHub)
	}
}

func TestGitHubDueHonoursTTLForceAndPending(t *testing.T) {
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a")}, urls: githubOrigin}
	rows := []Row{ghRow("/repo", "feat/a")}
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		after time.Duration
		force bool
		due   bool
	}{
		{time.Minute, false, false},
		{GitHubTTL, false, true},
		{10 * time.Second, true, false},
		{GitHubMinGap, true, true},
	} {
		now := start
		gh := NewGitHub(git, &fakePulls{}, nil)
		gh.now = func() time.Time { return now }
		first := gh.Due(rows, false)
		if len(first) != 1 || len(gh.Due(rows, true)) != 0 {
			t.Fatal("a pending group was returned twice")
		}
		gh.Lookup(context.Background(), first[0])
		now = start.Add(tc.after)
		if got := gh.Due(rows, tc.force); (len(got) == 1) != tc.due {
			t.Errorf("after %s, force %v: due %v, want %v", tc.after, tc.force, len(got) == 1, tc.due)
		}
	}
}

func TestGitHubLookupChunksHeadsAndForgetsVanishedRows(t *testing.T) {
	var branches []gitcli.Branch
	var rows []Row
	for i := range 45 {
		name := fmt.Sprintf("b%02d", i)
		branches = append(branches, upstream(name, "origin", name))
		rows = append(rows, ghRow("/repo/"+name, name))
	}
	pulls := &fakePulls{}
	gh := NewGitHub(fakeGitHubGit{branches: branches, urls: githubOrigin}, pulls, nil)
	gh.LookupAll(context.Background(), rows)
	var sizes []int
	for _, heads := range pulls.queries() {
		sizes = append(sizes, len(heads))
	}
	if !slices.Equal(sizes, []int{20, 20, 5}) {
		t.Fatalf("query sizes %v", sizes)
	}
	gh.Due(nil, false)
	if len(gh.entries) != 0 {
		t.Fatalf("entries kept for vanished rows: %d", len(gh.entries))
	}
}

func TestGitHubFailedRecheckKeepsEarlierPullRequests(t *testing.T) {
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a")}, urls: githubOrigin}
	pulls := &fakePulls{prs: map[string][]github.PullRequest{"feat/a": {{Number: 7, State: "OPEN"}}}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	first := now
	gh := NewGitHub(git, pulls, nil)
	gh.now = func() time.Time { return now }
	rows := []Row{ghRow("/repo", "feat/a")}
	gh.LookupAll(context.Background(), rows)
	pulls.err = github.ErrNotAuthenticated
	now = now.Add(GitHubTTL)
	gh.LookupAll(context.Background(), rows)
	gh.Annotate(rows)
	info := rows[0].GitHub
	if info == nil || info.Error != "gh is not logged in to github.com — run gh auth login" || len(info.PullRequests) != 1 || !info.CheckedAt.Equal(first) {
		t.Fatalf("after a failed recheck: %+v", info)
	}
}

func TestGitHubFailedBranchListingKeepsEarlierEntries(t *testing.T) {
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a"), upstream("feat/b", "gitlab", "feat/b")}, urls: githubOrigin}
	pulls := &fakePulls{prs: map[string][]github.PullRequest{"feat/a": {{Number: 7, State: "OPEN"}}}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	first := now
	gh := NewGitHub(git, pulls, nil)
	gh.now = func() time.Time { return now }
	rows := []Row{ghRow("/repo", "feat/a"), ghRow("/repo-b", "feat/b")}
	gh.LookupAll(context.Background(), rows)
	gh.git = fakeGitHubGit{err: errors.New("broken refs")}
	now = now.Add(GitHubTTL)
	gh.LookupAll(context.Background(), rows)
	gh.Annotate(rows)
	info := rows[0].GitHub
	if info == nil || !strings.Contains(info.Error, "could not read branch upstreams: broken refs") || info.Repository != "acme/widgets" ||
		len(info.PullRequests) != 1 || info.PullRequests[0].Number != 7 || !info.CheckedAt.Equal(first) {
		t.Fatalf("GitHub row after a failed branch listing: %+v", info)
	}
	if info := rows[1].GitHub; info != nil {
		t.Fatalf("GitLab row gained a GitHub entry: %+v", info)
	}
}

func TestGitHubFailedRecheckMatchesRepositoryNameIgnoringCase(t *testing.T) {
	// GitHub reports the canonical "acme/widgets"; the remote URL spells it
	// "Acme/Widgets", which is what a failed lookup knows.
	git := fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a")}, urls: map[string]string{"origin": "git@github.com:Acme/Widgets.git"}}
	pulls := &fakePulls{prs: map[string][]github.PullRequest{"feat/a": {{Number: 7, State: "OPEN"}}}, name: "acme/widgets"}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	first := now
	gh := NewGitHub(git, pulls, nil)
	gh.now = func() time.Time { return now }
	rows := []Row{ghRow("/repo", "feat/a")}
	gh.LookupAll(context.Background(), rows)
	gh.Annotate(rows)
	if info := rows[0].GitHub; info == nil || info.Repository != "acme/widgets" {
		t.Fatalf("first lookup: %+v", info)
	}
	pulls.err = github.ErrNotAuthenticated
	now = now.Add(GitHubTTL)
	gh.LookupAll(context.Background(), rows)
	gh.Annotate(rows)
	info := rows[0].GitHub
	if info == nil || info.Error == "" || len(info.PullRequests) != 1 || info.PullRequests[0].Number != 7 || !info.CheckedAt.Equal(first) || info.Repository != "acme/widgets" {
		t.Fatalf("after a failed recheck: %+v", info)
	}
}

func TestGitHubLookupFailuresAndCancellation(t *testing.T) {
	rows := []Row{ghRow("/repo", "feat/a")}
	gh := NewGitHub(fakeGitHubGit{err: errors.New("broken refs")}, &fakePulls{}, nil)
	gh.LookupAll(context.Background(), rows)
	gh.Annotate(rows)
	if info := rows[0].GitHub; info == nil || !strings.Contains(info.Error, "could not read branch upstreams: broken refs") {
		t.Fatalf("branch list failure: %+v", info)
	}
	pulls := &fakePulls{}
	gh = NewGitHub(fakeGitHubGit{branches: []gitcli.Branch{upstream("feat/a", "origin", "feat/a")}, urls: githubOrigin}, pulls, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gh.Lookup(ctx, gh.Due(rows, false)[0])
	if len(gh.entries) != 0 || len(gh.Due(rows, false)) != 1 {
		t.Fatal("a cancelled lookup recorded a result or stayed pending")
	}
}

func TestGitHubErrorMessages(t *testing.T) {
	repo := github.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}
	for err, want := range map[error]string{
		github.ErrNotAuthenticated:   "gh is not logged in to github.com — run gh auth login",
		github.ErrRepositoryNotFound: "GitHub repository acme/widgets not found or not accessible with gh's login",
		github.ErrRateLimited:        "GitHub API rate limit reached; gitperch checks again later",
		errors.New("timeout\x1b[2J"): `GitHub lookup failed: timeout\x1b[2J`,
	} {
		if got := githubError(repo, err); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}

func TestPullRequestFromMapsGitHubValues(t *testing.T) {
	for _, tc := range []struct {
		in                    github.PullRequest
		state, review, checks string
	}{
		{github.PullRequest{State: "OPEN", Review: "APPROVED", Checks: "SUCCESS"}, PullRequestOpen, ReviewApproved, ChecksPassing},
		{github.PullRequest{State: "OPEN", Review: "REVIEW_REQUIRED", Checks: "ERROR"}, PullRequestOpen, ReviewRequired, ChecksFailing},
		{github.PullRequest{State: "MERGED", Checks: "EXPECTED"}, PullRequestMerged, "", ChecksPending},
		{github.PullRequest{State: "CLOSED", Checks: "PENDING"}, PullRequestClosed, "", ChecksPending},
		{github.PullRequest{State: "SOMETHING_NEW"}, PullRequestClosed, "", ""},
	} {
		got := pullRequestFrom(tc.in)
		if got.State != tc.state || got.Review != tc.review || got.Checks != tc.checks {
			t.Errorf("%+v: %+v", tc.in, got)
		}
	}
}

func TestReportIncludesGitHubOnlyWhenAnnotated(t *testing.T) {
	row := ghRow("/repo", "feat/a")
	var out bytes.Buffer
	if err := WriteJSON(&out, []Row{row}, nil); err != nil || strings.Contains(out.String(), `"github"`) {
		t.Fatalf("unannotated report: %v\n%s", err, out.String())
	}
	row.GitHub = &GitHubInfo{Host: "github.com", Repository: "acme/widgets", Branch: "feat/a", PullRequests: []PullRequest{{Number: 42, State: PullRequestOpen, Checks: ChecksFailing}}}
	out.Reset()
	if err := WriteJSON(&out, []Row{row}, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"github": {`, `"repository": "acme/widgets"`, `"number": 42`, `"state": "open"`, `"checks": "failing"`, `"into_default_branch": false`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %s:\n%s", want, out.String())
		}
	}
}

func TestMergeVerdict(t *testing.T) {
	merged := func(number int, head string, intoDefault bool) PullRequest {
		return PullRequest{Number: number, State: PullRequestMerged, Base: "main", IntoDefaultBranch: intoDefault, HeadOID: head}
	}
	release := merged(43, headA, false)
	release.Base = "release"
	other := strings.Repeat("1", 40)
	for _, tc := range []struct {
		name   string
		prs    []PullRequest
		number int
		proven bool
		note   string
		noTip  bool // the branch tip is unknown (empty)
	}{
		{"none", nil, 0, false, "", false},
		{"merged with the tip", []PullRequest{merged(42, headA, true)}, 42, true, "", false},
		{"an open one comes first", []PullRequest{{Number: 45, State: PullRequestOpen}, merged(42, headA, true)}, 45, false, "PR #45 is still open", false},
		{"an older merge proves it", []PullRequest{merged(44, other, true), merged(42, headA, true)}, 42, true, "", false},
		{"newer local commits", []PullRequest{merged(42, other, true)}, 42, false, "PR #42 merged at 1111111; this branch is at aaaaaaa", false},
		{"merged elsewhere", []PullRequest{release}, 43, false, "PR #43 merged into release, not the default branch", false},
		{"no tip proves nothing", []PullRequest{merged(42, "", true)}, 42, false, "PR #42 merged at ; this branch is at ", true},
		{"closed", []PullRequest{{Number: 41, State: PullRequestClosed}}, 41, false, "PR #41 was closed without merging", false},
	} {
		tip := headA
		if tc.noTip {
			tip = ""
		}
		v := mergeVerdict(tc.prs, tip)
		number := 0
		if v.PullRequest != nil {
			number = v.PullRequest.Number
		}
		if number != tc.number || v.Proven != tc.proven || v.Note != tc.note {
			t.Errorf("%s: %+v (PR %d)", tc.name, v, number)
		}
	}
}

func TestMergedPullRequestNeedsTheCheckedOutHead(t *testing.T) {
	row := ghRow("/repo", "feat/a")
	row.GitHub = &GitHubInfo{PullRequests: []PullRequest{{Number: 42, State: PullRequestMerged, IntoDefaultBranch: true, HeadOID: headA}}}
	if pr := row.MergedPullRequest(); pr == nil || pr.Number != 42 {
		t.Fatalf("merged: %+v", pr)
	}
	for name, edit := range map[string]func(*Row){
		"detached":    func(r *Row) { r.Status.Detached = true },
		"no branch":   func(r *Row) { r.Status.Branch = "" },
		"unborn":      func(r *Row) { r.Status.Unborn = true },
		"moved on":    func(r *Row) { r.Status.HeadOID = strings.Repeat("b", 40) },
		"no data":     func(r *Row) { r.GitHub = nil },
		"no head oid": func(r *Row) { r.Status.HeadOID = "" },
	} {
		r := row
		edit(&r)
		if pr := r.MergedPullRequest(); pr != nil {
			t.Errorf("%s: %+v", name, pr)
		}
	}
}

func TestBrowseTarget(t *testing.T) {
	row := ghRow("/repo", "feat/a")
	if _, ok := row.BrowseTarget(); ok {
		t.Fatal("a row without GitHub data has a page")
	}
	row.GitHub = &GitHubInfo{Host: "github.com", Repository: "me/widgets", Branch: "feat/a", PullRequests: []PullRequest{}}
	if got, ok := row.BrowseTarget(); !ok || got != (BrowseTarget{Host: "github.com", Repository: "me/widgets", Branch: "feat/a"}) {
		t.Fatalf("pushed branch: %+v", got)
	}
	row.Status.ComparisonKnown = false // the remote branch is gone
	if got, _ := row.BrowseTarget(); got.Branch != "" {
		t.Fatalf("gone branch: %+v", got)
	}
	row.GitHub.PullRequests = []PullRequest{{Number: 7, BaseRepository: "acme/widgets"}}
	if got, _ := row.BrowseTarget(); got != (BrowseTarget{Host: "github.com", Repository: "acme/widgets", Number: 7}) {
		t.Fatalf("fork pull request: %+v", got)
	}
}
