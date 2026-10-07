package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

func tuiGit(t *testing.T, path string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func tuiWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tuiCleanupRepo builds a repository under its own parent with a local bare
// remote, origin/HEAD set, a merged clean worktree and an unmerged one. The
// worktrees live outside the repository's parent so discovery sees one row.
func tuiCleanupRepo(t *testing.T) (repo, merged, ahead string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "no-global-config"))
	parent := t.TempDir()
	repo = filepath.Join(parent, "repo")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	tuiGit(t, repo, "init", "-b", "main")
	tuiGit(t, repo, "config", "user.name", "Cleanup Test")
	tuiGit(t, repo, "config", "user.email", "cleanup-test@example.invalid")
	tuiGit(t, repo, "config", "commit.gpgsign", "false")
	tuiGit(t, filepath.Dir(remote), "init", "--bare", "-b", "main", remote)
	tuiWrite(t, filepath.Join(repo, "tracked"), "initial\n")
	tuiGit(t, repo, "add", "--all")
	tuiGit(t, repo, "commit", "-m", "initial")
	tuiGit(t, repo, "remote", "add", "origin", remote)
	tuiGit(t, repo, "push", "--set-upstream", "origin", "main")
	// Git 2.48+ would recreate origin/HEAD on fetch; older Git ignores this key.
	tuiGit(t, repo, "config", "remote.origin.followRemoteHEAD", "never")
	tuiGit(t, repo, "remote", "set-head", "origin", "main")
	base := t.TempDir()
	merged, ahead = filepath.Join(base, "merged"), filepath.Join(base, "ahead")
	tuiGit(t, repo, "worktree", "add", "-b", "merged", merged)
	tuiGit(t, repo, "worktree", "add", "-b", "ahead", ahead)
	tuiWrite(t, filepath.Join(ahead, "new"), "work\n")
	tuiGit(t, ahead, "add", "--all")
	tuiGit(t, ahead, "commit", "-m", "unmerged work")
	return repo, merged, ahead
}

// drain runs cmd and feeds every resulting message back into the model until
// none remain.
func drain(m *Model, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for n := 0; len(queue) > 0 && n < 1000; n++ {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			_, follow := m.Update(msg)
			queue = append(queue, follow)
		}
	}
}

func highlightPath(t *testing.T, m *Model, path string) {
	t.Helper()
	for i, index := range m.visibleRows() {
		if m.rows[index].Path == path {
			m.highlight = i
			return
		}
	}
	t.Fatalf("%s is not visible", path)
}

// cleanupModel loads a real temporary workspace and highlights repo.
func cleanupModel(t *testing.T, repo string) *Model {
	t.Helper()
	m := New(context.Background(), func(ctx context.Context) (app.Snapshot, error) {
		return app.Load(ctx, discovery.Options{Roots: []string{filepath.Dir(repo)}, MaxDepth: 2}, gitcli.Runner{}, 2)
	}, true)
	m.EnableActions(app.NewActions(gitcli.Service{}, 2))
	m.EnableCleanup(true)
	m.spinning, m.ticking = true, true // keep drained commands free of timers
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
	drain(m, m.Init())
	highlightPath(t, m, repo)
	return m
}

