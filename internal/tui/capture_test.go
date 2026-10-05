package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"strings"
	"testing"
)

// Captures use deterministic test fixtures only; production always loads Git.
func TestWorkspaceRenderCaptures(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {110, 35}, {78, 28}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := New(context.Background(), nil, true)
			m.lazyGitAvailable = false
			m.Configure("~/dev/platform", "unicode", false)
			m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
			m.EnableActions(app.NewActions(newActionFake(false), 1))
			m.EnableDetails(func(context.Context, string) (gitcli.RepoDetails, error) { return gitcli.RepoDetails{}, nil })
			m.highlight = 1
			m.detailCache[m.highlightedRow().Path] = detailResult{data: gitcli.RepoDetails{Commits: []gitcli.Commit{{OID: "29ac81", Subject: "Add semantic theme tokens"}, {OID: "fe2034", Subject: "Refactor shared components"}}, Files: []gitcli.ChangedFile{{Code: " M", Path: "src/theme/tokens.go", Added: 8, Deleted: 2}, {Code: " M", Path: "src/components/button.go", Added: 4}, {Code: "??", Path: "tests/theme_test.go"}}}}
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			content := m.View().Content
			if os.Getenv("UPDATE_RENDERS") == "1" {
				m.noColor = false
				path := filepath.Join(os.TempDir(), fmt.Sprintf("repodash-%dx%d.ansi", size[0], size[1]))
				if err := os.WriteFile(path, []byte(m.View().Content), 0644); err != nil {
					t.Fatal(err)
				}
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

func TestOverlayRenderCaptures(t *testing.T) {
	for _, mode := range []string{"palette", "details"} {
		t.Run(mode, func(t *testing.T) {
			m := New(nil, nil, true)
			m.Configure("~/dev/platform", "unicode", false)
			m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
			m.highlight = 1
			m.width, m.height = 110, 35
			m.lazyGitAvailable = false
			m.EnableActions(app.NewActions(newActionFake(false), 1))
			m.EnableDetails(func(context.Context, string) (gitcli.RepoDetails, error) { return gitcli.RepoDetails{}, nil })
			m.detailCache[m.highlightedRow().Path] = detailResult{data: gitcli.RepoDetails{Files: []gitcli.ChangedFile{{Code: " M", Path: "src/theme/tokens.go", Added: 8, Deleted: 2}, {Code: "??", Path: "tests/theme_test.go"}}, Commits: []gitcli.Commit{{OID: "29ac81", Subject: "Add semantic theme tokens"}, {OID: "fe2034", Subject: "Refactor shared components"}}}}
			if mode == "palette" {
				m.palette = true
				m.paletteQuery = "push"
			} else {
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
				if err := os.WriteFile(filepath.Join(os.TempDir(), "repodash-"+name+".ansi"), []byte(m.View().Content), 0644); err != nil {
					t.Fatal(err)
				}
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
