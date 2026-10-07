package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
)

var inspected = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// linkedRow is a clean, synchronized linked worktree whose HEAD last moved
// idle before inspection, with the given merge comparison.
func linkedRow(idle time.Duration, in *Integration) Row {
	s := tracking(0, 0)
	s.Branch, s.Upstream, s.HeadOID = "topic", "origin/topic", strings.Repeat("a", 40)
	s.InspectedAt, s.LastActivity = inspected, inspected.Add(-idle)
	return Row{Status: s, Worktree: &WorktreeInfo{Linked: true, MainPath: "/repo", Integration: in}}
}

var (
	merged    = &Integration{Base: "origin/main", Merged: true}
	notMerged = &Integration{Base: "origin/main"}
	noBase    = &Integration{Error: "default branch unknown — run git remote set-head origin -a"}
	day       = 24 * time.Hour
)

func TestLifecycleClassifiesLinkedWorktrees(t *testing.T) {
	edit := func(r Row, f func(*Row)) Row {
		w := *r.Worktree
		r.Worktree = &w
		f(&r)
		return r
	}
	cases := []struct {
		name     string
		row      Row
		state    WorktreeState
		reasons  []Signal
		describe []string
	}{
		{"merged and quiet", linkedRow(6*day, merged), WorktreeFinished,
			[]Signal{SignalClean, SignalNothingToPush, SignalMerged, SignalInactive},
			[]string{"clean working tree", "nothing to push to origin/topic", "merged into origin/main", "no HEAD activity for 6 days"}},
		// A worktree just created from the default branch is merged too.
		{"merged but recently active", linkedRow(3*time.Hour, merged), WorktreeActive,
			[]Signal{SignalClean, SignalNothingToPush, SignalMerged, SignalRecentActivity}, []string{3: "HEAD moved 3 hours ago"}},
		{"merged without activity", edit(linkedRow(0, merged), func(r *Row) { r.Status.LastActivity = time.Time{} }), WorktreeUnknown,
			[]Signal{SignalClean, SignalNothingToPush, SignalMerged, SignalActivityUnknown}, []string{3: "no HEAD reflog, so activity is unknown"}},
		{"unmerged and old", linkedRow(20*day, notMerged), WorktreeIdle,
			[]Signal{SignalClean, SignalNothingToPush, SignalNotMerged, SignalInactive}, []string{2: "not merged into origin/main"}},
		{"unmerged, neither recent nor old", linkedRow(5*day, notMerged), WorktreeUnknown,
			[]Signal{SignalClean, SignalNothingToPush, SignalNotMerged, SignalInactive}, nil},
		{"recent unmerged work", linkedRow(time.Hour, notMerged), WorktreeActive,
			[]Signal{SignalClean, SignalNothingToPush, SignalNotMerged, SignalRecentActivity}, nil},
		{"merge state unknown and old", linkedRow(20*day, noBase), WorktreeIdle,
			[]Signal{SignalClean, SignalNothingToPush, SignalMergeUnknown, SignalInactive},
			[]string{2: "merge state unknown: default branch unknown — run git remote set-head origin -a"}},
		{"no comparison attempted", linkedRow(6*day, nil), WorktreeUnknown,
			[]Signal{SignalClean, SignalNothingToPush, SignalMergeUnknown, SignalInactive}, nil},
		{"no upstream, merged", edit(linkedRow(3*day, merged), func(r *Row) { r.Status.Upstream, r.Status.ComparisonKnown = "", false }), WorktreeFinished,
			[]Signal{SignalClean, SignalNoUpstream, SignalMerged, SignalInactive}, nil},
		{"detached at a merged commit", edit(linkedRow(3*day, merged), func(r *Row) { r.Status.Branch, r.Status.Upstream, r.Status.Detached = "", "", true }), WorktreeFinished,
			[]Signal{SignalClean, SignalMerged, SignalInactive}, nil},
		// Age never turns unfinished work into cleanup material.
		{"dirty and old", edit(linkedRow(30*day, merged), func(r *Row) { r.Status.Changes, r.Status.Untracked = 2, 1 }), WorktreeInProgress,
			[]Signal{SignalUncommitted, SignalUntracked, SignalNothingToPush, SignalMerged, SignalInactive},
			[]string{"2 uncommitted files", "1 untracked file"}},
		{"unpushed commits", edit(linkedRow(30*day, notMerged), func(r *Row) { r.Status.Ahead = 2 }), WorktreeInProgress,
			[]Signal{SignalUnpushed, SignalClean, SignalNotMerged, SignalInactive}, []string{"2 commits ahead of origin/topic"}},
		{"diverged", edit(linkedRow(30*day, notMerged), func(r *Row) { r.Status.Ahead, r.Status.Behind = 1, 1 }), WorktreeInProgress,
			[]Signal{SignalDiverged, SignalClean, SignalNotMerged, SignalInactive}, nil},
		{"detached commits on no ref", edit(linkedRow(30*day, notMerged), func(r *Row) { r.Status.Detached, r.Status.HeadUnreferenced, r.Status.Upstream = true, true, "" }), WorktreeInProgress,
			[]Signal{SignalDetachedCommits, SignalClean, SignalNotMerged, SignalInactive}, nil},
		{"no commits", edit(linkedRow(30*day, nil), func(r *Row) { r.Status.Unborn, r.Status.Upstream = true, "" }), WorktreeInProgress,
			[]Signal{SignalNoCommits, SignalClean, SignalInactive}, nil},
		{"conflicts mid-merge", edit(linkedRow(30*day, merged), func(r *Row) { r.Status.Changes, r.Status.Conflicts, r.Status.Operation = 2, 1, "MERGE_HEAD" }), WorktreeBlocked,
			[]Signal{SignalConflicts, SignalOperation, SignalUncommitted, SignalNothingToPush, SignalMerged, SignalInactive},
			[]string{"1 conflicted file", "merge in progress", "1 uncommitted file"}},
		{"clean rebase stop", edit(linkedRow(30*day, merged), func(r *Row) { r.Status.Operation = "rebase-merge" }), WorktreeBlocked,
			[]Signal{SignalOperation, SignalClean, SignalNothingToPush, SignalMerged, SignalInactive}, nil},
		{"locked, merged and old", edit(linkedRow(30*day, merged), func(r *Row) { r.Worktree.Locked, r.Worktree.LockReason = true, "on USB disk" }), WorktreeBlocked,
			[]Signal{SignalLocked, SignalClean, SignalNothingToPush, SignalMerged, SignalInactive}, []string{"locked: on USB disk"}},
		{"inspection failed", edit(linkedRow(30*day, merged), func(r *Row) { r.Status.Error, r.Status.Changes = "boom", 3 }), WorktreeBlocked,
			[]Signal{SignalInspectionFailed}, []string{"Git status could not be read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := tc.row.Lifecycle()
			if l == nil {
				t.Fatal("no lifecycle for a linked worktree")
			}
			if l.State != tc.state || !slices.Equal(l.Reasons, tc.reasons) {
				t.Fatalf("lifecycle = %s %v, want %s %v", l.State, l.Reasons, tc.state, tc.reasons)
			}
			for i, want := range tc.describe {
				if want != "" {
					if got := tc.row.DescribeSignal(l.Reasons[i]); got != want {
						t.Errorf("DescribeSignal(%s) = %q, want %q", l.Reasons[i], got, want)
					}
				}
			}
		})
	}
}

