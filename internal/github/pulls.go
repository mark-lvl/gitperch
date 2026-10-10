package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// MaxHeads is the most branch names one PullRequests query asks about.
const MaxHeads = 20

// maxPerHead is how many pull requests are kept for one branch.
const maxPerHead = 5

var (
	// ErrRepositoryNotFound reports a repository GitHub does not show to gh's
	// login, because it does not exist or the login lacks access.
	ErrRepositoryNotFound = errors.New("repository not found or not accessible")
	// ErrRateLimited reports an exhausted GitHub API rate limit.
	ErrRateLimited = errors.New("GitHub API rate limit reached")
)

// PullRequest is one pull request as the GraphQL API reports it. State,
// Review and Checks keep GitHub's enum values.
type PullRequest struct {
	Number         int
	Title, URL     string
	State          string // OPEN, MERGED or CLOSED
	Draft          bool
	BaseRepository string // owner/name
	BaseRef        string
	HeadRef        string
	HeadOID        string
	IntoDefault    bool      // BaseRef is the base repository's default branch
	Review         string    // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	Checks         string    // the head commit's rollup: SUCCESS, FAILURE, ERROR, PENDING, EXPECTED or ""
	MergedAt       time.Time // zero unless merged
	UpdatedAt      time.Time
}

// Lookup answers one repository: its name as GitHub reports it, and for each
// asked-for branch the pull requests whose head is that branch of that
// repository, open ones first, then the most recently updated.
type Lookup struct {
	Repository string
	Heads      map[string][]PullRequest
}

// Client asks GitHub through gh.
type Client struct{ Runner Runner }

