package app

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mark-lvl/gitperch/internal/repository"
)

func tracking(ahead, behind int) repository.Status {
	return repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true, Ahead: ahead, Behind: behind}
}

func TestAttentionClassifiesGitState(t *testing.T) {
	with := func(s repository.Status, edit func(*repository.Status)) repository.Status {
		edit(&s)
		return s
	}
	cases := []struct {
		name     string
		row      Row
		level    Level
		reasons  []Reason
		describe []string
	}{
		{"clean and synchronized", Row{Status: tracking(0, 0)}, Low, nil, nil},
		{"unstaged change", Row{Status: with(tracking(0, 0), func(s *repository.Status) { s.Changes = 3 })}, High,
			[]Reason{ReasonUncommitted}, []string{"3 uncommitted files"}},
		{"untracked file", Row{Status: with(tracking(0, 0), func(s *repository.Status) { s.Untracked = 1 })}, High,
			[]Reason{ReasonUntracked}, []string{"1 untracked file"}},
		{"ahead", Row{Status: tracking(2, 0)}, High, []Reason{ReasonUnpushed}, []string{"2 commits ahead of origin/main"}},
		{"behind", Row{Status: tracking(0, 1)}, Medium, []Reason{ReasonBehind}, []string{"1 commit behind origin/main"}},
		{"diverged", Row{Status: tracking(2, 3)}, High, []Reason{ReasonDiverged}, []string{"diverged from origin/main: 2 ahead, 3 behind"}},
		{"conflicts during a merge", Row{Status: with(tracking(0, 0), func(s *repository.Status) { s.Changes, s.Conflicts, s.Operation = 3, 1, "MERGE_HEAD" })}, Critical,
			[]Reason{ReasonConflicts, ReasonOperation, ReasonUncommitted}, []string{"1 conflicted file", "merge in progress", "2 uncommitted files"}},
		{"clean rebase stop", Row{Status: with(tracking(0, 0), func(s *repository.Status) { s.Operation = "rebase-merge" })}, Critical,
			[]Reason{ReasonOperation}, []string{"rebase in progress"}},
		{"inspection failed hides other fields", Row{Status: repository.Status{Error: "boom", Changes: 4, Ahead: 1}}, Critical,
			[]Reason{ReasonInspectionFailed}, []string{"Git status could not be read"}},
		{"detached at a referenced commit", Row{Status: repository.Status{Detached: true}}, Low, nil, nil},
		{"detached with commits on no ref", Row{Status: repository.Status{Detached: true, HeadUnreferenced: true}}, High,
			[]Reason{ReasonDetachedCommits}, []string{"detached HEAD commit is on no branch or tag"}},
		{"detached and dirty", Row{Status: repository.Status{Detached: true, Changes: 1}}, High, []Reason{ReasonUncommitted}, nil},
		{"no upstream", Row{Status: repository.Status{Branch: "topic"}}, Medium, []Reason{ReasonNoUpstream}, []string{"branch has no upstream"}},
		{"upstream ref missing locally", Row{Status: repository.Status{Branch: "topic", Upstream: "origin/topic"}}, Medium,
			[]Reason{ReasonTrackingUnknown}, []string{"no local tracking ref for origin/topic"}},
		{"no commits", Row{Status: repository.Status{Branch: "main", Unborn: true}}, Medium, []Reason{ReasonNoCommits}, nil},
		{"dirty, ahead and untracked", Row{Status: with(tracking(2, 0), func(s *repository.Status) { s.Changes, s.Untracked = 1, 2 })}, High,
			[]Reason{ReasonUncommitted, ReasonUntracked, ReasonUnpushed}, []string{"1 uncommitted file", "2 untracked files", "2 commits ahead of origin/main"}},
		{"dirty and behind", Row{Status: with(tracking(0, 4), func(s *repository.Status) { s.Changes = 1 })}, High, []Reason{ReasonUncommitted, ReasonBehind}, nil},
		{"stale worktree record", Row{Worktree: &WorktreeInfo{Linked: true, Prunable: true, PrunableReason: "gitdir file points to non-existent location"}}, Medium,
			[]Reason{ReasonStaleWorktree}, []string{"stale worktree record: gitdir file points to non-existent location"}},
		{"bare main repository", Row{Worktree: &WorktreeInfo{Main: true, Bare: true}}, Low, nil, nil},
		{"locked clean worktree", Row{Status: tracking(0, 0), Worktree: &WorktreeInfo{Linked: true, Locked: true}}, Low, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := c.row.Attention()
			if a.Level != c.level || !slices.Equal(a.Reasons, c.reasons) {
				t.Fatalf("got %s %v, want %s %v", a.Level, a.Reasons, c.level, c.reasons)
			}
			if a.Reasons == nil || a.Needs() != (c.level > Low) {
				t.Fatalf("reasons must be non-nil and Needs consistent: %+v", a)
			}
			if c.describe != nil {
				var got []string
				for _, reason := range a.Reasons {
					got = append(got, c.row.Describe(reason))
				}
				if !slices.Equal(got, c.describe) {
					t.Fatalf("descriptions %q, want %q", got, c.describe)
				}
			}
		})
	}
}

