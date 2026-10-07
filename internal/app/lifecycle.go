package app

import (
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// Lifecycle thresholds. Activity means HEAD last moved in the worktree (a
// commit, checkout, pull or reset, from its reflog), measured at inspection.
// Neither threshold decides anything alone: both apply only to clean
// worktrees with nothing unfinished.
const (
	// RecentActivity: a clean worktree whose HEAD moved this recently is
	// treated as in use, even when its branch looks merged. A worktree just
	// created from the default branch looks merged too.
	RecentActivity = 24 * time.Hour
	// IdleAfter: a clean worktree untouched this long is worth reviewing even
	// though nothing shows its branch was merged.
	IdleAfter = 14 * 24 * time.Hour
)

// WorktreeState is a linked worktree's inferred lifecycle. It suggests what to
// review; it never decides that a worktree may be removed. Clean up decides
// that, against a fresh fetch, and asks for confirmation.
type WorktreeState string

const (
	// WorktreeBlocked: removal must not proceed casually: conflicts, an
	// interrupted operation, an unreadable status or a lock.
	WorktreeBlocked WorktreeState = "blocked"
	// WorktreeInProgress: holds work that exists nowhere else yet:
	// uncommitted or untracked files, unpushed or detached commits, a diverged
	// branch, or no commits at all.
	WorktreeInProgress WorktreeState = "in_progress"
	// WorktreeActive: clean, and HEAD moved within RecentActivity.
	WorktreeActive WorktreeState = "active"
	// WorktreeFinished: clean, nothing unpushed, HEAD merged into the default
	// branch and no HEAD activity within RecentActivity.
	WorktreeFinished WorktreeState = "likely_finished"
	// WorktreeIdle: clean, nothing unpushed, not shown merged and no HEAD
	// activity within IdleAfter.
	WorktreeIdle WorktreeState = "idle"
	// WorktreeUnknown: clean, but the signals point neither way.
	WorktreeUnknown WorktreeState = "unknown"
)

// Label names the state for display.
func (s WorktreeState) Label() string {
	switch s {
	case WorktreeInProgress:
		return "in progress"
	case WorktreeFinished:
		return "likely finished"
	case WorktreeUnknown:
		return "unclear"
	}
	return string(s)
}

// Signal is a stable, machine-readable fact behind a lifecycle state.
type Signal string

const (
	SignalInspectionFailed Signal = "inspection_failed"
	SignalConflicts        Signal = "conflicts"
	SignalOperation        Signal = "operation_in_progress"
	SignalLocked           Signal = "locked"
	SignalUncommitted      Signal = "uncommitted_changes"
	SignalUntracked        Signal = "untracked_files"
	SignalDiverged         Signal = "diverged"
	SignalUnpushed         Signal = "unpushed_commits"
	SignalDetachedCommits  Signal = "detached_commits"
	SignalNoCommits        Signal = "no_commits"
	SignalClean            Signal = "clean"
	SignalNothingToPush    Signal = "nothing_to_push"
	SignalNoUpstream       Signal = "no_upstream"
	SignalMerged           Signal = "merged"
	SignalNotMerged        Signal = "not_merged"
	SignalMergeUnknown     Signal = "merge_unknown"
	SignalRecentActivity   Signal = "recent_activity"
	SignalInactive         Signal = "inactive"
	SignalActivityUnknown  Signal = "activity_unknown"
)

// Lifecycle explains a linked worktree's State with the Signals it was
// inferred from: blockers first, then unfinished work, then the working tree,
// push, merge and activity facts.
type Lifecycle struct {
	State   WorktreeState `json:"state"`
	Reasons []Signal      `json:"reasons"`
}

// Inactivity is how long HEAD had not moved when the row was inspected; false
// when the worktree has no HEAD reflog or was not inspected.
func (r Row) Inactivity() (time.Duration, bool) {
	s := r.Status
	if s.LastActivity.IsZero() || s.InspectedAt.IsZero() {
		return 0, false
	}
	return max(0, s.InspectedAt.Sub(s.LastActivity)), true
}

// Lifecycle infers a linked worktree's lifecycle from the row's current Git
// facts; every refresh recomputes it, nothing is remembered between scans.
// It is nil for rows that are not linked worktrees: the main worktree is never
// a removal candidate, a stale record is pruned rather than reviewed, and a
// standalone repository has no worktrees to compare.
func (r Row) Lifecycle() *Lifecycle {
	w, s := r.Worktree, r.Status
	if w == nil || !w.Linked || w.Bare || w.Prunable {
		return nil
	}
	if s.Error != "" {
		// The other fields are unreliable without a status.
		return &Lifecycle{State: WorktreeBlocked, Reasons: []Signal{SignalInspectionFailed}}
	}
	var reasons []Signal
	if s.Conflicts > 0 {
		reasons = append(reasons, SignalConflicts)
	}
	if s.Operation != "" {
		reasons = append(reasons, SignalOperation)
	}
	if w.Locked {
		reasons = append(reasons, SignalLocked)
	}
	blockers := len(reasons)
	if s.Changes > s.Conflicts {
		reasons = append(reasons, SignalUncommitted)
	}
	if s.Untracked > 0 {
		reasons = append(reasons, SignalUntracked)
	}
	pushed := false
	switch {
	case s.Detached:
		if s.HeadUnreferenced {
			reasons = append(reasons, SignalDetachedCommits)
		}
	case s.Unborn:
		reasons = append(reasons, SignalNoCommits)
	case s.Upstream == "" || !s.ComparisonKnown:
	case s.Ahead > 0 && s.Behind > 0:
		reasons = append(reasons, SignalDiverged)
	case s.Ahead > 0:
		reasons = append(reasons, SignalUnpushed)
	default:
		pushed = true
	}
	unfinished := len(reasons) > blockers
	if !s.Dirty() {
		reasons = append(reasons, SignalClean)
	}
	if pushed {
		reasons = append(reasons, SignalNothingToPush)
	}
	if s.Upstream == "" && !s.Detached && !s.Unborn {
		reasons = append(reasons, SignalNoUpstream)
	}
	merged := false
	switch in := w.Integration; {
	case s.Unborn:
	case in == nil || in.Error != "":
		reasons = append(reasons, SignalMergeUnknown)
	case in.Merged:
		merged = true
		reasons = append(reasons, SignalMerged)
	default:
		reasons = append(reasons, SignalNotMerged)
	}
	idle, known := r.Inactivity()
	switch {
	case !known:
		reasons = append(reasons, SignalActivityUnknown)
	case idle < RecentActivity:
		reasons = append(reasons, SignalRecentActivity)
	default:
		reasons = append(reasons, SignalInactive)
	}
	l := &Lifecycle{Reasons: reasons}
	switch {
	case blockers > 0:
		l.State = WorktreeBlocked
	case unfinished:
		l.State = WorktreeInProgress
	case known && idle < RecentActivity:
		l.State = WorktreeActive
	case known && merged:
		l.State = WorktreeFinished
	case known && idle >= IdleAfter:
		l.State = WorktreeIdle
	default:
		l.State = WorktreeUnknown
	}
	return l
}

// DescribeSignal states signal for this row in a short phrase, such as
// "merged into origin/main" or "no HEAD activity for 6 days".
func (r Row) DescribeSignal(signal Signal) string {
	s := r.Status
	base := "the default branch"
	if in := r.Worktree.integration(); in.Base != "" {
		base = gitcli.SafeText(in.Base)
	}
	switch signal {
	case SignalInspectionFailed:
		return r.Describe(ReasonInspectionFailed)
	case SignalConflicts:
		return r.Describe(ReasonConflicts)
	case SignalOperation:
		return r.Describe(ReasonOperation)
	case SignalUncommitted:
		return r.Describe(ReasonUncommitted)
	case SignalUntracked:
		return r.Describe(ReasonUntracked)
	case SignalDiverged:
		return r.Describe(ReasonDiverged)
	case SignalUnpushed:
		return r.Describe(ReasonUnpushed)
	case SignalDetachedCommits:
		return r.Describe(ReasonDetachedCommits)
	case SignalNoCommits:
		return r.Describe(ReasonNoCommits)
	case SignalNoUpstream:
		return r.Describe(ReasonNoUpstream)
	case SignalLocked:
		if w := r.Worktree; w != nil && w.LockReason != "" {
			return "locked: " + gitcli.SafeText(w.LockReason)
		}
		return "locked"
	case SignalClean:
		return "clean working tree"
	case SignalNothingToPush:
		return "nothing to push to " + gitcli.SafeText(s.Upstream)
	case SignalMerged:
		return "merged into " + base
	case SignalNotMerged:
		return "not merged into " + base
	case SignalMergeUnknown:
		if in := r.Worktree.integration(); in.Error != "" {
			return "merge state unknown: " + gitcli.SafeText(in.Error)
		}
		return "merge state unknown"
	case SignalRecentActivity:
		idle, _ := r.Inactivity()
		return "HEAD moved " + span(idle) + " ago"
	case SignalInactive:
		idle, _ := r.Inactivity()
		return "no HEAD activity for " + span(idle)
	case SignalActivityUnknown:
		return "no HEAD reflog, so activity is unknown"
	}
	return gitcli.SafeText(string(signal))
}

func (w *WorktreeInfo) integration() Integration {
	if w == nil || w.Integration == nil {
		return Integration{}
	}
	return *w.Integration
}

// span states a duration in its largest whole unit, such as "6 days".
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return count(int(d/time.Minute), "minute")
	case d < 24*time.Hour:
		return count(int(d/time.Hour), "hour")
	}
	return count(int(d/(24*time.Hour)), "day")
}
