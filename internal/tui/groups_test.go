package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
)

func groupedRows() []app.Row {
	main := "/home/mark/projects/api"
	info := func(linked bool) *app.WorktreeInfo {
		return &app.WorktreeInfo{Main: !linked, Linked: linked, MainPath: main}
	}
	stale := info(true)
	stale.Prunable, stale.PrunableReason = true, "gitdir file points to non-existent location"
	return []app.Row{
		{Repository: repository.Repository{Name: "api", Path: main}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}, Worktree: info(false)},
		{Repository: repository.Repository{Name: "fix-auth", Path: main + "/.claude/worktrees/fix-auth"}, Status: repository.Status{Branch: "fix-auth", Changes: 2}, Worktree: info(true)},
		{Repository: repository.Repository{Name: "old", Path: "/tmp/old"}, Status: repository.Status{Branch: "old"}, Worktree: stale},
		{Repository: repository.Repository{Name: "web", Path: "/home/mark/projects/web"}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}},
	}
}

func visibleNames(m *Model) []string {
	var names []string
	for _, i := range m.visibleRows() {
		names = append(names, m.rows[i].Name)
	}
	return names
}

func TestGroupsCollapseAndExpand(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	if got := strings.Join(visibleNames(m), ","); got != "api,web" {
		t.Fatalf("collapsed: %s", got)
	}
	m.key(key("right"))
	if got := strings.Join(visibleNames(m), ","); got != "api,old,fix-auth,web" && got != "api,fix-auth,old,web" {
		t.Fatalf("expanded: %s", got)
	}
	m.highlight = 2
	m.key(key("left"))
	if m.highlightedRow().Name != "api" {
		t.Fatalf("left on a child should jump to its parent, got %s", m.highlightedRow().Name)
	}
	m.key(key("left"))
	if got := strings.Join(visibleNames(m), ","); got != "api,web" {
		t.Fatalf("collapsed again: %s", got)
	}
}

func TestAttentionSortsGroupsByNeediestMember(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.attentionFirst = true
	rows := groupedRows()
	rows = append(rows, app.Row{Repository: repository.Repository{Name: "aaa", Path: "/home/mark/projects/aaa"}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true, Behind: 1}})
	m.applySnapshot(app.Snapshot{Rows: rows})
	if got := visibleNames(m)[0]; got != "api" {
		t.Fatalf("group with a dirty child should rank first, got %s", got)
	}
}

func TestSearchRevealsChildUnderContextParent(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.filter = "fix-auth"
	if got := strings.Join(visibleNames(m), ","); got != "api,fix-auth" {
		t.Fatalf("filtered: %s", got)
	}
	if !m.isContext(m.visibleRows()[0]) || m.isContext(m.visibleRows()[1]) {
		t.Fatal("parent should be context, child a match")
	}
	m.key(key("a"))
	if len(m.selected) != 1 || !m.selected["/home/mark/projects/api/.claude/worktrees/fix-auth"] {
		t.Fatalf("select-all must skip context parents: %v", m.selected)
	}
}

func TestStaleRowsAreNotSelectable(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.key(key("right"))
	for i, index := range m.visibleRows() {
		if m.rows[index].Name == "old" {
			m.highlight = i
		}
	}
	m.key(key(" "))
	if len(m.selected) != 0 || !strings.Contains(m.message, "Clean up") {
		t.Fatalf("stale row selected: %v, message %q", m.selected, m.message)
	}
}

func TestCollapsedParentShowsBadgeAndChildrenShowTree(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	view := m.View().Content
	if !strings.Contains(view, "⑂2") || !strings.Contains(view, "1 stale") {
		t.Fatalf("badge missing:\n%s", view)
	}
	m.key(key("right"))
	view = m.View().Content
	if !strings.Contains(view, "├─ ") || !strings.Contains(view, "└─ ") || !strings.Contains(view, "stale") {
		t.Fatalf("tree missing:\n%s", view)
	}
}

func highlightStale(m *Model) {
	m.key(key("right"))
	for i, index := range m.visibleRows() {
		if m.rows[index].Name == "old" {
			m.highlight = i
		}
	}
}

func TestStaleRowsSkipDirectoryActions(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.EnableDetails(func(context.Context, string) (gitcli.RepoDetails, error) { return gitcli.RepoDetails{}, nil })
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	highlightStale(m)
	if cmd := m.ensureDetail(); cmd != nil {
		t.Fatal("details must not load for a stale row")
	}
	if cmd := m.launchShell(); cmd != nil || !strings.Contains(m.message, "no directory") {
		t.Fatalf("shell on stale row: %q", m.message)
	}
	m.message = ""
	if cmd := m.launchLazyGit(); cmd != nil || !strings.Contains(m.message, "no directory") {
		t.Fatalf("lazygit on stale row: %q", m.message)
	}
}

func TestPromotedChildWhenParentRowIsAbsent(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()[1:2]})
	if got := strings.Join(visibleNames(m), ","); got != "fix-auth" {
		t.Fatalf("promoted: %s", got)
	}
	if m.treePrefix(0, m.visibleRows()) != "" {
		t.Fatal("promoted child stands as the group's first row")
	}
}

const fixAuthPath = "/home/mark/projects/api/.claude/worktrees/fix-auth"

func selectFixAuth(t *testing.T, m *Model) {
	t.Helper()
	m.key(key("right"))
	for i, index := range m.visibleRows() {
		if m.rows[index].Name == "fix-auth" {
			m.highlight = i
		}
	}
	m.key(key(" "))
	if !m.selected[fixAuthPath] {
		t.Fatalf("child not selected: %v", m.selected)
	}
}

func TestCollapseDropsHiddenChildSelection(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	selectFixAuth(t, m)
	m.key(key("left")) // to the parent
	m.key(key("left")) // collapse
	if len(m.selected) != 0 || len(m.selectedPaths()) != 0 {
		t.Fatalf("hidden child still selected: %v", m.selected)
	}
}

func TestRefreshDropsSelectionHiddenByCollapse(t *testing.T) {
	m := New(context.Background(), nil, true)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	selectFixAuth(t, m)
	delete(m.expanded, "/home/mark/projects/api") // collapse without going through the key
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	if len(m.selected) != 0 {
		t.Fatalf("refresh kept a hidden selection: %v", m.selected)
	}
	// A still-visible selection survives a refresh.
	m.highlight = 0
	selectFixAuth(t, m)
	m.applySnapshot(app.Snapshot{Rows: groupedRows()})
	if !m.selected[fixAuthPath] {
		t.Fatalf("visible selection lost: %v", m.selected)
	}
}