func TestCleanupReviewTogglesAndRuns(t *testing.T) {
	repo, merged, ahead := tuiCleanupRepo(t)
	m := cleanupModel(t, repo)
	drain(m, m.key(key("c")))
	if m.cleanup == nil {
		t.Fatalf("no review opened: %q", m.message)
	}
	view := m.View().Content
	for _, want := range []string{"Clean up", "[x]", filepath.Base(merged), "merged into origin/main", "kept", filepath.Base(ahead), "not merged"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	m.key(key(" ")) // untick the worktree, then its merged branch
	m.key(key("down"))
	m.key(key(" "))
	if strings.Contains(m.View().Content, "[x]") {
		t.Fatal("toggle did not untick")
	}
	m.key(key("enter"))
	if m.cleanup == nil || !strings.Contains(m.message, "Nothing ticked") {
		t.Fatalf("empty run should be refused: %q", m.message)
	}
	m.key(key(" "))
	m.key(key("up"))
	m.key(key(" "))
	drain(m, m.key(key("enter")))
	if _, err := os.Stat(merged); !os.IsNotExist(err) {
		t.Fatal("merged worktree not removed")
	}
	if !strings.Contains(m.message, "Clean up finished · 2 succeeded") {
		t.Fatalf("summary: %q", m.message)
	}
}

func TestCleanupEscDiscards(t *testing.T) {
	repo, merged, _ := tuiCleanupRepo(t)
	m := cleanupModel(t, repo)
	drain(m, m.key(key("c")))
	m.key(key("esc"))
	if m.cleanup != nil || m.message != "Cancelled" {
		t.Fatalf("esc: %v %q", m.cleanup, m.message)
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("cancelled cleanup removed a worktree")
	}
}

func TestCleanupHiddenOnOldGit(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.EnableCleanup(false)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	for _, c := range m.commands() {
		if c.id == "cleanup" {
			t.Fatal("cleanup offered on old Git")
		}
	}
	if cmd := m.key(key("c")); cmd != nil || !strings.Contains(m.message, "Git 2.36") {
		t.Fatalf("c on old Git: %q", m.message)
	}
}

func TestCleanupTargetsWholeGroupFromChild(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.key(key("right"))
	m.highlight = 1
	if got := m.cleanupPaths(); len(got) != 1 || got[0] != "/home/mark/projects/api" {
		t.Fatalf("paths: %v", got)
	}
}

func TestCleanupResultsSurviveRemovedRows(t *testing.T) {
	repo, merged, _ := tuiCleanupRepo(t)
	m := cleanupModel(t, repo)
	drain(m, m.key(key("c")))
	drain(m, m.key(key("enter")))
	if _, err := os.Stat(merged); !os.IsNotExist(err) {
		t.Fatal("merged worktree not removed")
	}
	found := false
	for _, line := range m.detailsContent() {
		found = found || strings.Contains(line, merged)
	}
	if !found {
		t.Fatalf("removed worktree missing from batch results: %v", m.detailsContent())
	}
}

func TestCleanupPreflightFailureMarksActionFailed(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.EnableCleanup(true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.actionGeneration = 1
	m.cleanupMessage(cleanupMsg{generation: 1, preview: app.CleanupPreview{ID: 1, Items: []app.CleanupItem{
		{ID: "x", Group: "/repos/a", Kind: app.CleanupGroup, Path: "/repos/a", Reason: "fetch failed", Failed: true},
	}}})
	if m.cleanup == nil || !m.actionFailed {
		t.Fatalf("failed preflight not flagged: %v %v", m.cleanup, m.actionFailed)
	}
	view := m.View().Content
	for _, want := range []string{"kept", "fetch failed", "Nothing can be cleaned up safely"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
}

func TestCleanupOfferedInPaletteHintsAndHelp(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.EnableCleanup(true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 30}) // Clean up is the first hint to drop
	found := false
	for _, c := range m.commands() {
		found = found || c.id == "cleanup"
	}
	if !found {
		t.Fatal("cleanup missing from palette")
	}
	if !strings.Contains(m.View().Content, "Clean up") {
		t.Fatal("hint bar lacks Clean up")
	}
	if !strings.Contains(strings.Join(m.helpContent(), "\n"), "Clean up merged worktrees") {
		t.Fatal("help lacks c")
	}
	m.EnableCleanup(false)
	if strings.Contains(strings.Join(m.helpContent(), "\n"), "Clean up") {
		t.Fatal("help mentions Clean up on old Git")
	}
}

func TestCleanupEscWhilePreparingOpensNoReviewAndReleasesSlot(t *testing.T) {
	repo, merged, _ := tuiCleanupRepo(t)
	m := cleanupModel(t, repo)
	cmd := m.key(key("c"))
	if !m.preparing || cmd == nil {
		t.Fatalf("preparation did not start: %q", m.message)
	}
	m.key(key("esc")) // cancels the context before the plan result arrives
	drain(m, cmd)
	if m.cleanup != nil || m.preparing || m.message != "Cleanup preparation cancelled" {
		t.Fatalf("cancelled preparation: review=%v preparing=%v message=%q", m.cleanup, m.preparing, m.message)
	}
	if m.actionCancel != nil {
		t.Fatal("cancel function not released")
	}
	if _, err := os.Stat(merged); err != nil {
		t.Fatal("cancelled preparation removed a worktree")
	}
	drain(m, m.key(key("c")))
	if m.cleanup == nil {
		t.Fatalf("active slot not released; cannot plan again: %q", m.message)
	}
}

func TestCleanupErrorAndEmptyPlanReleaseCancel(t *testing.T) {
	for name, msg := range map[string]cleanupMsg{
		"error": {generation: 1, err: context.DeadlineExceeded},
		"empty": {generation: 1},
	} {
		t.Run(name, func(t *testing.T) {
			m := New(context.Background(), nil, true)
			m.EnableActions(app.NewActions(newActionFake(false), 1))
			m.EnableCleanup(true)
			ctx, cancel := context.WithCancel(context.Background())
			m.actionGeneration, m.actionCancel, m.actionCtx, m.preparing = 1, cancel, ctx, true
			m.cleanupMessage(msg)
			if m.cleanup != nil || m.actionCancel != nil || ctx.Err() == nil {
				t.Fatalf("cancel leaked: review=%v cancel=%v ctxErr=%v", m.cleanup, m.actionCancel != nil, ctx.Err())
			}
		})
	}
}

func TestCleanupUntickedItemIsNotRun(t *testing.T) {
	repo, merged, _ := tuiCleanupRepo(t)
	second := filepath.Join(t.TempDir(), "merged2")
	tuiGit(t, repo, "worktree", "add", "-b", "merged2", second)
	m := cleanupModel(t, repo)
	drain(m, m.key(key("c")))
	if m.cleanup == nil {
		t.Fatalf("no review: %q", m.message)
	}
	eligible := m.eligibleCleanup()
	if len(eligible) != 4 { // two worktrees, then their two branches
		t.Fatalf("want four eligible items, got %+v", eligible)
	}
	keep, remove := eligible[0].Path, eligible[1].Path
	m.key(key(" ")) // untick the first eligible item under the cursor
	drain(m, m.key(key("enter")))
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unticked worktree %s was touched: %v", keep, err)
	}
	if _, err := os.Stat(remove); !os.IsNotExist(err) {
		t.Fatalf("ticked worktree %s still exists", remove)
	}
	// The removed worktree and its branch succeed; the kept worktree's branch
	// is skipped because that worktree still has it checked out.
	if !strings.Contains(m.message, "2 succeeded · 1 skipped") {
		t.Fatalf("summary: %q", m.message)
	}
	_ = merged
}

func TestCleanupKeptListShowsFailedFirstAndNoFalsePointer(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.EnableCleanup(true)
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 35})
	items := []app.CleanupItem{}
	for i := 0; i < 6; i++ {
		items = append(items, app.CleanupItem{ID: fmt.Sprint("k", i), Group: "/repos/a", Kind: app.RemoveWorktree, Path: fmt.Sprint("/w/k", i), Reason: "dirty (1 files)"})
	}
	items = append(items, app.CleanupItem{ID: "fail", Group: "/repos/a", Kind: app.CleanupGroup, Path: "/repos/a", Reason: "preflight fetch failed: no route", Failed: true})
	m.cleanup = &app.CleanupPreview{ID: 1, Items: items}
	m.cleanupTicked = map[string]bool{}
	view := m.View().Content
	if !strings.Contains(view, "preflight fetch failed: no route") {
		t.Fatalf("failed item hidden:\n%s", view)
	}
	if !strings.Contains(view, "+3 more kept") || strings.Contains(view, "d details") {
		t.Fatalf("overflow text wrong:\n%s", view)
	}
}

