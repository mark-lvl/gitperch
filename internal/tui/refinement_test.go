package tui

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"strings"
	"testing"
)

func TestSmallWorkspaceKeepsPreviewNextToList(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.width, m.height = 160, 45
	lines := strings.Split(m.View().Content, "\n")
	last, preview := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "worker") {
			last = i
		}
		if strings.Contains(line, "╭") && last >= 0 && preview < 0 {
			preview = i
		}
	}
	if last < 0 || preview != last+1 {
		t.Fatalf("preview separated from list: %d -> %d", last, preview)
	}
}
func TestTabTogglesFocusAndRetainsRepository(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.highlight = 1
	path := m.highlightedRow().Path
	m.selected[path] = true
	for _, scope := range []int{1, 0, 1, 0} {
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		if m.scope != scope || m.highlightedRow().Path != path || len(m.selected) != 0 {
			t.Fatal("focus navigation lost context")
		}
	}
	m.scope = 1
	m.filter = "tokens"
	// The search excludes several attention rows; they are not 'healthy hidden'.
	if !strings.Contains(m.searchLineAt(100), "1 healthy hidden") {
		t.Fatal("healthy count included filtered attention rows")
	}
}
func TestPaletteWorkspaceActionsWorkFromDetails(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.details = true
	m.executeCommand("all")
	if len(m.selected) != len(m.rows) || m.details {
		t.Fatal("palette select-all swallowed by details")
	}
	m.details = true
	m.executeCommand("sort")
	if !m.attentionFirst || m.details {
		t.Fatal("palette sort swallowed by details")
	}
	m.executeCommand("scope:2")
	if m.scope != 2 || len(m.visibleRows()) != 2 {
		t.Fatal("secondary filters inaccessible")
	}
}
func TestEmptySearchEnterDoesNotOpenUnrelatedDetails(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.filtering = true
	m.filter = "no matching repository"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.details || !m.filtering {
		t.Fatal("empty search opened detail view")
	}
}
func TestContextScrollClampsAndReverses(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.width, m.height = 60, 20
	m.EnableDetails(func(context.Context, string) (gitcli.RepoDetails, error) { return gitcli.RepoDetails{}, nil })
	files := []gitcli.ChangedFile{}
	for i := 0; i < 20; i++ {
		files = append(files, gitcli.ChangedFile{Code: " M", Path: fmt.Sprintf("file-%02d.go", i)})
	}
	m.detailCache[m.highlightedRow().Path] = detailResult{data: gitcli.RepoDetails{Files: files}}
	for i := 0; i < 50; i++ {
		m.Update(key("]"))
	}
	if !strings.Contains(m.View().Content, "file-19.go") {
		t.Fatal("last file inaccessible")
	}
	before := m.contextOffset
	m.Update(key("["))
	if m.contextOffset != before-1 {
		t.Fatal("preview overshoot prevented reversing")
	}
	m.Update(key("j"))
	if m.contextOffset != 0 {
		t.Fatal("new repository inherited offset")
	}
}
func TestPatchLoadsOnlyInChangesAndRejectsCancelledResults(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	var contextUsed context.Context
	m.EnablePatch(func(ctx context.Context, path string) (string, error) {
		contextUsed = ctx
		return "+added\n-deleted\n", nil
	})
	if m.ensurePatch() != nil {
		t.Fatal("workspace fetched full patch")
	}
	m.details = true
	m.detailTab = 0
	if m.ensurePatch() != nil {
		t.Fatal("overview fetched full patch")
	}
	m.detailTab = 1
	cmd := m.ensurePatch()
	if cmd == nil {
		t.Fatal("Changes did not load patch")
	}
	msg := cmd()
	m.details = false
	m.ensurePatch()
	if contextUsed.Err() == nil {
		t.Fatal("leaving Changes did not cancel read")
	}
	m.Update(msg)
	if m.patch != nil {
		t.Fatal("cancelled patch replaced state")
	}
	m.details = true
	m.detailTab = 1
	cmd = m.ensurePatch()
	m.Update(cmd())
	if m.patch == nil || len(m.patch.lines) != 2 || m.ensurePatch() != nil {
		t.Fatal("patch not cached")
	}
	m.applySnapshot(app.Snapshot{Rows: testRows()})
	if m.patch != nil {
		t.Fatal("refresh retained obsolete patch")
	}
}
func TestPaletteFitsEverySupportedWidth(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {110, 35}, {78, 28}, {60, 20}, {60, 12}} {
		m := New(nil, nil, true)
		m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
		m.width, m.height = size[0], size[1]
		m.palette = true
		content := m.View().Content
		if !strings.Contains(content, "Command Palette") || !strings.Contains(content, "Esc") {
			t.Fatalf("palette lost navigation: %v", size)
		}
		if len(strings.Split(content, "\n")) > size[1] {
			t.Fatal("palette overflow")
		}
	}
}

func TestThemeDegradesToTerminalColorProfiles(t *testing.T) {
	m := New(nil, nil, false)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	raw := m.View().Content
	for _, profile := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.ANSI, colorprofile.NoTTY} {
		var out bytes.Buffer
		writer := colorprofile.Writer{Forward: &out, Profile: profile}
		if _, err := writer.WriteString(raw); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), ";2;") {
			t.Fatal("truecolor leaked into limited terminal profile")
		}
		if profile == colorprofile.ANSI && (strings.Contains(out.String(), "38;5;") || strings.Contains(out.String(), "48;5;")) {
			t.Fatal("indexed colors leaked into basic ANSI")
		}
		if ansi.Strip(out.String()) != ansi.Strip(raw) {
			t.Fatal("color fallback changed content")
		}
	}
}
func TestSuggestedPushWorksInDetails(t *testing.T) {
	m := New(nil, nil, true)
	m.applySnapshot(app.Snapshot{Rows: dashboardRows()})
	m.highlight = 1
	m.details = true
	m.EnableActions(app.NewActions(newActionFake(false), 1))
	_, cmd := m.Update(key("p"))
	if cmd == nil || !m.preparing || m.actionTotal != 1 || len(m.selected) != 0 {
		t.Fatal("suggested push did not open guarded review")
	}
	if m.actionCancel != nil {
		m.actionCancel()
	}
}
