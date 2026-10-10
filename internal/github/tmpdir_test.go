package github

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain canonicalises TMPDIR so t.TempDir() paths match what Git reports.
// On macOS the default /var/folders/... is behind the /var -> /private/var
// symlink, which discovery rejects as a symlink ancestor.
func TestMain(m *testing.M) {
	if dir, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		os.Setenv("TMPDIR", dir)
	}
	os.Exit(m.Run())
}
