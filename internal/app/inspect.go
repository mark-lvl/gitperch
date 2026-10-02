// Package app coordinates discovery, inspection, and later Git actions.
package app

import (
	"context"
	"repodash/internal/repository"
)

type GitService interface {
	Inspect(context.Context, string) repository.Status
}

type Row struct {
	repository.Repository
	Status repository.Status `json:"status"`
}

// Inspect maintains discovery ordering and isolates per-repository failures.
func Inspect(ctx context.Context, repos []repository.Repository, service GitService) ([]Row, error) {
	rows := make([]Row, 0, len(repos))
	for _, repo := range repos {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		rows = append(rows, Row{Repository: repo, Status: service.Inspect(ctx, repo.Path)})
	}
	return rows, ctx.Err()
}
