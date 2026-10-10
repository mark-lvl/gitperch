package app

import (
	"slices"
	"strings"
)

// CleanupStatus says whether a reviewed cleanup item may run. Only Allowed
// items can be ticked; the others stay listed with their reasons.
type CleanupStatus string

const (
	// CleanupAllowed: every check passed. The item starts ticked.
	CleanupAllowed CleanupStatus = "allowed"
	// CleanupNeedsReview: nothing at risk was found, but Git cannot show the
	// work is finished, such as a branch not merged into the default branch
	// (squash and rebase merges look like this). gitperch never runs it.
	CleanupNeedsReview CleanupStatus = "needs_review"
	// CleanupUnknown: a check could not be made, so safety is not
	// established.
	CleanupUnknown CleanupStatus = "unknown"
	// CleanupBlocked: running it would lose work or disturb Git, or the state
	// changed since review.
	CleanupBlocked CleanupStatus = "blocked"
)

// statusRank orders statuses from permissive to restrictive; an assessment
// takes the most restrictive status among its reasons.
var statusRank = map[CleanupStatus]int{CleanupAllowed: 1, CleanupNeedsReview: 2, CleanupUnknown: 3, CleanupBlocked: 4}

// Label names the status for display.
func (s CleanupStatus) Label() string {
	switch s {
	case CleanupNeedsReview:
		return "review"
	case "":
		return "unchecked"
	}
	return string(s)
}

// CleanupCode is a stable, machine-readable fact behind a cleanup decision.
type CleanupCode string

const (
	// Facts that allow an item.
	CleanupClean            CleanupCode = "clean"
	CleanupNoIgnoredFiles   CleanupCode = "no_ignored_files"
	CleanupNoHiddenChanges  CleanupCode = "no_hidden_changes"
	CleanupMerged           CleanupCode = "merged"
	CleanupDirectoryMissing CleanupCode = "directory_missing"
	// CleanupMergedPullRequest: GitHub merged a pull request into the default
	// branch whose head is exactly the item's commit; see mergeVerdict.
	CleanupMergedPullRequest CleanupCode = "merged_pull_request"

	// Facts that need the user's own review.
	CleanupNotMerged    CleanupCode = "not_merged"
	CleanupUpstreamGone CleanupCode = "upstream_gone"
	CleanupNoUpstream   CleanupCode = "no_upstream"
	// CleanupPullRequestUnproven: the branch's pull request does not prove it
	// merged, such as one still open or merged at an older commit.
	CleanupPullRequestUnproven CleanupCode = "pull_request_unproven"
	// CleanupGitHubCheckFailed: gh could not answer; Git alone decides.
	CleanupGitHubCheckFailed CleanupCode = "github_check_failed"

	// Checks that could not be made.
	CleanupInspectionFailed     CleanupCode = "inspection_failed"
	CleanupCheckFailed          CleanupCode = "check_failed"
	CleanupFetchFailed          CleanupCode = "fetch_failed"
	CleanupDefaultBranchUnknown CleanupCode = "default_branch_unknown"

	// Facts that block an item.
	CleanupMainWorktree        CleanupCode = "main_worktree"
	CleanupLocked              CleanupCode = "locked"
	CleanupOperation           CleanupCode = "operation_in_progress"
	CleanupConflicts           CleanupCode = "conflicts"
	CleanupUncommitted         CleanupCode = "uncommitted_changes"
	CleanupUntracked           CleanupCode = "untracked_files"
	CleanupIgnoredFiles        CleanupCode = "ignored_files"
	CleanupHiddenChanges       CleanupCode = "hidden_changes"
	CleanupNoCommits           CleanupCode = "no_commits"
	CleanupUnpushed            CleanupCode = "unpushed_commits"
	CleanupDiverged            CleanupCode = "diverged"
	CleanupUnreferencedCommit  CleanupCode = "unreferenced_commit"
	CleanupCheckedOutElsewhere CleanupCode = "checked_out_elsewhere"
	CleanupChangedSinceReview  CleanupCode = "changed_since_review"
)

// Status is the status the code alone warrants. A locked worktree whose
// directory is missing stays blocked through its lock.
func (c CleanupCode) Status() CleanupStatus {
	switch c {
	case CleanupClean, CleanupNoIgnoredFiles, CleanupNoHiddenChanges, CleanupMerged, CleanupDirectoryMissing, CleanupMergedPullRequest:
		return CleanupAllowed
	case CleanupNotMerged, CleanupUpstreamGone, CleanupNoUpstream, CleanupPullRequestUnproven, CleanupGitHubCheckFailed:
		return CleanupNeedsReview
	case CleanupInspectionFailed, CleanupCheckFailed, CleanupFetchFailed, CleanupDefaultBranchUnknown:
		return CleanupUnknown
	}
	// Unknown codes never allow anything.
	return CleanupBlocked
}

// CleanupReason is one fact behind an assessment. Code decides; Text states
// it for this item in a short phrase already made safe for display, such as
// "3 uncommitted files" or "merged into origin/main".
type CleanupReason struct {
	Code CleanupCode `json:"code"`
	Text string      `json:"text"`
}

// CleanupAssessment explains whether an item may run. Status is the most
// restrictive status among Reasons, in the order the checks ran. An assessment
// without reasons has no status and allows nothing.
type CleanupAssessment struct {
	Status  CleanupStatus   `json:"status"`
	Reasons []CleanupReason `json:"reasons"`
}

// Assess builds an assessment from reasons.
func Assess(reasons ...CleanupReason) CleanupAssessment {
	var a CleanupAssessment
	for _, r := range reasons {
		a.add(r.Code, r.Text)
	}
	return a
}

func (a *CleanupAssessment) add(code CleanupCode, text string) {
	a.Reasons = append(a.Reasons, CleanupReason{Code: code, Text: text})
	if s := code.Status(); statusRank[s] > statusRank[a.Status] {
		a.Status = s
	}
}

// Allowed reports whether the item may run.
func (a CleanupAssessment) Allowed() bool { return a.Status == CleanupAllowed }

// Has reports whether code is among the reasons.
func (a CleanupAssessment) Has(code CleanupCode) bool {
	return slices.ContainsFunc(a.Reasons, func(r CleanupReason) bool { return r.Code == code })
}

// Concerns are the reasons that keep the item from running.
func (a CleanupAssessment) Concerns() []CleanupReason {
	var concerns []CleanupReason
	for _, r := range a.Reasons {
		if r.Code.Status() != CleanupAllowed {
			concerns = append(concerns, r)
		}
	}
	return concerns
}

// Summary states the decision in one line: for an allowed item the deciding
// fact, which is checked last; otherwise every concern.
func (a CleanupAssessment) Summary() string {
	if len(a.Reasons) == 0 {
		return "not checked"
	}
	if a.Allowed() {
		return a.Reasons[len(a.Reasons)-1].Text
	}
	var texts []string
	for _, r := range a.Concerns() {
		texts = append(texts, r.Text)
	}
	return strings.Join(texts, "; ")
}
