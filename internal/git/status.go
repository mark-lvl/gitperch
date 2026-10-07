package git

import (
	"context"
	"github.com/mark-lvl/gitperch/internal/repository"
	"time"
)

func (r Runner) Inspect(ctx context.Context, path string) repository.Status {
	out, err := r.Run(ctx, path, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all", "--ignore-submodules=none", "--ahead-behind")
	var s repository.Status
	if err == nil {
		s, err = ParseStatus(out.Stdout)
	}
	s.InspectedAt = time.Now().UTC()
	if err != nil {
		s.Error = SafeText(err.Error())
	} else {
		s.CommonDir, s.Operation, s.LastActivity, err = r.localMetadata(ctx, path)
		if err != nil {
			s.Error = SafeText(err.Error())
		} else if s.Detached {
			// Commits only a detached HEAD reaches are lost once the worktree
			// moves on. A failed check leaves the flag unset rather than
			// failing the whole inspection.
			if reachable, err := r.ReachableFromRefs(ctx, path, s.HeadOID); err == nil {
				s.HeadUnreferenced = !reachable
			}
		}
	}
	return s
}
