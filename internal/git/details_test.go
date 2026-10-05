package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetailsRealChangesAndHistoryAreReadOnly(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "tracked"), "original\n")
	commit(t, d)
	write(t, filepath.Join(d, "tracked"), "original\nstaged\n")
	gitCmd(t, d, "add", "tracked")
	write(t, filepath.Join(d, "tracked"), "original\nstaged\nunstaged\n")
	write(t, filepath.Join(d, "new file"), "new\n")
	before, _ := os.ReadFile(filepath.Join(d, ".git", "index"))
	data, err := (Runner{}).Details(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data.Diff, "+unstaged") || !strings.Contains(data.Diff, "+staged") {
		t.Fatal("tracked patches missing")
	}
	if len(data.Files) != 2 || len(data.Commits) != 1 {
		t.Fatalf("%+v", data)
	}
	for _, f := range data.Files {
		if f.Path == "tracked" && (f.Added != 2 || f.Deleted != 0 || f.Code != "MM") {
			t.Fatalf("staged+unstaged: %+v", f)
		}
	}
	after, _ := os.ReadFile(filepath.Join(d, ".git", "index"))
	if !bytes.Equal(before, after) {
		t.Fatal("details mutated index")
	}
}
func TestDetailsUnbornAndRename(t *testing.T) {
	d := disposable(t)
	write(t, filepath.Join(d, "before"), "content\n")
	data, err := (Runner{}).Details(context.Background(), d)
	if err != nil || len(data.Commits) != 0 || len(data.Files) != 1 {
		t.Fatalf("unborn: %+v %v", data, err)
	}
	commit(t, d)
	gitCmd(t, d, "mv", "before", "after")
	data, err = (Runner{}).Details(context.Background(), d)
	if err != nil || len(data.Files) != 1 || data.Files[0].Path != "after" {
		t.Fatalf("rename: %+v %v", data, err)
	}
}
