package repository

import "time"

// Status stores independent dimensions; dirty and diverged may both be true.
type Status struct {
	Branch          string `json:"branch"`
	HeadOID         string `json:"head_oid"`
	Upstream        string `json:"upstream"`
	ComparisonKnown bool   `json:"comparison_known"`
	Ahead           int    `json:"ahead"`
	Behind          int    `json:"behind"`
	Changes         int    `json:"changes"`
	Untracked       int    `json:"untracked"`
	Conflicts       int    `json:"conflicts"`
	Detached        bool   `json:"detached"`
	// HeadUnreferenced reports a detached HEAD whose commit no branch,
	// remote-tracking branch or tag contains. False when the check failed.
	HeadUnreferenced bool      `json:"head_unreferenced,omitempty"`
	Unborn           bool      `json:"unborn"`
	InspectedAt      time.Time `json:"inspected_at"`
	Error            string    `json:"error,omitempty"`
	CommonDir        string    `json:"common_git_dir,omitempty"`
	Operation        string    `json:"operation,omitempty"`
	// LastActivity is the HEAD reflog's modification time: the last commit,
	// checkout, pull or reset in this worktree. Zero when the reflog is absent.
	LastActivity time.Time `json:"last_activity,omitzero"`
}

func (s Status) Dirty() bool { return s.Changes > 0 || s.Untracked > 0 || s.Conflicts > 0 }
func (s Status) Synchronized() bool {
	return s.Error == "" && s.Operation == "" && !s.Dirty() && s.ComparisonKnown && s.Ahead == 0 && s.Behind == 0
}