func TestLifecycleOnlyAppliesToLinkedWorktrees(t *testing.T) {
	old := linkedRow(60*day, merged)
	for name, w := range map[string]*WorktreeInfo{
		"standalone repository": nil,
		"main worktree":         {Main: true, MainPath: "/repo", Integration: merged},
		"bare main repository":  {Main: true, Bare: true, MainPath: "/repo"},
		"stale record":          {Linked: true, Prunable: true, MainPath: "/repo", Integration: merged},
	} {
		row := Row{Status: old.Status, Worktree: w}
		if l := row.Lifecycle(); l != nil {
			t.Errorf("%s: lifecycle %+v, want none", name, l)
		}
		if a := row.Attention(); slices.Contains(a.Reasons, ReasonWorktreeFinished) || slices.Contains(a.Reasons, ReasonWorktreeIdle) {
			t.Errorf("%s: attention %+v", name, a)
		}
	}
}

func TestLifecycleRaisesMediumAttentionForReview(t *testing.T) {
	cases := []struct {
		row      Row
		reasons  []Reason
		describe string
	}{
		{linkedRow(6*day, merged), []Reason{ReasonWorktreeFinished}, "linked worktree looks finished: merged into origin/main, no HEAD activity for 6 days"},
		{linkedRow(20*day, notMerged), []Reason{ReasonWorktreeIdle}, "linked worktree idle: no HEAD activity for 20 days"},
		{linkedRow(5*day, notMerged), []Reason{}, ""},
		{linkedRow(time.Hour, merged), []Reason{}, ""},
	}
	for _, tc := range cases {
		a := tc.row.Attention()
		wantLevel := Low
		if len(tc.reasons) > 0 {
			wantLevel = Medium
		}
		if a.Level != wantLevel || !slices.Equal(a.Reasons, tc.reasons) {
			t.Fatalf("%s: attention %+v, want %s %v", tc.row.Lifecycle().State, a, wantLevel, tc.reasons)
		}
		if tc.describe != "" {
			if got := tc.row.Describe(tc.reasons[0]); got != tc.describe {
				t.Errorf("Describe = %q, want %q", got, tc.describe)
			}
		}
	}
	// Lifecycle review ranks below unfinished work in the same worktree.
	idleNoUpstream := linkedRow(20*day, notMerged)
	idleNoUpstream.Status.Upstream, idleNoUpstream.Status.ComparisonKnown = "", false
	if a := idleNoUpstream.Attention(); a.Level != Medium || !slices.Equal(a.Reasons, []Reason{ReasonNoUpstream, ReasonWorktreeIdle}) {
		t.Fatalf("idle without upstream: %+v", a)
	}
}

