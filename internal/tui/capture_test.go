package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureNow pins the header clock and relative ages in reviewed captures.
var captureNow = time.Date(2026, 10, 5, 19, 42, 0, 0, time.UTC)

func captureRows() []app.Row {
	rows := dashboardRows()
	for i, age := range []time.Duration{3 * time.Hour, 2 * time.Minute, 26 * time.Hour, 11 * time.Minute, 0, 7 * time.Minute} {
		if age > 0 {
			rows[i].Status.LastActivity = captureNow.Add(-age)
		}
	}
	// design-system owns two linked worktrees: one clean, one stale.
	main := rows[1].Path
	rows[1].Worktree = &app.WorktreeInfo{Main: true, MainPath: main}
	fixTokens := app.Row{Repository: repository.Repository{Name: "fix-tokens", Path: main + "/.worktrees/fix-tokens"}, Status: repository.Status{Branch: "fix-tokens"}, Worktree: &app.WorktreeInfo{Linked: true, MainPath: main}}
	oldSpike := app.Row{Repository: repository.Repository{Name: "old-spike", Path: "/home/mark/worktrees/old-spike"}, Status: repository.Status{Branch: "old-spike"}, Worktree: &app.WorktreeInfo{Linked: true, Prunable: true, PrunableReason: "gitdir file points to non-existent location", OutsideRoots: true, MainPath: main}}
	return append(rows, fixTokens, oldSpike)
}

func captureCommits() []gitcli.Commit {
	return []gitcli.Commit{{OID: "29ac81", Subject: "Add semantic theme tokens", Time: captureNow.Add(-12 * time.Minute)}, {OID: "fe2034", Subject: "Refactor shared components", Time: captureNow.Add(-time.Hour)}}
}

// writeANSICapture exports color output for scripts/render-captures.py into
// GITPERCH_ANSI_DIR, a directory the caller creates privately (mktemp -d).
func writeANSICapture(t *testing.T, name, content string) {
	t.Helper()
	dir := os.Getenv("GITPERCH_ANSI_DIR")
	if dir == "" {
		t.Log("GITPERCH_ANSI_DIR unset; skipping color export for PNG rendering")
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// captureCleanup is a fixed review of the design-system group in captureRows:
// its stale old-spike record, its merged fix-tokens worktree, two merged
// branches and one kept branch whose upstream is gone. Reasons repeat what the
// engine produces. It never touches Git.
func captureCleanup(main string) *app.CleanupPreview {
	base := "origin/main"
	branch := func(id, name, oid string, eligible bool, reason string) app.CleanupItem {
		return app.CleanupItem{ID: id, Group: main, Kind: app.DeleteBranch, Path: main, Branch: name, OID: oid, BaseName: base, Eligible: eligible, Reason: reason}
	}
	return &app.CleanupPreview{ID: 1, Items: []app.CleanupItem{
		{ID: "prune", Group: main, Kind: app.PruneStale, Path: main, Stale: []string{"/home/mark/worktrees/old-spike"}, Eligible: true, Reason: "directory missing: old-spike"},
		{ID: "fix-tokens", Group: main, Kind: app.RemoveWorktree, Path: main + "/.worktrees/fix-tokens", Branch: "fix-tokens", BaseName: base, Eligible: true, Reason: "merged into " + base},
		branch("branch-fix-tokens", "fix-tokens", "2d7a90c3e1f4856b0a9d8c7e6f5a4b3c2d1e0f9a", true, "merged into "+base),
		branch("branch-old-login", "feat/old-login", "9f3c2a7d41b86e05c1d2f3a4b5c6d7e8f9a0b1c2", true, "merged into "+base),
		branch("branch-squashed", "feat/squashed", "4be81d09a7c3f5261e8d0b9c7a6f5e4d3c2b1a09", false, "upstream gone but not merged into "+base+" — squash merge?"),
	}}
}

// Captures use deterministic test fixtures only; production always loads Git.
func TestWorkspaceRenderCaptures(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {110, 35}, {78, 28}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := New(context.Background(), nil, true)
			m.lazyGitAvailable = false
			m.Configure("~/dev/platform", "unicode", false)
			m.clock = func() time.Time { return captureNow }
			m.applySnapshot(app.Snapshot{Rows: captureRows()})
			m.EnableActions(app.NewActions(newActionFake(false), 1))
			m.EnableCleanup(true)
			m.EnableDetails(func(context.Context, string) (gitcli.RepoDetails, error) { return gitcli.RepoDetails{}, nil })
			m.highlight = 1
			m.detailCache[m.highlightedRow().Path] = detailResult{data: gitcli.RepoDetails{Commits: captureCommits(), Files: []gitcli.ChangedFile{{Code: " M", Path: "src/theme/tokens.go", Added: 8, Deleted: 2}, {Code: " M", Path: "src/components/button.go", Added: 4}, {Code: "??", Path: "tests/theme_test.go"}}}}
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			content := m.View().Content
			if os.Getenv("UPDATE_RENDERS") == "1" {
				m.noColor = false
				writeANSICapture(t, fmt.Sprintf("workspace-%dx%d", size[0], size[1]), m.View().Content)
				m.noColor = true
			}
			// Trim trailing padding to keep checked-in artifacts readable.
			lines := strings.Split(content, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " ")
			}
			content = strings.Join(lines, "\n") + "\n"
			path := filepath.Join("..", "..", "docs", "captures", fmt.Sprintf("workspace-%dx%d.txt", size[0], size[1]))
			if os.Getenv("UPDATE_RENDERS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			expected, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(expected) != content {
				t.Fatalf("render changed; review with UPDATE_RENDERS=1 go test ./internal/tui -run TestWorkspaceRenderCaptures")
			}
		})
	}
}

