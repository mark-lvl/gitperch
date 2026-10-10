package git

import (
	"context"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// Service separates local inspection deadlines from network action deadlines.
type Service struct{ Read, Write Runner }

func (s Service) Inspect(ctx context.Context, path string) repository.Status {
	return s.Read.Inspect(ctx, path)
}
func (s Service) Metadata(ctx context.Context, path string) (Metadata, error) {
	return s.Read.Metadata(ctx, path)
}
func (s Service) Remotes(ctx context.Context, path string) ([]string, error) {
	return s.Read.Remotes(ctx, path)
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
func (s Service) SupportsWorktreeInventory(ctx context.Context) bool {
	return s.Read.SupportsWorktreeInventory(ctx)
}
func (s Service) Worktrees(ctx context.Context, path string) ([]Worktree, error) {
	return s.Read.Worktrees(ctx, path)
}
func (s Service) Operation(ctx context.Context, path string) (string, error) {
	return s.Read.Operation(ctx, path)
}
func (s Service) HiddenChanges(ctx context.Context, path string) ([]string, error) {
	return s.Read.HiddenChanges(ctx, path)
}
func (s Service) ResolveRemote(ctx context.Context, path string, m Metadata, remote string) (FetchTarget, error) {
	return s.Read.ResolveRemote(ctx, path, m, remote)
}
func (s Service) RemoteDefaultRef(ctx context.Context, path, remote string) (string, error) {
	return s.Read.RemoteDefaultRef(ctx, path, remote)
}
func (s Service) ResolveCommit(ctx context.Context, path, ref string) (string, error) {
	return s.Read.ResolveCommit(ctx, path, ref)
}
func (s Service) IsAncestor(ctx context.Context, path, oid, ref string) (bool, error) {
	return s.Read.IsAncestor(ctx, path, oid, ref)
}
func (s Service) ReachableFromRefs(ctx context.Context, path, oid string) (bool, error) {
	return s.Read.ReachableFromRefs(ctx, path, oid)
}
func (s Service) IgnoredFiles(ctx context.Context, path string) ([]string, error) {
	return s.Read.IgnoredFiles(ctx, path)
}
func (s Service) PruneWorktrees(ctx context.Context, path string) error {
	return s.Write.PruneWorktrees(ctx, path)
}
func (s Service) RemoveWorktree(ctx context.Context, path, worktree string) error {
	return s.Write.RemoveWorktree(ctx, path, worktree)
}
func (s Service) LocalBranches(ctx context.Context, path string) ([]Branch, error) {
	return s.Read.LocalBranches(ctx, path)
}
func (s Service) RemoteURL(ctx context.Context, path, remote string) (string, error) {
	return s.Read.RemoteURL(ctx, path, remote)
}
func (s Service) DeleteBranch(ctx context.Context, path, name, oid string) error {
	return s.Write.DeleteBranch(ctx, path, name, oid)
}
