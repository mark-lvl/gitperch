package git

import (
	"context"
	"repodash/internal/repository"
)

// Service separates local inspection deadlines from network action deadlines.
type Service struct{ Read, Write Runner }

func (s Service) Inspect(ctx context.Context, path string) repository.Status {
	return s.Read.Inspect(ctx, path)
}
func (s Service) Metadata(ctx context.Context, path string) (Metadata, error) {
	return s.Read.Metadata(ctx, path)
}
func (s Service) ResolveFetch(ctx context.Context, path string, m Metadata) (FetchTarget, error) {
	return s.Read.ResolveFetch(ctx, path, m)
}
func (s Service) Fetch(ctx context.Context, path string, target FetchTarget) error {
	return s.Write.Fetch(ctx, path, target)
}
