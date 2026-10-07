package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// DefaultRestoreLogPath is $XDG_STATE_HOME/gitperch/cleanup.log, falling back
// to ~/.local/state/gitperch/cleanup.log when the variable is unset or, as the
// XDG specification requires, not absolute.
func DefaultRestoreLogPath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "gitperch", "cleanup.log"), nil
}

// SetRestoreLog makes every branch deletion append its restore command to
// path, so the command outlives the dashboard. An empty path disables it.
func (a *Actions) SetRestoreLog(path string) {
	a.mu.Lock()
	a.restoreLog = path
	a.mu.Unlock()
}

// recordRestore appends one tab-separated line: time, repository, branch,
// commit and the command that recreates the branch. Repository and branch are
// Go-quoted (strconv.Quote), which keeps them exact and recoverable while no
// tab or newline in a name can forge a column or line.
// It reports whether the line was written; no log configured is not an error.
func (a *Actions) recordRestore(item CleanupItem, command string) (bool, error) {
	a.mu.Lock()
	path := a.restoreLog
	a.mu.Unlock()
	if path == "" {
		return false, nil
	}
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return false, err
	}
	// The log names repositories and commits; keep it private even if an
	// earlier file was created with looser permissions.
	err = file.Chmod(0o600)
	if err == nil {
		_, err = fmt.Fprintf(file, "%s\t%s\t%s\t%s\t%s\n", time.Now().UTC().Format(time.RFC3339), strconv.Quote(item.Group), strconv.Quote(item.Branch), item.OID, command)
	}
	err = errors.Join(err, file.Close())
	return err == nil, err
}