func TestCleanupLateResultAfterCancelDoesNotOpenReview(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.EnableCleanup(true)
	ctx, cancel := context.WithCancel(context.Background())
	m.actionGeneration, m.actionCancel, m.actionCtx, m.preparing = 1, cancel, ctx, true
	cancel() // Esc arrived after the plan's last context check
	items := []app.CleanupItem{{ID: "a", Group: "/r", Kind: app.PruneStale, Path: "/r", Eligible: true}}
	m.cleanupMessage(cleanupMsg{generation: 1, preview: app.CleanupPreview{ID: 1, Items: items}})
	if m.cleanup != nil || m.message != "Cleanup preparation cancelled" || m.actionCancel != nil {
		t.Fatalf("review opened after cancel: %v %q", m.cleanup, m.message)
	}
}

func TestCleanupSummaryPointsToRestoreCommands(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.running, m.runningAction, m.results = true, app.Cleanup, map[string]app.Event{}
	m.Update(batchDoneMsg{generation: m.actionGeneration, results: []app.Event{{Path: "/r", Item: "delete branch\x00/r\x00/r\x00done", State: app.Succeeded, Message: "deleted done · restore: git branch done 0123456789abcdef0123456789abcdef01234567"}}})
	if !strings.Contains(m.message, "d restore commands") {
		t.Fatalf("summary: %q", m.message)
	}
	m.details, m.detailTab = true, 0
	if !strings.Contains(strings.Join(m.detailsContent(), "\n"), "restore: git branch done 0123456789abcdef") {
		t.Fatal("restore command missing from diagnostics")
	}
}

