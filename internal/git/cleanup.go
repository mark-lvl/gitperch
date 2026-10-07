package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
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

type Branch struct {
	Name     string // short name
	OID      string
	Upstream string // e.g. refs/remotes/origin/x; "" when none
	Gone     bool   // upstream configured but its tracking ref is missing
	Symref   string // target of a symbolic ref such as refs/heads/master -> refs/heads/main; "" for a normal branch
}

func (r Runner) LocalBranches(ctx context.Context, path string) ([]Branch, error) {
	out, err := r.Run(ctx, path, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(upstream)%00%(upstream:track)%00%(symref)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var branches []Branch
	for _, line := range strings.Split(strings.TrimSuffix(string(out.Stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		name, ok := strings.CutPrefix(fields[0], "refs/heads/")
		if len(fields) != 5 || !ok || name == "" || !objectID(fields[1]) {
			return nil, fmt.Errorf("malformed branch list")
		}
		branches = append(branches, Branch{Name: name, OID: fields[1], Upstream: fields[2], Gone: fields[3] == "[gone]", Symref: fields[4]})
	}
	return branches, nil
}

// DeleteBranch removes refs/heads/<name> only while it still points at oid
// (Git compares and deletes atomically), then drops its config section.
// --no-deref makes a symbolic ref delete itself, never the branch it names.
func (r Runner) DeleteBranch(ctx context.Context, path, name, oid string) error {
	if name == "" || !objectID(oid) {
		return fmt.Errorf("invalid reviewed branch")
	}
	if _, err := r.Run(ctx, path, "update-ref", "--no-deref", "-d", "refs/heads/"+name, oid); err != nil {
		return err
	}
	section := "branch." + name
	_, err := r.Run(ctx, path, "config", "--local", "--name-only", "--get-regexp", "^"+regexp.QuoteMeta(section)+`\.[^.]+$`) // direct keys only, not branch.<name>.<more>.<key>
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil // no configuration for this branch
	}
	if err != nil {
		return fmt.Errorf("branch deleted; configuration not checked: %w", err)
	}
	if _, err := r.Run(ctx, path, "config", "--local", "--remove-section", section); err != nil {
		return fmt.Errorf("branch deleted; configuration left behind: %w", err)
	}
	return nil
}
