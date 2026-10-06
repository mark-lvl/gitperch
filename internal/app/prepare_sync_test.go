package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// preflightGit is a push-ready repository set whose fetches record how many
// run at once. A fetch waits briefly for meet fetches to be running together,
// so overlap is observed without relying on timing.
type preflightGit struct {
	commonDir    func(path string) string
	meet         int
	mu           sync.Mutex
	active, peak int
	together     chan struct{}
}

func newPreflightGit(meet int, commonDir func(string) string) *preflightGit {
	return &preflightGit{commonDir: commonDir, meet: meet, together: make(chan struct{})}
}

func (g *preflightGit) Metadata(_ context.Context, path string) (gitcli.Metadata, error) {
	s := repository.Status{Branch: "main", HeadOID: strings.Repeat("a", 40), Upstream: "origin/main", ComparisonKnown: true, Ahead: 1}
	return gitcli.Metadata{Status: s, CommonDir: g.commonDir(path), ConfigHash: "config"}, nil
}
func (g *preflightGit) ResolveFetch(context.Context, string, gitcli.Metadata) (gitcli.FetchTarget, error) {
	return gitcli.FetchTarget{Remote: "origin", URL: "url"}, nil
}
func (g *preflightGit) Fetch(ctx context.Context, _ string, _ gitcli.FetchTarget) error {
	g.mu.Lock()
	g.active++
	g.peak = max(g.peak, g.active)
	if g.active == g.meet {
		close(g.together)
		g.together = make(chan struct{})
	}
	together := g.together
	g.mu.Unlock()
	select {
	case <-together:
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
	}
	g.mu.Lock()
	g.active--
	g.mu.Unlock()
	return ctx.Err()
}
func (g *preflightGit) Inspect(context.Context, string) repository.Status { return repository.Status{} }
func (g *preflightGit) ResolveUpstream(context.Context, string, gitcli.Metadata) (gitcli.UpstreamTarget, error) {
	return gitcli.UpstreamTarget{Fetch: gitcli.FetchTarget{Remote: "origin", URL: "url"}, Branch: "refs/heads/main", TrackingRef: "refs/remotes/origin/main"}, nil
}
func (g *preflightGit) ResolvePush(_ context.Context, _ string, m gitcli.Metadata, u gitcli.UpstreamTarget) (gitcli.PushTarget, error) {
	return gitcli.PushTarget{Remote: "origin", URL: "url", SourceRef: "refs/heads/main", DestinationRef: u.Branch, HeadOID: m.Status.HeadOID}, nil
}
func (g *preflightGit) UpstreamCommit(context.Context, string, gitcli.UpstreamTarget) (string, error) {
	return strings.Repeat("b", 40), nil
}
func (g *preflightGit) Push(context.Context, string, gitcli.PushTarget) error { return nil }
func (g *preflightGit) FastForward(context.Context, string, string) error     { return nil }

func preparePush(t *testing.T, g *preflightGit, workers int, paths []string) Preview {
	t.Helper()
	actions := NewActions(g, workers)
	scope, err := actions.Plan(context.Background(), Fetch, paths)
	if err != nil {
		t.Fatal(err)
	}
	final, err := actions.PrepareSync(context.Background(), scope.ID, Push)
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func TestPrepareSyncFetchesSeparateRepositoriesConcurrently(t *testing.T) {
	g := newPreflightGit(2, func(path string) string { return "/common" + path })
	paths := []string{"/d", "/c", "/b", "/a"}
	final := preparePush(t, g, 2, paths)
	if g.peak != 2 {
		t.Fatalf("peak concurrent preflight fetches = %d, want action_workers (2)", g.peak)
	}
	for i, target := range final.Targets {
		if want := fmt.Sprintf("/%c", 'a'+i); target.Path != want || !target.Eligible || target.Action != Push {
			t.Fatalf("target %d = %+v, want eligible push for %s in path order", i, target, want)
		}
	}
}

func TestPrepareSyncSerializesLinkedWorktreeFetches(t *testing.T) {
	g := newPreflightGit(2, func(string) string { return "/shared/.git" })
	preparePush(t, g, 4, []string{"/main", "/linked-one", "/linked-two"})
	if g.peak != 1 {
		t.Fatalf("worktrees sharing a common directory fetched concurrently: peak=%d", g.peak)
	}
}