func TestJSONReportsLifecycleForLinkedWorktreesOnly(t *testing.T) {
	main := Row{Repository: repository.Repository{Path: "/repo", Name: "repo"}, Status: tracking(0, 0), Worktree: &WorktreeInfo{Main: true, MainPath: "/repo"}}
	linked := linkedRow(6*day, merged)
	linked.Repository = repository.Repository{Path: "/wt/topic", Name: "topic"}
	var out bytes.Buffer
	if err := WriteJSON(&out, []Row{main, linked}, nil); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Repositories []map[string]json.RawMessage `json:"repositories"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if _, ok := report.Repositories[0]["lifecycle"]; ok {
		t.Fatalf("main worktree has a lifecycle: %s", out.String())
	}
	want := `{"state":"likely_finished","reasons":["clean","nothing_to_push","merged","inactive"]}`
	if got := compactJSON(t, report.Repositories[1]["lifecycle"]); got != want {
		t.Fatalf("lifecycle = %s, want %s", got, want)
	}
	var worktree struct {
		Integration Integration `json:"integration"`
	}
	if err := json.Unmarshal(report.Repositories[1]["worktree"], &worktree); err != nil || worktree.Integration != *merged {
		t.Fatalf("integration = %+v, %v", worktree.Integration, err)
	}
	var table bytes.Buffer
	if err := WriteTable(&table, []Row{linked}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table.String(), "likely finished worktree") {
		t.Fatalf("table:\n%s", table.String())
	}
}

func compactJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// ageHead backdates a worktree's HEAD reflog, the activity lifecycle reads.
func ageHead(t *testing.T, worktree string, age time.Duration) {
	t.Helper()
	gitDir := strings.TrimSpace(string(actionGit(t, worktree, "rev-parse", "--path-format=absolute", "--git-dir")))
	when := time.Now().Add(-age)
	if err := os.Chtimes(filepath.Join(gitDir, "logs", "HEAD"), when, when); err != nil {
		t.Fatal(err)
	}
}

func TestLoadComparesLinkedWorktreesWithDefaultBranch(t *testing.T) {
	repo, merged, ahead := cleanupRepo(t)
	snapshot := loadRows(t, repo)
	if main := rowByPath(t, snapshot.Rows, repo); main.Worktree.Integration != nil || main.Lifecycle() != nil {
		t.Fatalf("main worktree: %+v %+v", main.Worktree.Integration, main.Lifecycle())
	}
	if in := rowByPath(t, snapshot.Rows, merged).Worktree.Integration; in == nil || *in != (Integration{Base: "origin/main", Merged: true}) {
		t.Fatalf("merged worktree integration: %+v", in)
	}
	if in := rowByPath(t, snapshot.Rows, ahead).Worktree.Integration; in == nil || *in != (Integration{Base: "origin/main"}) {
		t.Fatalf("ahead worktree integration: %+v", in)
	}

	// Without origin/HEAD the default branch is unknown, as in cleanup.
	actionGit(t, repo, "remote", "set-head", "origin", "--delete")
	in := rowByPath(t, loadRows(t, repo).Rows, merged).Worktree.Integration
	if in == nil || in.Merged || !strings.Contains(in.Error, "git remote set-head origin -a") {
		t.Fatalf("integration without origin/HEAD: %+v", in)
	}
}

func TestLifecycleFollowsRefreshes(t *testing.T) {
	repo, wt, ahead := cleanupRepo(t)
	state := func(path string) (WorktreeState, []Signal) {
		t.Helper()
		l := rowByPath(t, loadRows(t, repo).Rows, path).Lifecycle()
		if l == nil {
			t.Fatalf("no lifecycle for %s", path)
		}
		return l.State, l.Reasons
	}

	// Freshly created from main: merged, but just active.
	if got, _ := state(wt); got != WorktreeActive {
		t.Fatalf("new worktree: %s", got)
	}
	ageHead(t, wt, 3*day)
	if got, reasons := state(wt); got != WorktreeFinished || !slices.Equal(reasons, []Signal{SignalClean, SignalNoUpstream, SignalMerged, SignalInactive}) {
		t.Fatalf("quiet merged worktree: %s %v", got, reasons)
	}
	// clean → dirty
	actionTestWrite(t, filepath.Join(wt, "draft"), "wip\n")
	if got, _ := state(wt); got != WorktreeInProgress {
		t.Fatalf("dirty worktree: %s", got)
	}
	// dirty → committed: the new commit is not on origin/main yet.
	actionTestCommit(t, wt, "draft")
	ageHead(t, wt, 3*day)
	if got, reasons := state(wt); got != WorktreeUnknown || !slices.Contains(reasons, SignalNotMerged) {
		t.Fatalf("committed worktree: %s %v", got, reasons)
	}
	// committed → merged into the default branch (push updates origin/main).
	actionGit(t, wt, "push", "origin", "HEAD:main")
	ageHead(t, wt, 3*day)
	if got, _ := state(wt); got != WorktreeFinished {
		t.Fatalf("merged after push: %s", got)
	}
	// An operation in progress blocks; finishing it unblocks.
	gitDir := strings.TrimSpace(string(actionGit(t, wt, "rev-parse", "--path-format=absolute", "--git-dir")))
	marker := filepath.Join(gitDir, "MERGE_HEAD")
	actionTestWrite(t, marker, string(actionGit(t, wt, "rev-parse", "HEAD")))
	if got, _ := state(wt); got != WorktreeBlocked {
		t.Fatalf("mid-merge: %s", got)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if got, _ := state(wt); got != WorktreeFinished {
		t.Fatalf("after the operation: %s", got)
	}
	// A lock blocks removal whatever else is true.
	actionGit(t, repo, "worktree", "lock", wt)
	if got, _ := state(wt); got != WorktreeBlocked {
		t.Fatalf("locked: %s", got)
	}

	// An unmerged, clean worktree is only idle once it has been quiet long.
	ageHead(t, ahead, 5*day)
	if got, _ := state(ahead); got != WorktreeUnknown {
		t.Fatalf("unmerged, 5 days: %s", got)
	}
	ageHead(t, ahead, 20*day)
	if got, reasons := state(ahead); got != WorktreeIdle || !slices.Equal(reasons, []Signal{SignalClean, SignalNoUpstream, SignalNotMerged, SignalInactive}) {
		t.Fatalf("unmerged, 20 days: %s %v", got, reasons)
	}
}

func TestIntegrationWithLocalDefaultBranch(t *testing.T) {
	repo := actionTestRepo(t)
	actionTestWrite(t, filepath.Join(repo, "f"), "x\n")
	actionTestCommit(t, repo, "initial")
	actionGit(t, repo, "checkout", "-b", "other")
	base := t.TempDir()
	onMain, topic := filepath.Join(base, "on-main"), filepath.Join(base, "topic")
	actionGit(t, repo, "worktree", "add", onMain, "main")
	actionGit(t, repo, "worktree", "add", "-b", "topic", topic, "main")
	rows := loadRows(t, repo).Rows
	// Comparing the default branch with itself proves nothing.
	if in := rowByPath(t, rows, onMain).Worktree.Integration; in == nil || in.Merged || in.Error != "the default branch is checked out here" {
		t.Fatalf("default branch worktree: %+v", in)
	}
	if in := rowByPath(t, rows, topic).Worktree.Integration; in == nil || *in != (Integration{Base: "main (local default)", Merged: true}) {
		t.Fatalf("topic worktree: %+v", in)
	}
}

// inventoryOnly lists worktrees but cannot compare them with a base.
type inventoryOnly struct{ git gitcli.Runner }

func (s inventoryOnly) Inspect(ctx context.Context, path string) repository.Status {
	return s.git.Inspect(ctx, path)
}
func (s inventoryOnly) SupportsWorktreeInventory(ctx context.Context) bool {
	return s.git.SupportsWorktreeInventory(ctx)
}
func (s inventoryOnly) Worktrees(ctx context.Context, path string) ([]gitcli.Worktree, error) {
	return s.git.Worktrees(ctx, path)
}

func TestLifecycleWithoutIntegrationSupport(t *testing.T) {
	repo, merged, _ := cleanupRepo(t)
	ageHead(t, merged, 3*day)
	snapshot, err := Load(context.Background(), discovery.Options{Roots: []string{repo}, MaxDepth: 4}, inventoryOnly{}, 4)
	if err != nil {
		t.Fatal(err)
	}
	row := rowByPath(t, snapshot.Rows, merged)
	if l := row.Lifecycle(); row.Worktree.Integration != nil || l == nil || l.State != WorktreeUnknown || !slices.Contains(l.Reasons, SignalMergeUnknown) {
		t.Fatalf("without integration: %+v %+v", row.Worktree.Integration, l)
	}
}