// PullRequests asks once for the pull requests of up to MaxHeads branches of
// repo, including those in repo's parent, where a fork's pull requests live.
func (c Client) PullRequests(ctx context.Context, repo Repo, heads []string) (Lookup, error) {
	if len(heads) == 0 || len(heads) > MaxHeads {
		return Lookup{}, fmt.Errorf("pull request lookups take 1 to %d branches, not %d", MaxHeads, len(heads))
	}
	// Values travel only in -f raw fields. -F would read a file for @value and
	// expand {owner}-style placeholders, both of which a branch name can hold.
	args := []string{"api", "graphql", "--hostname=" + repo.Host, "-f", "query=" + pullRequestQuery(len(heads)), "-f", "owner=" + repo.Owner, "-f", "name=" + repo.Name}
	for i, head := range heads {
		args = append(args, "-f", fmt.Sprintf("h%d=%s", i, head))
	}
	out, runErr := c.Runner.Run(ctx, args...)
	var response struct {
		Data struct {
			Repository *rawRepository `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	// gh prints GraphQL error bodies on stdout and still exits non-zero.
	if err := json.Unmarshal(out.Stdout, &response); err != nil || response.Data.Repository == nil && len(response.Errors) == 0 {
		if runErr != nil {
			return Lookup{}, runErr
		}
		return Lookup{}, errors.New("unexpected gh api output")
	}
	if response.Data.Repository == nil {
		for _, e := range response.Errors {
			switch e.Type {
			case "NOT_FOUND":
				return Lookup{}, ErrRepositoryNotFound
			case "RATE_LIMITED":
				return Lookup{}, ErrRateLimited
			}
		}
		return Lookup{}, errors.New(gitcli.SafeText(response.Errors[0].Message))
	}
	return collect(*response.Data.Repository, heads), nil
}

// pullRequestFields are read for every pull request; see PullRequest.
const pullRequestFields = "number title url state isDraft baseRefName headRefName headRefOid" +
	" headRepository { nameWithOwner } baseRepository { nameWithOwner } mergedAt updatedAt reviewDecision" +
	" commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }"

// pullRequestQuery asks for n branches' pull requests on a repository and on
// its parent. The text depends on n alone: owner, name and branch names are
// variables and never part of the query.
func pullRequestQuery(n int) string {
	var vars, heads strings.Builder
	for i := range n {
		fmt.Fprintf(&vars, ", $h%d: String!", i)
		fmt.Fprintf(&heads, " h%d: pullRequests(headRefName: $h%d, first: 10, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { ...pr } }", i, i)
	}
	return "query($owner: String!, $name: String!" + vars.String() + ") {" +
		" repository(owner: $owner, name: $name) { nameWithOwner defaultBranchRef { name }" + heads.String() +
		" parent { nameWithOwner defaultBranchRef { name }" + heads.String() + " } } }" +
		" fragment pr on PullRequest { " + pullRequestFields + " }"
}

type rawPullRequest struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	BaseRefName    string `json:"baseRefName"`
	HeadRefName    string `json:"headRefName"`
	HeadRefOid     string `json:"headRefOid"`
	HeadRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	BaseRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"baseRepository"`
	MergedAt       *time.Time `json:"mergedAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	ReviewDecision *string    `json:"reviewDecision"`
	Commits        struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// rawRepository is a repository object of the query, whose pull request
// connections are aliased h0…hN.
type rawRepository struct {
	NameWithOwner string
	DefaultBranch string
	Parent        *rawRepository
	Heads         map[string][]rawPullRequest // by alias
}

func (r *rawRepository) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	r.Heads = map[string][]rawPullRequest{}
	for key, value := range fields {
		var err error
		switch {
		case key == "nameWithOwner":
			err = json.Unmarshal(value, &r.NameWithOwner)
		case key == "defaultBranchRef":
			var ref *struct {
				Name string `json:"name"`
			}
			if err = json.Unmarshal(value, &ref); err == nil && ref != nil {
				r.DefaultBranch = ref.Name
			}
		case key == "parent":
			err = json.Unmarshal(value, &r.Parent)
		case strings.HasPrefix(key, "h"):
			var connection *struct {
				Nodes []rawPullRequest `json:"nodes"`
			}
			if err = json.Unmarshal(value, &connection); err == nil && connection != nil {
				r.Heads[key] = connection.Nodes
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// collect keeps, per head, the pull requests whose head is that branch of the
// queried repository: the same branch name in someone else's fork never
// counts, nor does a pull request whose head repository was deleted.
func collect(repo rawRepository, heads []string) Lookup {
	lookup := Lookup{Repository: repo.NameWithOwner, Heads: map[string][]PullRequest{}}
	for i, head := range heads {
		alias := fmt.Sprintf("h%d", i)
		var prs []PullRequest
		seen := map[string]bool{}
		add := func(nodes []rawPullRequest, defaultBranch string) {
			for _, n := range nodes {
				if n.HeadRefName != head || n.HeadRepository == nil || !strings.EqualFold(n.HeadRepository.NameWithOwner, repo.NameWithOwner) {
					continue
				}
				pr := PullRequest{Number: n.Number, Title: n.Title, URL: n.URL, State: n.State, Draft: n.IsDraft, BaseRef: n.BaseRefName, HeadRef: n.HeadRefName,
					HeadOID: n.HeadRefOid, IntoDefault: defaultBranch != "" && n.BaseRefName == defaultBranch, UpdatedAt: n.UpdatedAt}
				if n.BaseRepository != nil {
					pr.BaseRepository = n.BaseRepository.NameWithOwner
				}
				key := pr.BaseRepository + "#" + strconv.Itoa(pr.Number)
				if seen[key] {
					continue
				}
				seen[key] = true
				if n.MergedAt != nil {
					pr.MergedAt = *n.MergedAt
				}
				if n.ReviewDecision != nil {
					pr.Review = *n.ReviewDecision
				}
				if commits := n.Commits.Nodes; len(commits) > 0 && commits[0].Commit.StatusCheckRollup != nil {
					pr.Checks = commits[0].Commit.StatusCheckRollup.State
				}
				prs = append(prs, pr)
			}
		}
		add(repo.Heads[alias], repo.DefaultBranch)
		if repo.Parent != nil {
			add(repo.Parent.Heads[alias], repo.Parent.DefaultBranch)
		}
		sort.SliceStable(prs, func(a, b int) bool {
			if open := prs[a].State == "OPEN"; open != (prs[b].State == "OPEN") {
				return open
			}
			if !prs[a].UpdatedAt.Equal(prs[b].UpdatedAt) {
				return prs[a].UpdatedAt.After(prs[b].UpdatedAt)
			}
			return prs[a].Number > prs[b].Number
		})
		lookup.Heads[head] = prs[:min(len(prs), maxPerHead)]
	}
	return lookup
}
