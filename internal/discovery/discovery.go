// Package discovery finds Git worktrees without invoking Git. A candidate is
// a directory containing a .git directory or a regular .git file; validation
// is left to the Git inspection layer.
package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark-lvl/gitperch/internal/repository"
)

// Options controls filesystem traversal. Each root starts at depth zero;
// MaxDepth zero therefore inspects only the root itself.
type Options struct {
	Roots      []string
	MaxDepth   int
	IgnoreDirs []string
}

// Result contains all candidates found and non-fatal filesystem warnings.
type Result struct {
	Repositories []repository.Repository
	Warnings     []Warning
}

// Warning describes a path that could not be scanned or was skipped for
// symlink safety.
type Warning struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Scan traverses each configured root. Errors under one root do not prevent
// discoveries from other roots. Cancellation returns discoveries collected so
// far together with the context error.
func Scan(ctx context.Context, opts Options) (Result, error) {
	var result Result
	if ctx == nil {
		return result, fmt.Errorf("discovery: nil context")
	}
	if opts.MaxDepth < 0 {
		return result, fmt.Errorf("discovery: maximum depth must not be negative")
	}
	if len(opts.Roots) == 0 {
		return result, fmt.Errorf("discovery: at least one root is required")
	}

	ignore := make(map[string]struct{}, len(opts.IgnoreDirs))
	for _, name := range opts.IgnoreDirs {
		ignore[name] = struct{}{}
	}
	found := make(map[string]repository.Repository)
	for _, root := range opts.Roots {
		if err := ctx.Err(); err != nil {
			finalize(&result, found)
			return result, err
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			result.Warnings = append(result.Warnings, Warning{Path: root, Message: err.Error()})
			continue
		}
		abs = filepath.Clean(abs)
		if linkPath, err := symlinkInPath(abs); err != nil {
			result.Warnings = append(result.Warnings, Warning{Path: abs, Message: err.Error()})
			continue
		} else if linkPath != "" {
			result.Warnings = append(result.Warnings, Warning{Path: linkPath, Message: "skipped symlink root or symlink ancestor"})
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			result.Warnings = append(result.Warnings, Warning{Path: abs, Message: err.Error()})
			continue
		}
		if !info.IsDir() {
			result.Warnings = append(result.Warnings, Warning{Path: abs, Message: "root is not a directory"})
			continue
		}
		walk(ctx, abs, abs, 0, opts.MaxDepth, ignore, found, &result.Warnings)
	}
	finalize(&result, found)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// symlinkInPath checks every existing component, including the root itself.
// This catches roots whose apparent location escapes through a symlinked
// parent, while nonexistent suffixes are left for the normal root error.
func symlinkInPath(path string) (string, error) {
	vol := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, vol)
	current := vol
	if filepath.IsAbs(path) {
		current += string(filepath.Separator)
	}
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return current, nil
		}
	}
	return "", nil
}

func walk(ctx context.Context, root, dir string, depth, maxDepth int, ignore map[string]struct{}, found map[string]repository.Repository, warnings *[]Warning) {
	if ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		*warnings = append(*warnings, Warning{Path: dir, Message: err.Error()})
		return
	}
	if isRepository(entries) {
		repo, err := repository.New(dir)
		if err != nil {
			*warnings = append(*warnings, Warning{Path: dir, Message: err.Error()})
			return
		}
		found[repo.Path] = repo
		return
	}
	if depth >= maxDepth {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if _, ignored := ignore[entry.Name()]; ignored {
			continue
		}
		walk(ctx, root, filepath.Join(dir, entry.Name()), depth+1, maxDepth, ignore, found, warnings)
	}
}

func isRepository(entries []os.DirEntry) bool {
	for _, entry := range entries {
		if entry.Name() != ".git" {
			continue
		}
		if entry.IsDir() {
			return true
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func finalize(result *Result, found map[string]repository.Repository) {
	result.Repositories = make([]repository.Repository, 0, len(found))
	for _, repo := range found {
		result.Repositories = append(result.Repositories, repo)
	}
	sort.Slice(result.Repositories, func(i, j int) bool {
		a, b := result.Repositories[i], result.Repositories[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	sort.Slice(result.Warnings, func(i, j int) bool {
		if result.Warnings[i].Path != result.Warnings[j].Path {
			return result.Warnings[i].Path < result.Warnings[j].Path
		}
		return result.Warnings[i].Message < result.Warnings[j].Message
	})
}
