package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// prJSON is one pull request node as the query returns it.
func prJSON(number int, state, base, head, headRepo, oid, updated string) string {
	headRepository := "null"
	if headRepo != "" {
		headRepository = `{"nameWithOwner":"` + headRepo + `"}`
	}
	merged := "null"
	if state == "MERGED" {
		merged = `"` + updated + `"`
	}
	return fmt.Sprintf(`{"number":%d,"title":"PR %d","url":"https://github.com/acme/widgets/pull/%d","state":%q,"isDraft":false,`+
		`"baseRefName":%q,"headRefName":%q,"headRefOid":%q,"headRepository":%s,"baseRepository":{"nameWithOwner":"acme/widgets"},`+
		`"mergedAt":%s,"updatedAt":%q,"reviewDecision":null,"commits":{"nodes":[]}}`,
		number, number, number, state, base, head, oid, headRepository, merged, updated)
}

const oid1, oid2 = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"

var sameRepoResponse = `{"data":{"repository":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[` +
	prJSON(40, "MERGED", "main", "feat/x", "acme/widgets", oid1, "2026-10-01T10:00:00Z") + `,` +
	`{"number":42,"title":"Add tokens","url":"https://github.com/acme/widgets/pull/42","state":"OPEN","isDraft":true,"baseRefName":"main",` +
	`"headRefName":"feat/x","headRefOid":"` + oid2 + `","headRepository":{"nameWithOwner":"ACME/widgets"},"baseRepository":{"nameWithOwner":"acme/widgets"},` +
	`"mergedAt":null,"updatedAt":"2026-09-30T10:00:00Z","reviewDecision":"CHANGES_REQUESTED","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}},` +
	prJSON(41, "OPEN", "main", "feat/x", "someone/widgets", oid1, "2026-10-02T10:00:00Z") + `,` + // same branch name, another fork
	prJSON(39, "CLOSED", "main", "feat/x", "", oid1, "2026-10-03T10:00:00Z") + // head repository deleted
	`]},"h1":{"nodes":[]},"parent":null}}}`

func TestPullRequestsKeepsOnlyThisRepositorysBranch(t *testing.T) {
	gh, dir := fakeGH(t, sameRepoResponse, 0)
	heads := []string{"feat/x", "@etc/passwd"}
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "acme", "widgets"}, heads)
	if err != nil {
		t.Fatal(err)
	}
	prs := got.Heads["feat/x"]
	if got.Repository != "acme/widgets" || len(prs) != 2 || len(got.Heads["@etc/passwd"]) != 0 {
		t.Fatalf("lookup: %+v", got)
	}
	open, merged := prs[0], prs[1]
	if open.Number != 42 || open.State != "OPEN" || !open.Draft || open.Review != "CHANGES_REQUESTED" || open.Checks != "FAILURE" || !open.IntoDefault || open.HeadOID != oid2 || !open.MergedAt.IsZero() || open.BaseRepository != "acme/widgets" {
		t.Fatalf("open: %+v", open)
	}
	if merged.Number != 40 || merged.State != "MERGED" || merged.MergedAt.IsZero() || merged.Review != "" || merged.Checks != "" {
		t.Fatalf("merged: %+v", merged)
	}
	args := recordedArgs(t, dir)
	if strings.Join(args[:3], " ") != "api graphql --hostname=github.com" || slices.Contains(args, "-F") {
		t.Fatalf("args %q", args)
	}
	query, ok := strings.CutPrefix(args[4], "query=")
	if args[3] != "-f" || !ok {
		t.Fatalf("query argument %q", args[3:5])
	}
	for _, value := range []string{"acme", "widgets", "feat/x", "@etc"} {
		if strings.Contains(query, value) {
			t.Errorf("query text contains %q", value)
		}
	}
	if want := []string{"-f", "owner=acme", "-f", "name=widgets", "-f", "h0=feat/x", "-f", "h1=@etc/passwd"}; !slices.Equal(args[5:], want) {
		t.Fatalf("fields %q, want %q", args[5:], want)
	}
}

func TestPullRequestsFindsForkPullRequestsInTheParent(t *testing.T) {
	seven := prJSON(7, "MERGED", "trunk", "fix", "me/widgets", oid1, "2026-10-04T10:00:00Z")
	response := `{"data":{"repository":{"nameWithOwner":"me/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[]},` +
		`"parent":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"trunk"},"h0":{"nodes":[` + seven + `,` +
		prJSON(8, "MERGED", "release", "fix", "me/widgets", oid1, "2026-10-05T10:00:00Z") + `,` + seven + `]}}}}}`
	gh, _ := fakeGH(t, response, 0)
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "me", "widgets"}, []string{"fix"})
	if err != nil {
		t.Fatal(err)
	}
	prs := got.Heads["fix"]
	if len(prs) != 2 || prs[0].Number != 8 || prs[0].IntoDefault || prs[1].Number != 7 || !prs[1].IntoDefault {
		t.Fatalf("fork pull requests: %+v", prs)
	}
}

