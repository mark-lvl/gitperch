package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrNoDefaultRef = errors.New("default branch unknown")

func (r Runner) RemoteDefaultRef(ctx context.Context, path, remote string) (string, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return "", fmt.Errorf("invalid remote")
	}
	out, err := r.Run(ctx, path, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", ErrNoDefaultRef
		}
		return "", err
	}
	ref := strings.TrimSpace(string(out.Stdout))
	if !strings.HasPrefix(ref, "refs/remotes/"+remote+"/") || ref == "refs/remotes/"+remote+"/HEAD" {
		return "", ErrNoDefaultRef
	}
	return ref, nil
}

func (r Runner) ResolveCommit(ctx context.Context, path, ref string) (string, error) {
	if !strings.HasPrefix(ref, "refs/") {
		return "", fmt.Errorf("invalid reference")
	}
	out, err := r.Run(ctx, path, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out.Stdout))
	if !objectID(oid) {
		return "", fmt.Errorf("invalid commit")
	}
	return oid, nil
}

// IsAncestor reports whether oid is reachable from ref; exit status 1 means
// "no", any other failure is an error.
func (r Runner) IsAncestor(ctx context.Context, path, oid, ref string) (bool, error) {
	if !objectID(oid) || !strings.HasPrefix(ref, "refs/") {
		return false, fmt.Errorf("invalid ancestry check")
	}
	_, err := r.Run(ctx, path, "merge-base", "--is-ancestor", oid, ref)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// IgnoredFiles lists ignored paths in a worktree, collapsing wholly ignored
// directories, so a cleanup can refuse to delete them with the worktree.
func (r Runner) IgnoredFiles(ctx context.Context, path string) ([]string, error) {
	out, err := r.Run(ctx, path, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range bytes.Split(out.Stdout, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files, nil
}

// PruneWorktrees drops administrative records of worktrees whose directory
// is gone. Git never prunes locked records.
func (r Runner) PruneWorktrees(ctx context.Context, path string) error {
	_, err := r.Run(ctx, path, "worktree", "prune")
	return err
}

// RemoveWorktree deletes a clean linked worktree. Without --force Git refuses
// modified or untracked files, locked worktrees and the main worktree.
func (r Runner) RemoveWorktree(ctx context.Context, path, worktree string) error {
	if !filepath.IsAbs(worktree) {
		return fmt.Errorf("invalid reviewed worktree path")
	}
	_, err := r.Run(ctx, path, "worktree", "remove", worktree)
	return err
}
