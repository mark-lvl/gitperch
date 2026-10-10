package github

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBrowseCommand(t *testing.T) {
	gh, _ := fakeGH(t, "", 0)
	repo := Repo{"github.com", "acme", "widgets"}
	for _, tc := range []struct {
		number int
		branch string
		want   []string
	}{
		{42, "ignored", []string{"browse", "42", "--repo=github.com/acme/widgets"}},
		{0, "-x/feature", []string{"browse", "--repo=github.com/acme/widgets", "--branch=-x/feature"}},
		{0, "", []string{"browse", "--repo=github.com/acme/widgets"}},
	} {
		cmd, err := (Runner{Executable: gh}).BrowseCommand(repo, tc.number, tc.branch)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cmd.Args[1:], tc.want) || cmd.Dir != os.TempDir() || !slices.Contains(cmd.Env, "GH_PROMPT_DISABLED=1") {
			t.Fatalf("command %q in %q", cmd.Args, cmd.Dir)
		}
	}
	if _, err := (Runner{Executable: filepath.Join(t.TempDir(), "missing-gh")}).BrowseCommand(repo, 1, ""); err == nil {
		t.Fatal("missing gh built a command")
	}
}
