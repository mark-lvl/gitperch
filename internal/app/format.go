package app

import (
	"encoding/json"
	"fmt"
	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"io"
	"strings"
	"text/tabwriter"
)

// Report's JSON schema has stable field names and deterministic repository order.
type Report struct {
	SchemaVersion int                 `json:"schema_version"`
	Repositories  []Row               `json:"repositories"`
	Warnings      []discovery.Warning `json:"warnings"`
}

func WriteJSON(w io.Writer, rows []Row, warnings []discovery.Warning) error {
	if rows == nil {
		rows = []Row{}
	}
	if warnings == nil {
		warnings = []discovery.Warning{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Report{1, rows, warnings})
}

func WriteTable(w io.Writer, rows []Row) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "REPOSITORY / PATH\tBRANCH\tCHANGES\tUNTRACKED\tCONFLICTS\tAHEAD*\tBEHIND*\tUPSTREAM\tSTATE"); err != nil {
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
		if s.Upstream == "" {
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
		if _, err := fmt.Fprintf(tw, "%s / %s\t%s\tchanges:%d\t%d\t%d\t%s\t%s\t%s\t%s\n", gitcli.SafeText(row.Name), gitcli.SafeText(row.Path), gitcli.SafeText(branch), s.Changes, s.Untracked, s.Conflicts, ahead, behind, gitcli.SafeText(s.Upstream), gitcli.SafeText(strings.Join(markers, ", "))); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "* Ahead/behind use locally known tracking refs; status does not contact remotes.")
	return err
}