func TestCleanupSummaryOmitsRestoreHintWithoutDeletedBranch(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.running, m.runningAction, m.results = true, app.Cleanup, map[string]app.Event{}
	m.Update(batchDoneMsg{generation: m.actionGeneration, results: []app.Event{{Path: "/r", Item: "remove worktree\x00/r\x00/w", State: app.Succeeded, Message: "removed"}}})
	if strings.Contains(m.message, "restore") {
		t.Fatalf("summary: %q", m.message)
	}
}

func TestCleanupLabelShowsShortBranchCommit(t *testing.T) {
	m := New(context.Background(), nil, true)
	label := m.cleanupLabel(app.CleanupItem{Kind: app.DeleteBranch, Branch: "feat/x", OID: "0123456789abcdef0123456789abcdef01234567"})
	if !strings.Contains(label, "delete branch feat/x") || !strings.Contains(label, "0123456") || strings.Contains(label, "01234567") {
		t.Fatalf("label: %q", label)
	}
}

// A kept prune item's reason carries the command that saves a stranded commit,
// so it must reach the screen whole instead of being cut at the overlay edge.
func TestCleanupKeptReasonWrapsToShowRescueCommand(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.width, m.height = 110, 35
	oid := "890ae7e5c1d2b3a4f5e6d7c8b9a0f1e2d3c4b5a6"
	reason := "stale worktree feature-experiment-2026 holds unreferenced commit 890ae7e — create a branch first: git branch rescue/feature-experiment-2026 " + oid
	m.cleanup = &app.CleanupPreview{ID: 1, Items: []app.CleanupItem{{ID: "p", Group: "/r", Kind: app.PruneStale, Path: "/r", Stale: []string{"/w/feature-experiment-2026"}, Reason: reason}}}
	content := m.View().Content
	text := overlayText(content)
	for _, want := range []string{"feature-experiment-2026 holds unreferenced commit 890ae7e", "git branch rescue/feature-experiment-2026 " + oid, "Esc close"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, content)
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "…") {
			t.Fatalf("kept reason truncated: %q", line)
		}
	}
}

func TestCleanupKeptBranchOmitsCommitSoReasonFits(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	m.width, m.height = 110, 35
	m.cleanup = &app.CleanupPreview{ID: 1, Items: []app.CleanupItem{{ID: "b", Group: "/r", Kind: app.DeleteBranch, Path: "/r", Branch: "feat/squashed", OID: "4be81d09a7c3f5261e8d0b9c7a6f5e4d3c2b1a09", Reason: "upstream gone but not merged into origin/main — squash merge?"}}}
	content := m.View().Content
	if text := overlayText(content); !strings.Contains(text, "delete branch feat/squashed: upstream gone but not merged into origin/main — squash merge?") || strings.Contains(text, "4be81d0") {
		t.Fatalf("kept branch: %s", content)
	}
}

// overlayText joins the screen's lines without box borders and padding, so a
// sentence wrapped across popup lines can be matched as one string.
func overlayText(content string) string {
	var words []string
	for _, line := range strings.Split(content, "\n") {
		words = append(words, strings.Fields(strings.Map(func(r rune) rune {
			if strings.ContainsRune("│╭╮╰╯─", r) {
				return ' '
			}
			return r
		}, line))...)
	}
	return strings.Join(words, " ")
}

func TestRestoreCommandsCollectAcrossBatches(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	finish := func(results ...app.Event) {
		m.running, m.runningAction, m.results = true, app.Cleanup, map[string]app.Event{}
		m.Update(batchDoneMsg{generation: m.actionGeneration, results: results})
	}
	finish(app.Event{Item: "a", State: app.Succeeded, Restore: "git -C /r branch one 1111111111111111111111111111111111111111", RestoreLogged: true},
		app.Event{Item: "b", State: app.Skipped, Message: "checked out in wt"})
	if _, logged := m.RestoreCommands(); !logged {
		t.Fatal("first batch was logged")
	}
	finish(app.Event{Item: "c", State: app.Succeeded, Restore: "git -C /r branch two 2222222222222222222222222222222222222222"})
	got, logged := m.RestoreCommands()
	if len(got) != 2 || !strings.Contains(got[0], "branch one") || !strings.Contains(got[1], "branch two") || logged {
		t.Fatalf("restore commands: %q, logged %v", got, logged)
	}
}
