package git

import (
	"context"
	"github.com/markkaghazgarian/gitperch/internal/repository"
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

func (s Service) ResolveUpstream(ctx context.Context, path string, m Metadata) (UpstreamTarget, error) {
	return s.Read.ResolveUpstream(ctx, path, m)
}
func (s Service) ResolvePush(ctx context.Context, path string, m Metadata, upstream UpstreamTarget) (PushTarget, error) {
	return s.Read.ResolvePush(ctx, path, m, upstream)
}
func (s Service) UpstreamCommit(ctx context.Context, path string, upstream UpstreamTarget) (string, error) {
	return s.Read.UpstreamCommit(ctx, path, upstream)
}
func (s Service) Push(ctx context.Context, path string, target PushTarget) error {
	return s.Write.Push(ctx, path, target)
}
func (s Service) FastForward(ctx context.Context, path, commit string) error {
	return s.Write.FastForward(ctx, path, commit)
}
