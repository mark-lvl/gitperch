package git

import (
	"context"
	"repodash/internal/repository"
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
	}
	return s
}
