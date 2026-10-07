package git

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Worktree is one record of `git worktree list --porcelain -z`. The first
// record is always the main worktree, which may be a bare repository.
type Worktree struct {
	Path           string
	HeadOID        string
	Branch         string
	Bare, Detached bool
	Main           bool
	Locked         bool
	LockReason     string
	Prunable       bool
	PrunableReason string
}

var errWorktreeList = errors.New("malformed worktree list")

// ParseWorktrees reads NUL-terminated attributes; an empty attribute ends a
// record. Unknown attributes from newer Git are ignored, structural surprises
// are rejected rather than guessed.
func ParseWorktrees(data []byte) ([]Worktree, error) {
	var result []Worktree
	var current *Worktree
	fields := strings.Split(string(data), "\x00")
	// Output ends with the record terminator plus Split's trailing empty field.
	if len(fields) < 2 || fields[len(fields)-1] != "" {
		return nil, errWorktreeList
	}
	for _, field := range fields[:len(fields)-1] {
		if field == "" {
			if current == nil {
				return nil, errWorktreeList
			}
			result = append(result, *current)
			current = nil
			continue
		}
		key, value, _ := strings.Cut(field, " ")
		if key == "worktree" {
			if current != nil || !filepath.IsAbs(value) {
				return nil, errWorktreeList
			}
			current = &Worktree{Path: filepath.Clean(value), Main: len(result) == 0}
			continue
		}
		if current == nil {
			return nil, errWorktreeList
		}
		switch key {
		case "HEAD":
			// Accept all-zero OID explicitly for unborn HEAD, or standard OID format
			if !(objectID(value) || value == strings.Repeat("0", len(value)) && (len(value) == 40 || len(value) == 64)) {
				return nil, errWorktreeList
			}
			current.HeadOID = value
		case "branch":
			name, ok := strings.CutPrefix(value, "refs/heads/")
			if !ok || name == "" {
				return nil, errWorktreeList
			}
			current.Branch = name
		case "detached":
			current.Detached = true
		case "bare":
			current.Bare = true
		case "locked":
			current.Locked, current.LockReason = true, value
		case "prunable":
			current.Prunable, current.PrunableReason = true, value
		}
	}
	if current != nil || len(result) == 0 {
		return nil, errWorktreeList
	}
	return result, nil
}

// Worktrees lists every worktree sharing path's common Git directory,
// including linked worktrees outside any scanned root and stale records.
func (r Runner) Worktrees(ctx context.Context, path string) ([]Worktree, error) {
	out, err := r.Run(ctx, path, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out.Stdout)
}

var inventorySupport sync.Map // executable path → bool

func parseGitVersion(out string) (major, minor int, ok bool) {
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return 0, 0, false
	}
	parts := strings.SplitN(fields[2], ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	return major, minor, errMajor == nil && errMinor == nil
}

// SupportsWorktreeInventory gates worktree listing and cleanup on Git 2.36,
// which added `worktree list -z`. Older or unreadable Git turns the feature
// off quietly instead of warning on every repository.
func (r Runner) SupportsWorktreeInventory(ctx context.Context) bool {
	key := r.Executable
	if key == "" {
		key = "git"
	}
	if cached, ok := inventorySupport.Load(key); ok {
		return cached.(bool)
	}
	out, err := r.Run(ctx, ".", "version")
	major, minor, ok := parseGitVersion(string(out.Stdout))
	supported := err == nil && ok && (major > 2 || major == 2 && minor >= 36)
	// Cache only definitive answers: a parsed version, or Git itself exiting
	// with an error. Timeouts, cancellation, output overflow and failures to
	// start Git may pass, so they are retried on the next call.
	var exit *exec.ExitError
	if err == nil && ok || errors.As(err, &exit) {
		inventorySupport.Store(key, supported)
	}
	return supported
}
