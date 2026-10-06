package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	patch, err := (Runner{}).Patch(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+unstaged") || !strings.Contains(patch, "+staged") {
		t.Fatal("tracked patches missing")
	}
	if len(data.Files) != 2 || len(data.Commits) != 1 {
		t.Fatalf("%+v", data)
	}
	if c := data.Commits[0]; c.Subject == "" || c.Time.IsZero() || time.Since(c.Time) > time.Hour {
		t.Fatalf("commit time not parsed: %+v", c)
	}
	if s := (Runner{}).Inspect(context.Background(), d); s.LastActivity.IsZero() || time.Since(s.LastActivity) > time.Hour {
		t.Fatalf("last activity not read from the HEAD reflog: %+v", s)
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

func TestAddLineStatsMatchesNumstatPasses(t *testing.T) {
	files := []ChangedFile{{Code: "MM", Path: "tracked"}, {Code: "R ", Path: "after"}, {Code: " M", Path: "image.png"}, {Code: "??", Path: "new"}}
	unstaged := "2\t1\ttracked\x00-\t-\timage.png\x00"
	staged := "3\t0\ttracked\x00" + "1\t1\t\x00before\x00after\x00"
	if err := addLineStats(files, unstaged, staged); err != nil {
		t.Fatal(err)
	}
	want := []ChangedFile{{Code: "MM", Path: "tracked", Added: 5, Deleted: 1}, {Code: "R ", Path: "after", Added: 1, Deleted: 1}, {Code: " M", Path: "image.png", Binary: true}, {Code: "??", Path: "new"}}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("file %d = %+v, want %+v", i, files[i], want[i])
		}
	}
	if err := addLineStats(files, "x\t1\ttracked\x00"); err == nil {
		t.Fatal("invalid line statistics accepted")
	}
}

// Agents can leave tens of thousands of changed files; matching statistics
// to them must not grow with the square of the count.
func BenchmarkAddLineStats(b *testing.B) {
	const n = 20000
	files := make([]ChangedFile, n)
	var numstat strings.Builder
	for i := range files {
		files[i] = ChangedFile{Code: " M", Path: fmt.Sprintf("dir/file%05d.txt", i)}
		fmt.Fprintf(&numstat, "1\t0\tdir/file%05d.txt\x00", i)
	}
	out := numstat.String()
	b.ResetTimer()
	for range b.N {
		if err := addLineStats(files, out); err != nil {
			b.Fatal(err)
		}
	}
}
