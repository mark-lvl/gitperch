package app

import (
	"encoding/json"
	"fmt"
	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
)

// Report's JSON schema has stable field names and deterministic repository order.
type Report struct {
	SchemaVersion int                 `json:"schema_version"`
	Repositories  []ReportRow         `json:"repositories"`
	Warnings      []discovery.Warning `json:"warnings"`
}

// ReportRow is a row with the attention and, for a linked worktree, the
// lifecycle derived from its Git facts.
type ReportRow struct {
	Row
	Attention Attention  `json:"attention"`
	Lifecycle *Lifecycle `json:"lifecycle,omitempty"`
}

func WriteJSON(w io.Writer, rows []Row, warnings []discovery.Warning) error {
	report := Report{SchemaVersion: 1, Repositories: make([]ReportRow, len(rows)), Warnings: warnings}
	for i, row := range rows {
		report.Repositories[i] = ReportRow{row, row.Attention(), row.Lifecycle()}
	}
	if report.Warnings == nil {
		report.Warnings = []discovery.Warning{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func WriteTable(w io.Writer, rows []Row) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fromGitHub := false
	if _, err := fmt.Fprintln(tw, "REPOSITORY / PATH\tBRANCH\tCHANGES\tUNTRACKED\tCONFLICTS\tAHEAD*\tBEHIND*\tUPSTREAM\tSTATE\tATTENTION"); err != nil {
		return err
	}
	for _, row := range rows {
		s := row.Status
		branch := s.Branch
		if s.Detached {
			branch = "(detached)"
		}
		if s.Unborn {
			branch += " (unborn)"
		}
		ahead, behind := "?", "?"
		if s.ComparisonKnown {
			ahead = fmt.Sprint(s.Ahead)
			behind = fmt.Sprint(s.Behind)
		}
		attention := row.Attention()
		var markers []string
		if s.Dirty() {
			markers = append(markers, "dirty")
		}
		if s.Conflicts > 0 {
			markers = append(markers, "conflicts")
		}
		if s.Detached {
			markers = append(markers, "detached")
		}
		if w := row.Worktree; w != nil {
			switch {
			case w.Bare:
				markers = append(markers, "bare repository")
			case w.Prunable:
				markers = append(markers, "stale worktree: "+w.PrunableReason)
			case w.Locked:
				markers = append(markers, "locked worktree")
			}
		}
		if l := row.Lifecycle(); l != nil && (l.State == WorktreeFinished || l.State == WorktreeIdle) {
			markers = append(markers, l.State.Label()+" worktree")
		}
		// The same rule as the attention column: detached, unborn, failed,
		// stale and bare rows have no branch whose upstream could be missing.
		if slices.Contains(attention.Reasons, ReasonNoUpstream) {
			markers = append(markers, "no upstream")
		}
		if s.Synchronized() {
			markers = append(markers, "synchronized (local refs)")
		}
		if s.Error != "" {
			markers = append(markers, "error: "+s.Error)
		}
		if s.Operation != "" {
			markers = append(markers, "operation: "+s.Operation)
		}
		markers = append(markers, githubMarkers(row)...)
		fromGitHub = fromGitHub || row.GitHub != nil
		if _, err := fmt.Fprintf(tw, "%s / %s\t%s\tchanges:%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", gitcli.SafeText(row.Name), gitcli.SafeText(row.Path), gitcli.SafeText(branch), s.Changes, s.Untracked, s.Conflicts, ahead, behind, gitcli.SafeText(s.Upstream), gitcli.SafeText(strings.Join(markers, ", ")), attention.Level); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "* Ahead/behind use locally known tracking refs; status does not contact remotes."); err != nil || !fromGitHub {
		return err
	}
	_, err := fmt.Fprintln(w, "* Pull request data comes from GitHub through gh.")
	return err
}

// githubMarkers describe a row's pull request state for the STATE column.
func githubMarkers(row Row) []string {
	if row.GitHub == nil {
		return nil
	}
	var markers []string
	if pr := row.CurrentPullRequest(); pr != nil {
		state := pr.State
		if pr.Draft && state == PullRequestOpen {
			state = "draft"
		}
		markers = append(markers, fmt.Sprintf("pr #%d %s", pr.Number, state))
		if pr.State == PullRequestOpen && pr.Checks == ChecksFailing {
			markers = append(markers, "checks failing")
		}
		if pr.State == PullRequestOpen && pr.Review == ReviewChangesRequested {
			markers = append(markers, "changes requested")
		}
	}
	if row.GitHub.Error != "" {
		markers = append(markers, "github: "+row.GitHub.Error)
	}
	return markers
}