func TestPullRequestsKeepsDataDespiteAnInaccessibleParent(t *testing.T) {
	// A fork of a private repository: GitHub answers for the fork, reports an
	// error for its parent, and gh exits non-zero.
	response := `{"data":{"repository":{"nameWithOwner":"me/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[` +
		prJSON(3, "OPEN", "main", "fix", "me/widgets", oid1, "2026-10-04T10:00:00Z") + `]},"parent":null}},` +
		`"errors":[{"type":"FORBIDDEN","message":"Resource not accessible","path":["repository","parent"]}]}`
	gh, _ := fakeGH(t, response, 1)
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "me", "widgets"}, []string{"fix"})
	if err != nil || len(got.Heads["fix"]) != 1 || got.Heads["fix"][0].Number != 3 {
		t.Fatalf("partial answer: %+v, %v", got, err)
	}
}

func TestPullRequestsOrdersOpenFirstAndKeepsFive(t *testing.T) {
	var nodes []string
	for i := 1; i <= 7; i++ {
		nodes = append(nodes, prJSON(i, "CLOSED", "main", "b", "acme/widgets", oid1, time.Date(2026, 10, i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)))
	}
	nodes = append(nodes, prJSON(99, "OPEN", "main", "b", "acme/widgets", oid1, "2020-01-01T00:00:00Z"))
	response := `{"data":{"repository":{"nameWithOwner":"acme/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[` + strings.Join(nodes, ",") + `]},"parent":null}}}`
	gh, _ := fakeGH(t, response, 0)
	got, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), Repo{"github.com", "acme", "widgets"}, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, pr := range got.Heads["b"] {
		numbers = append(numbers, pr.Number)
	}
	if !slices.Equal(numbers, []int{99, 7, 6, 5, 4}) {
		t.Fatalf("order %v", numbers)
	}
}

func TestPullRequestsClassifiesErrors(t *testing.T) {
	repo := Repo{"github.com", "acme", "widgets"}
	for name, tc := range map[string]struct {
		stdout string
		code   int
		want   error
		text   string
	}{
		"not found":    {`{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve"}]}`, 1, ErrRepositoryNotFound, ""},
		"rate limited": {`{"data":{"repository":null},"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`, 1, ErrRateLimited, ""},
		"other":        {`{"data":{"repository":null},"errors":[{"type":"FORBIDDEN","message":"Resource protected\u001b[31m"}]}`, 1, nil, `Resource protected\x1b[31m`},
		"not logged":   {"", 4, ErrNotAuthenticated, ""},
		"garbage":      {"<html>", 1, nil, "gh api: exit status 1: first problem"},
		"empty answer": {`{"data":{}}`, 0, nil, "unexpected gh api output"},
	} {
		gh, _ := fakeGH(t, tc.stdout, tc.code)
		_, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), repo, []string{"b"})
		if tc.want != nil && !errors.Is(err, tc.want) || tc.want == nil && (err == nil || err.Error() != tc.text) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tooMany := make([]string, MaxHeads+1)
	missing := Client{Runner{Executable: "/nonexistent/gh"}}
	if _, err := missing.PullRequests(context.Background(), repo, tooMany); err == nil || errors.Is(err, ErrNotInstalled) {
		t.Fatalf("more than %d heads: %v", MaxHeads, err)
	}
}

func TestPullRequestQueryNamesOnlyVariables(t *testing.T) {
	query := pullRequestQuery(3)
	if strings.Count(query, "pullRequests(") != 6 || !strings.Contains(query, "$h2: String!") || !strings.Contains(query, "h2: pullRequests(headRefName: $h2,") || !strings.Contains(query, "parent {") {
		t.Fatalf("query %s", query)
	}
	if strings.Contains(pullRequestQuery(1), "$h1") {
		t.Fatal("one head declares a second variable")
	}
}

func TestPullRequestsPassesGHSyntaxLookalikesVerbatim(t *testing.T) {
	response := `{"data":{"repository":{"nameWithOwner":"{owner}/widgets","defaultBranchRef":{"name":"main"},"h0":{"nodes":[]},"parent":null}}}`
	gh, dir := fakeGH(t, response, 0)
	repo := Repo{Host: "github.com", Owner: "{owner}", Name: "widgets"}
	if _, err := (Client{Runner{Executable: gh}}).PullRequests(context.Background(), repo, []string{"-x"}); err != nil {
		t.Fatal(err)
	}
	args := recordedArgs(t, dir)
	if slices.Contains(args, "-F") {
		t.Fatalf("args %q use -F", args)
	}
	query, ok := strings.CutPrefix(args[4], "query=")
	if args[3] != "-f" || !ok {
		t.Fatalf("query argument %q", args[3:5])
	}
	for _, value := range []string{"{owner}", "-x"} {
		if strings.Contains(query, value) {
			t.Errorf("query text contains %q", value)
		}
	}
	if want := []string{"-f", "owner={owner}", "-f", "name=widgets", "-f", "h0=-x"}; !slices.Equal(args[5:], want) {
		t.Fatalf("fields %q, want %q", args[5:], want)
	}
}
