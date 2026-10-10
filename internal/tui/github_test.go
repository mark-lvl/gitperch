package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// githubGit reports every branch as tracking its namesake on origin, which is
// acme/widgets on github.com.
type githubGit struct{}

func (githubGit) LocalBranches(context.Context, string) ([]gitcli.Branch, error) {
	var branches []gitcli.Branch
	for _, name := range []string{"main", "feat/x"} {
		branches = append(branches, gitcli.Branch{Name: name, Remote: "origin", RemoteRef: "refs/heads/" + name})
	}
	return branches, nil
}

func (githubGit) RemoteURL(context.Context, string, string) (string, error) {
	return "https://github.com/acme/widgets.git", nil
}

// githubPulls gives feat/x one open pull request whose checks it reports.
type githubPulls struct {
	checks string
	calls  atomic.Int32
}

func (p *githubPulls) PullRequests(_ context.Context, repo github.Repo, heads []string) (github.Lookup, error) {
	p.calls.Add(1)
	return github.Lookup{Repository: repo.FullName(), Heads: map[string][]github.PullRequest{
		"feat/x": {{Number: 42, Title: "Add tokens", State: "OPEN", Checks: p.checks, BaseRef: "main", BaseRepository: "acme/widgets"}},
	}}, nil
}

// githubRows are two clean, synchronized repositories: alpha on main and zeta
// on feat/x.
func githubRows() []app.Row {
	row := func(name, branch string) app.Row {
		return app.Row{Repository: repository.Repository{Name: name, Path: "/repos/" + name},
			Status: repository.Status{Branch: branch, Upstream: "origin/" + branch, ComparisonKnown: true, CommonDir: "/repos/" + name + "/.git"}}
	}
	return []app.Row{row("alpha", "main"), row("zeta", "feat/x")}
}

// prRow is a clean, synchronized row whose branch has the given pull requests.
func prRow(prs ...app.PullRequest) app.Row {
	row := githubRows()[1]
	row.GitHub = &app.GitHubInfo{Host: "github.com", Repository: "acme/widgets", Branch: "feat/x", PullRequests: prs, CheckedAt: captureNow.Add(-2 * time.Minute)}
	return row
}

var failingPR = app.PullRequest{Number: 42, Title: "Add tokens", URL: "https://github.com/acme/widgets/pull/42", State: app.PullRequestOpen, Draft: true,
	BaseRepository: "acme/widgets", Base: "main", IntoDefaultBranch: true, Checks: app.ChecksFailing, Review: app.ReviewChangesRequested, UpdatedAt: captureNow.Add(-3 * time.Hour)}

func TestGitHubStatusLabels(t *testing.T) {
	m := New(context.Background(), nil, true)
	open := failingPR
	open.Draft, open.Review = false, ""
	changes := open
	changes.Checks = app.ChecksPassing
	changes.Review = app.ReviewChangesRequested
	healthy := changes
	healthy.Review = app.ReviewRequired
	ahead := prRow(open)
	ahead.Status.Ahead = 2
	for _, tc := range []struct {
		row         app.Row
		label, text string
	}{
		{prRow(open), "× checks #42", "unicode"},
		{prRow(changes), "! changes #42", "unicode"},
		{prRow(healthy), "✓ PR #42", "unicode"},
		{prRow(open), "! checks #42", "ascii"},
		{prRow(healthy), "ok PR #42", "ascii"},
		{ahead, "↑ 2 commits", "unicode"}, // Git state comes first
		{prRow(app.PullRequest{Number: 9, State: app.PullRequestClosed}), "✓ clean", "unicode"},
	} {
		m.iconMode = tc.text
		if label, _ := m.primaryStatus(tc.row); label != tc.label {
			t.Errorf("%s: %q, want %q", tc.text, label, tc.label)
		}
	}
}

func TestPreviewShowsPullRequestLine(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.clock = func() time.Time { return captureNow }
	m.applySnapshot(app.Snapshot{Rows: []app.Row{prRow(failingPR)}})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
	withLine := m.layout().bottom
	if view := m.View().Content; !strings.Contains(view, "PR #42 · draft · checks failing · changes requested · 3h ago") {
		t.Fatalf("missing pull request line:\n%s", view)
	}
	m.applySnapshot(app.Snapshot{Rows: githubRows()[1:]})
	if view := m.View().Content; strings.Contains(view, "PR #") || m.layout().bottom != withLine-1 {
		t.Fatalf("a branch without a pull request got a line or the same card height:\n%s", view)
	}
	failed := prRow()
	failed.GitHub.Error = "gh is not logged in to github.com — run gh auth login"
	m.applySnapshot(app.Snapshot{Rows: []app.Row{failed}})
	if view := m.View().Content; !strings.Contains(view, "GitHub: gh is not logged in to github.com — run gh auth login") {
		t.Fatalf("missing GitHub error:\n%s", view)
	}
	stale := prRow(failingPR)
	stale.GitHub.Error = "GitHub lookup failed: timeout"
	m.applySnapshot(app.Snapshot{Rows: []app.Row{stale}})
	if view := m.View().Content; !strings.Contains(view, "· checked 2m ago") {
		t.Fatalf("stale data not labelled:\n%s", view)
	}
	// A six-row card, the smallest, keeps its file row and drops the line.
	for h, want := range map[int]bool{6: false, 7: true} {
		card := strings.Join(m.selectedPreview(56, h), "\n")
		if strings.Contains(card, "PR #42") != want || !strings.Contains(card, "Working tree") {
			t.Fatalf("card of height %d:\n%s", h, card)
		}
	}
}

