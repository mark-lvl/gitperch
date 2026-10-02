package repository

import "time"

// Status stores independent dimensions; dirty and diverged may both be true.
type Status struct {
	Branch          string    `json:"branch"`
	HeadOID         string    `json:"head_oid"`
	Upstream        string    `json:"upstream"`
	ComparisonKnown bool      `json:"comparison_known"`
	Ahead           int       `json:"ahead"`
	Behind          int       `json:"behind"`
	Changes         int       `json:"changes"`
	Untracked       int       `json:"untracked"`
	Conflicts       int       `json:"conflicts"`
	Detached        bool      `json:"detached"`
	Unborn          bool      `json:"unborn"`
	InspectedAt     time.Time `json:"inspected_at"`
	Error           string    `json:"error,omitempty"`
}

func (s Status) Dirty() bool { return s.Changes > 0 || s.Untracked > 0 || s.Conflicts > 0 }
func (s Status) Synchronized() bool {
	return s.Error == "" && !s.Dirty() && s.ComparisonKnown && s.Ahead == 0 && s.Behind == 0
}