func TestAttentionWithKeepsOrderAndRaisesLevel(t *testing.T) {
	a := Row{Status: tracking(0, 1)}.Attention()
	b := a.With(ReasonActionFailed)
	if b.Level != Critical || !slices.Equal(b.Reasons, []Reason{ReasonActionFailed, ReasonBehind}) {
		t.Fatalf("%+v", b)
	}
	if len(a.Reasons) != 1 || a.Level != Medium {
		t.Fatalf("With mutated its receiver: %+v", a)
	}
	if c := b.With(ReasonActionFailed); len(c.Reasons) != 2 {
		t.Fatalf("duplicate reason: %+v", c)
	}
}

func TestAttentionTracksStatusAcrossRefreshes(t *testing.T) {
	row := Row{Status: tracking(0, 0)}
	if row.Attention().Needs() {
		t.Fatal("clean row needs attention")
	}
	row.Status.Changes = 1
	if row.Attention().Level != High {
		t.Fatal("new change not reflected")
	}
	row.Status = tracking(0, 0)
	if row.Attention().Needs() {
		t.Fatal("resolved change still reported")
	}
}

func TestAttentionInJSONReport(t *testing.T) {
	dirty := tracking(1, 0)
	dirty.Changes = 2
	rows := []Row{{Repository: repository.Repository{Path: "/a", Name: "a"}, Status: dirty}, {Repository: repository.Repository{Path: "/b", Name: "b"}, Status: tracking(0, 0)}}
	var out bytes.Buffer
	if err := WriteJSON(&out, rows, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"attention": {
        "level": "high",
        "reasons": [
          "uncommitted_changes",
          "unpushed_commits"
        ]`) || !strings.Contains(out.String(), `"reasons": []`) {
		t.Fatal(out.String())
	}
	var report Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if got := report.Repositories[0]; got.Path != "/a" || got.Status.Ahead != 1 || got.Attention.Level != High {
		t.Fatalf("%+v", got)
	}
	if report.Repositories[1].Attention.Level != Low {
		t.Fatalf("%+v", report.Repositories[1])
	}
	var level Level
	if level.UnmarshalText([]byte("urgent")) == nil {
		t.Fatal("unknown level accepted")
	}
}

// TestAttentionFromRealGitState classifies repositories and linked worktrees
// as Git leaves them, through the same inspection the dashboard uses.
func TestAttentionFromRealGitState(t *testing.T) {
	repo, remote := actionTestRepoWithRemote(t)
	root := t.TempDir()
	// Linked worktrees share repo's common Git directory but carry their own
	// HEAD, index and operation state.
	dirty := filepath.Join(root, "wt-dirty")
	actionGit(t, repo, "worktree", "add", "-b", "dirty", dirty)
	actionTestWrite(t, filepath.Join(dirty, "tracked"), "edited\n")
	actionGit(t, dirty, "add", "tracked") // staged only
	parked := filepath.Join(root, "wt-parked")
	actionGit(t, repo, "worktree", "add", "--detach", parked, "main")
	orphaned := filepath.Join(root, "wt-orphaned")
	actionGit(t, repo, "worktree", "add", "--detach", orphaned, "main")
	actionTestWrite(t, filepath.Join(orphaned, "new"), "only here\n")
	actionTestCommit(t, orphaned, "detached work")

	// A second clone becomes behind, then diverged, then conflicted.
	conflicted := filepath.Join(t.TempDir(), "conflicted")
	clone := exec.Command("git", "clone", remote, conflicted)
	clone.Env = actionTestGitEnv(t)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	actionGit(t, conflicted, "config", "user.name", "Test")
	actionGit(t, conflicted, "config", "user.email", "test@example.invalid")
	actionTestAdvanceRemote(t, remote, "peer\n")
	actionGit(t, conflicted, "fetch", "origin")
	behind := loadRows(t, conflicted).Rows[0].Attention()
	if behind.Level != Medium || !slices.Equal(behind.Reasons, []Reason{ReasonBehind}) {
		t.Fatalf("behind: %+v", behind)
	}
	actionTestWrite(t, filepath.Join(conflicted, "peer-file"), "mine\n")
	actionTestCommit(t, conflicted, "local")
	diverged := loadRows(t, conflicted).Rows[0].Attention()
	if diverged.Level != High || !slices.Equal(diverged.Reasons, []Reason{ReasonDiverged}) {
		t.Fatalf("diverged: %+v", diverged)
	}
	merge := exec.Command("git", "-C", conflicted, "merge", "origin/main")
	merge.Env = actionTestGitEnv(t)
	if out, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("merge should conflict: %s", out)
	}

	rows := append(loadRows(t, repo).Rows, loadRows(t, conflicted).Rows...)
	want := map[string]struct {
		level   Level
		reasons []Reason
	}{
		repo:       {Low, nil},
		dirty:      {High, []Reason{ReasonUncommitted, ReasonNoUpstream}}, // staged only
		parked:     {Low, nil},
		orphaned:   {High, []Reason{ReasonDetachedCommits}},
		conflicted: {Critical, []Reason{ReasonConflicts, ReasonOperation, ReasonDiverged}},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for path, w := range want {
		a := rowByPath(t, rows, path).Attention()
		if a.Level != w.level || !slices.Equal(a.Reasons, w.reasons) {
			t.Errorf("%s: got %s %v, want %s %v", filepath.Base(path), a.Level, a.Reasons, w.level, w.reasons)
		}
	}
}
