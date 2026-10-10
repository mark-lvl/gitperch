package app

import (
	"fmt"
	"slices"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// Level orders how much a row's Git state asks of the user. Every level
// above Low counts as needing attention.
type Level int

const (
	// Low: clean and synchronized, or unusual but valid, such as a detached
	// HEAD that a branch or tag also reaches, a locked worktree or a bare
	// repository.
	Low Level = iota
	// Medium: worth reviewing, nothing at risk yet: behind the upstream, no
	// upstream, unknown tracking, no commits, a stale worktree record.
	Medium
	// High: unfinished local work that exists nowhere else: uncommitted or
	// untracked files, unpushed commits, a diverged branch, commits only a
	// detached HEAD reaches.
	High
	// Critical: Git needs intervention before anything else: conflicts, an
	// interrupted operation, a status that could not be read, a failed action.
	Critical
)

var levelNames = []string{"low", "medium", "high", "critical"}

func (l Level) String() string {
	if l < Low || l > Critical {
		return fmt.Sprintf("level(%d)", int(l))
	}
	return levelNames[l]
}

func (l Level) MarshalText() ([]byte, error) {
	if l < Low || l > Critical {
		return nil, fmt.Errorf("invalid attention level %d", int(l))
	}
	return []byte(l.String()), nil
}

func (l *Level) UnmarshalText(text []byte) error {
	i := slices.Index(levelNames, string(text))
	if i < 0 {
		return fmt.Errorf("invalid attention level %q", text)
	}
	*l = Level(i)
	return nil
}

// Reason is a stable, machine-readable cause of attention.
type Reason string

const (
	ReasonInspectionFailed Reason = "inspection_failed"
	ReasonConflicts        Reason = "conflicts"
	ReasonOperation        Reason = "operation_in_progress"
	// ReasonActionFailed marks a fetch, push or pull in this session that
	// failed or whose outcome is unknown. Status inspection never reports it.
	ReasonActionFailed    Reason = "action_failed"
	ReasonUncommitted     Reason = "uncommitted_changes"
	ReasonUntracked       Reason = "untracked_files"
	ReasonDiverged        Reason = "diverged"
	ReasonUnpushed        Reason = "unpushed_commits"
	ReasonDetachedCommits Reason = "detached_commits"
	ReasonBehind          Reason = "behind_upstream"
	ReasonNoUpstream      Reason = "no_upstream"
	ReasonTrackingUnknown Reason = "tracking_unknown"
	ReasonNoCommits       Reason = "no_commits"
	ReasonStaleWorktree   Reason = "stale_worktree"
	// ReasonWorktreeFinished and ReasonWorktreeIdle come from a linked
	// worktree's Lifecycle: worth a review, nothing at risk.
	ReasonWorktreeFinished Reason = "worktree_finished"
	ReasonWorktreeIdle     Reason = "worktree_idle"
	// ReasonPRChecksFailing and ReasonPRChangesRequested come from the
	// branch's open pull request on GitHub (Row.GitHub): someone has to act.
	ReasonPRChecksFailing    Reason = "pr_checks_failing"
	ReasonPRChangesRequested Reason = "pr_changes_requested"
)

// reasonOrder lists reasons from most to least severe; Attention.Reasons
// follows it.
var reasonOrder = []Reason{
	ReasonActionFailed, ReasonInspectionFailed, ReasonConflicts, ReasonOperation,
	ReasonDiverged, ReasonUncommitted, ReasonUntracked, ReasonUnpushed, ReasonDetachedCommits,
	ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree,
	ReasonWorktreeFinished, ReasonWorktreeIdle,
}

// Level is the attention level the reason alone warrants.
func (r Reason) Level() Level {
	switch r {
	case ReasonActionFailed, ReasonInspectionFailed, ReasonConflicts, ReasonOperation:
		return Critical
	case ReasonDiverged, ReasonUncommitted, ReasonUntracked, ReasonUnpushed, ReasonDetachedCommits:
		return High
	case ReasonBehind, ReasonPRChecksFailing, ReasonPRChangesRequested, ReasonTrackingUnknown, ReasonNoUpstream, ReasonNoCommits, ReasonStaleWorktree, ReasonWorktreeFinished, ReasonWorktreeIdle:
		return Medium
	}
	return Low
}

// Attention explains why a row needs the user: Level is the most severe of
// its Reasons, which are ordered most severe first. Low has no reasons.
type Attention struct {
	Level   Level    `json:"level"`
	Reasons []Reason `json:"reasons"`
}

// Needs reports whether the row needs attention at all.
func (a Attention) Needs() bool { return a.Level > Low }

// With adds reason, keeping Reasons ordered and Level the most severe.
func (a Attention) With(reason Reason) Attention {
	if slices.Contains(a.Reasons, reason) {
		return a
	}
	a.Reasons = append(slices.Clone(a.Reasons), reason)
	slices.SortStableFunc(a.Reasons, func(x, y Reason) int {
		return slices.Index(reasonOrder, x) - slices.Index(reasonOrder, y)
	})
	a.Level = max(a.Level, reason.Level())
	return a
}

// Attention classifies the row from its own worktree's Git state. Every
// condition here is per worktree in Git (HEAD, index, working tree, operation
// markers, the checked-out branch), so linked worktrees of one repository are
// classified independently and nothing is counted twice.
func (r Row) Attention() Attention {
	a := Attention{Reasons: []Reason{}}
	if w := r.Worktree; w != nil {
		if w.Bare {
			return a
		}
		if w.Prunable {
			return a.With(ReasonStaleWorktree)
		}
	}
	s := r.Status
	if s.Error != "" {
		// The other fields are unreliable without a status.
		return a.With(ReasonInspectionFailed)
	}
	if s.Conflicts > 0 {
		a = a.With(ReasonConflicts)
	}
	if s.Operation != "" {
		a = a.With(ReasonOperation)
	}
	if s.Changes > s.Conflicts {
		a = a.With(ReasonUncommitted)
	}
	if s.Untracked > 0 {
		a = a.With(ReasonUntracked)
	}
	switch {
	case s.Detached:
		if s.HeadUnreferenced {
			a = a.With(ReasonDetachedCommits)
		}
	case s.Unborn:
		a = a.With(ReasonNoCommits)
	case s.Upstream == "":
		a = a.With(ReasonNoUpstream)
	case !s.ComparisonKnown:
		a = a.With(ReasonTrackingUnknown)
	case s.Ahead > 0 && s.Behind > 0:
		a = a.With(ReasonDiverged)
	case s.Ahead > 0:
		a = a.With(ReasonUnpushed)
	case s.Behind > 0:
		a = a.With(ReasonBehind)
	}
	if pr := r.CurrentPullRequest(); pr != nil && pr.State == PullRequestOpen {
		if pr.Checks == ChecksFailing {
			a = a.With(ReasonPRChecksFailing)
		}
		if pr.Review == ReviewChangesRequested {
			a = a.With(ReasonPRChangesRequested)
		}
	}
	// Lifecycle reads the same facts, never attention, so the two cannot loop.
	if l := r.Lifecycle(); l != nil {
		switch l.State {
		case WorktreeFinished:
			a = a.With(ReasonWorktreeFinished)
		case WorktreeIdle:
			a = a.With(ReasonWorktreeIdle)
		}
	}
	return a
}

// Describe states reason for this row in a short phrase, such as
// "3 uncommitted files" or "2 commits ahead of origin/main".
func (r Row) Describe(reason Reason) string {
	s := r.Status
	upstream := gitcli.SafeText(s.Upstream)
	switch reason {
	case ReasonInspectionFailed:
		return "Git status could not be read"
	case ReasonActionFailed:
		return "last fetch, push or pull failed or has an unknown outcome"
	case ReasonConflicts:
		return count(s.Conflicts, "conflicted file")
	case ReasonOperation:
		return operationName(s.Operation) + " in progress"
	case ReasonUncommitted:
		return count(s.Changes-s.Conflicts, "uncommitted file")
	case ReasonUntracked:
		return count(s.Untracked, "untracked file")
	case ReasonDiverged:
		return fmt.Sprintf("diverged from %s: %d ahead, %d behind", upstream, s.Ahead, s.Behind)
	case ReasonUnpushed:
		return count(s.Ahead, "commit") + " ahead of " + upstream
	case ReasonDetachedCommits:
		return "detached HEAD commit is on no branch or tag"
	case ReasonBehind:
		return count(s.Behind, "commit") + " behind " + upstream
	case ReasonTrackingUnknown:
		return "no local tracking ref for " + upstream
	case ReasonNoUpstream:
		return "branch has no upstream"
	case ReasonNoCommits:
		return "no commits yet"
	case ReasonStaleWorktree:
		if w := r.Worktree; w != nil && w.PrunableReason != "" {
			return "stale worktree record: " + gitcli.SafeText(w.PrunableReason)
		}
		return "stale worktree record"
	case ReasonWorktreeFinished:
		idle, _ := r.Inactivity()
		return "linked worktree looks finished: " + r.DescribeSignal(SignalMerged) + ", no HEAD activity for " + span(idle)
	case ReasonWorktreeIdle:
		idle, _ := r.Inactivity()
		return "linked worktree idle: no HEAD activity for " + span(idle)
	case ReasonPRChecksFailing:
		return pullRequestName(r.CurrentPullRequest()) + " checks failing"
	case ReasonPRChangesRequested:
		return pullRequestName(r.CurrentPullRequest()) + ": changes requested"
	}
	return gitcli.SafeText(string(reason))
}

// pullRequestName is "PR #42", or "the pull request" without one.
func pullRequestName(pr *PullRequest) string {
	if pr == nil {
		return "the pull request"
	}
	return fmt.Sprintf("PR #%d", pr.Number)
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// operationName names the Git operation an unfinished-operation marker
// belongs to; see git.Runner.Operation.
func operationName(marker string) string {
	switch marker {
	case "MERGE_HEAD":
		return "merge"
	case "CHERRY_PICK_HEAD":
		return "cherry-pick"
	case "REVERT_HEAD":
		return "revert"
	case "rebase-merge", "rebase-apply":
		return "rebase"
	case "sequencer":
		return "cherry-pick or revert sequence"
	case "BISECT_LOG":
		return "bisect"
	}
	return gitcli.SafeText(marker)
}