func TestDetailsShowPullRequestSection(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.clock = func() time.Time { return captureNow }
	earlier := app.PullRequest{Number: 40, State: app.PullRequestMerged, Base: "main", MergedAt: captureNow.Add(-48 * time.Hour)}
	m.applySnapshot(app.Snapshot{Rows: []app.Row{prRow(failingPR, earlier)}})
	m.EnableActions(app.NewActions(newActionFake(false), 1)) // read-only dashboards show no next step
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m.details = true
	view := m.View().Content
	for _, want := range []string{"Pull request · draft", "#42 Add tokens (draft)", "main ← feat/x · acme/widgets", "Checks failing · Changes requested · updated 3h ago",
		"https://github.com/acme/widgets/pull/42", "Earlier: #40 merged into main", "Checked 2m ago", "PR #42 checks failing", "PR #42 checks are failing on GitHub."} {
		if !strings.Contains(view, want) {
			t.Errorf("details lack %q", want)
		}
	}
	none := prRow()
	m.applySnapshot(app.Snapshot{Rows: []app.Row{none}})
	if view := m.View().Content; !strings.Contains(view, "none for feat/x on acme/widgets") {
		t.Fatalf("details without pull requests:\n%s", view)
	}
}

func TestGitHubLookupsAnnotateRowsAndKeepHighlight(t *testing.T) {
	pulls := &githubPulls{checks: "FAILURE"}
	m := New(context.Background(), func(context.Context) (app.Snapshot, error) { return app.Snapshot{Rows: githubRows()}, nil }, true)
	m.EnableGitHub(app.NewGitHub(githubGit{}, pulls, nil))
	m.spinning, m.ticking = true, true // keep drained commands free of timers
	m.attentionFirst = true
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
	drain(m, m.Init())
	if pulls.calls.Load() != 2 || m.githubPending != 0 { // one query per repository
		t.Fatalf("calls %d, pending %d", pulls.calls.Load(), m.githubPending)
	}
	if first := m.rows[m.visibleRows()[0]]; first.Name != "zeta" || first.GitHub == nil {
		t.Fatalf("failing checks did not raise zeta: %+v", first)
	}
	failing := m.github
	passing := app.NewGitHub(githubGit{}, &githubPulls{checks: "SUCCESS"}, nil)
	passing.LookupAll(context.Background(), githubRows())

	// Passing checks drop zeta below alpha; the highlight follows alpha.
	highlightPath(t, m, "/repos/alpha")
	m.github = passing
	m.applyGitHub()
	if row := m.highlightedRow(); row == nil || row.Path != "/repos/alpha" || m.highlight != 0 {
		t.Fatalf("highlight %d on %+v", m.highlight, row)
	}

	// Focus shows only zeta while its checks fail; once they pass it is
	// hidden, so it must not stay selected.
	m.github = failing
	m.applyGitHub()
	m.setScope(1)
	m.key(key(" "))
	if !m.selected["/repos/zeta"] {
		t.Fatal("zeta not selected in Focus")
	}
	m.github = passing
	m.applyGitHub()
	if len(m.selected) != 0 {
		t.Fatalf("a row hidden by new GitHub data stayed selected: %v", m.selected)
	}
}

func TestOnlyManualRefreshesForceGitHub(t *testing.T) {
	m := New(context.Background(), func(context.Context) (app.Snapshot, error) { return app.Snapshot{Rows: githubRows()}, nil }, true)
	m.EnableGitHub(app.NewGitHub(githubGit{}, &githubPulls{}, nil))
	m.SetAutoRefresh(time.Minute)
	m.Update(autoRefreshMsg{generation: m.autoGeneration})
	if m.githubForce {
		t.Fatal("automatic refresh forced a GitHub recheck")
	}
	m.key(key("r"))
	if !m.githubForce {
		t.Fatal("r did not force a GitHub recheck")
	}
	m.githubPending = 1
	m.width = 110
	if line := m.summaryLineAt(106); !strings.Contains(line, "refreshing") && !strings.Contains(line, "checking GitHub") {
		t.Fatalf("header: %q", line)
	}
	m.loading, m.refreshUntil = false, time.Time{}
	if line := m.summaryLineAt(106); !strings.Contains(line, "checking GitHub") {
		t.Fatalf("header without the GitHub activity: %q", line)
	}
	m.key(key("q"))
	if m.githubCtx.Err() == nil {
		t.Fatal("quitting left GitHub lookups running")
	}
}