// The first scan shows the terminal mark in the empty list.
func TestScanningRenderCapture(t *testing.T) {
	m := New(nil, nil, true)
	m.Configure("~/dev/platform", "unicode", false)
	m.clock = func() time.Time { return captureNow }
	m.loading = true
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	content := m.View().Content
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	content = strings.Join(lines, "\n") + "\n"
	path := filepath.Join("..", "..", "docs", "captures", "scanning-80x24.txt")
	if os.Getenv("UPDATE_RENDERS") == "1" {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		m.noColor = false
		writeANSICapture(t, "scanning-80x24", m.View().Content)
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(expected) != content {
		t.Fatal("scanning view changed; review with UPDATE_RENDERS=1 go test ./internal/tui -run TestScanningRenderCapture")
	}
}

func TestOverlayRenderCaptures(t *testing.T) {
	for _, mode := range []string{"palette", "details", "worktrees", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			m := New(nil, nil, true)
			m.Configure("~/dev/platform", "unicode", false)
			m.clock = func() time.Time { return captureNow }
			m.applySnapshot(app.Snapshot{Rows: captureRows()})
			m.highlight = 1
			m.width, m.height = 110, 35
			m.lazyGitAvailable = false
			m.EnableActions(app.NewActions(newActionFake(false), 1))
			m.EnableCleanup(true)
			m.EnableDetails(func(context.Context, string) (gitcli.RepoDetails, error) { return gitcli.RepoDetails{}, nil })
			m.detailCache[m.highlightedRow().Path] = detailResult{data: gitcli.RepoDetails{Files: []gitcli.ChangedFile{{Code: " M", Path: "src/theme/tokens.go", Added: 8, Deleted: 2}, {Code: "??", Path: "tests/theme_test.go"}}, Commits: captureCommits()}}
			switch mode {
			case "palette":
				m.palette = true
				m.paletteQuery = "push"
			case "worktrees":
				m.expanded["/home/mark/projects/design-system"] = true
			case "cleanup":
				m.cleanup = captureCleanup(m.highlightedRow().Path)
				m.cleanupTicked = map[string]bool{}
				for _, item := range m.cleanup.Items {
					m.cleanupTicked[item.ID] = item.Eligible
				}
				m.cleanupCursor = 1
			default:
				m.details = true
			}
			content := m.View().Content
			lines := strings.Split(content, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " ")
			}
			content = strings.Join(lines, "\n") + "\n"
			name := mode + "-110x35"
			path := filepath.Join("..", "..", "docs", "captures", name+".txt")
			if os.Getenv("UPDATE_RENDERS") == "1" {
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
				m.noColor = false
				writeANSICapture(t, name, m.View().Content)
			}
			expected, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(expected) != content {
				t.Fatal("overlay changed; review with UPDATE_RENDERS=1")
			}
		})
	}
}
