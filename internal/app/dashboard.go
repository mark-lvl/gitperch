package app

import (
	"context"
	"github.com/mark-lvl/gitperch/internal/discovery"
)

type Snapshot struct {
	Rows     []Row
	Warnings []discovery.Warning
}

func Load(ctx context.Context, opts discovery.Options, service GitService, workers int) (Snapshot, error) {
	result, err := discovery.Scan(ctx, opts)
	s := Snapshot{Warnings: result.Warnings}
	if err != nil {
		return s, err
	}
	s.Rows, err = Inspect(ctx, result.Repositories, service, workers)
	if err != nil {
		return s, err
	}
	var warnings []discovery.Warning
	s.Rows, warnings = attachWorktrees(ctx, s.Rows, service, workers, opts.Roots)
	s.Warnings = append(s.Warnings, warnings...)
	return s, ctx.Err()
}
