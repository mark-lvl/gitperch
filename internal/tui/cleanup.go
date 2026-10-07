package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

type cleanupMsg struct {
	generation uint64
	preview    app.CleanupPreview
	err        error
}

// resultKey keys a batch result: cleanup items have an ID, repository actions
// are keyed by path.
func resultKey(e app.Event) string {
	if e.Item != "" {
		return e.Item
	}
	return e.Path
}

// cleanupPaths names one existing worktree per targeted group: the parent,
// or its first existing child when the parent is bare or stale.
func (m *Model) cleanupPaths() []string {
	groups := map[string]bool{}
	for path := range m.selected {
		for _, row := range m.rows {
			if row.Path == path {
				groups[groupKey(row)] = true
			}
		}
	}
	if len(groups) == 0 {
		if row := m.highlightedRow(); row != nil {
			groups[groupKey(*row)] = true
		}
	}
	var paths []string
	for key := range groups {
		chosen := ""
		for _, row := range m.rows {
			if groupKey(row) != key || !row.Selectable() {
				continue
			}
			if row.Path == key || chosen == "" {
				chosen = row.Path
			}
		}
		if chosen != "" {
			paths = append(paths, chosen)
		}
	}
	sort.Strings(paths)
	return paths
}

// EnableCleanup shows Clean up only where Git can list worktrees safely.
func (m *Model) EnableCleanup(supported bool) { m.cleanupSupported = supported }

func (m *Model) prepareCleanup() tea.Cmd {
	if m.actions == nil {
		return nil
	}
	if !m.cleanupSupported {
		m.message = "Clean up needs Git 2.36 or newer"
		return nil
	}
	paths := m.cleanupPaths()
	if len(paths) == 0 {
		m.message = "Nothing to clean up here"
		return nil
	}
	if m.loadCancel != nil {
		m.loadCancel()
		m.loadCancel = nil
	}
	m.generation++
	m.refreshInterrupted = m.refreshInterrupted || m.loading
	m.loading = false
	m.actionGeneration++
	generation := m.actionGeneration
	ctx, cancel := context.WithCancel(m.ctx)
	m.actionCancel, m.actionCtx = cancel, ctx
	m.preparing = true
	m.details, m.help, m.palette = false, false, false
	m.message = "Fetching and checking " + plural(len(paths), "repository", "repositories") + " for cleanup"
	actions := m.actions
	return func() tea.Msg {
		preview, err := actions.PlanCleanup(ctx, paths)
		return cleanupMsg{generation, preview, err}
	}
}

func (m *Model) cleanupMessage(msg cleanupMsg) tea.Cmd {
	if msg.generation != m.actionGeneration {
		m.actions.Discard(msg.preview.ID)
		return nil
	}
	m.preparing = false
	// Every path that does not open a review releases the cancel function.
	release := func() {
		if m.actionCancel != nil {
			m.actionCancel()
			m.actionCancel = nil
		}
	}
	cancelled := m.actionCtx != nil && m.actionCtx.Err() != nil
	if cancelled && (msg.err == nil || errors.Is(msg.err, context.Canceled)) {
		m.actions.Discard(msg.preview.ID)
		m.message = "Cleanup preparation cancelled"
		release()
		return m.resumeInterruptedRefresh()
	}
	if msg.err != nil {
		m.message = gitcli.SafeText(msg.err.Error())
		release()
		return m.resumeInterruptedRefresh()
	}
	if len(msg.preview.Items) == 0 {
		m.actions.Discard(msg.preview.ID)
		m.message = "Nothing to clean up"
		release()
		return m.resumeInterruptedRefresh()
	}
	m.cleanup = &msg.preview
	m.cleanupTicked = map[string]bool{}
	for _, item := range msg.preview.Items {
		if item.Eligible() {
			m.cleanupTicked[item.ID] = true
		}
		if item.Failed() {
			m.actionFailed = true
		}
	}
	m.cleanupCursor = 0
	m.message = ""
	return nil
}

func (m *Model) eligibleCleanup() []app.CleanupItem {
	var items []app.CleanupItem
	for _, item := range m.cleanup.Items {
		if item.Eligible() {
			items = append(items, item)
		}
	}
	return items
}

func (m *Model) cleanupKey(key string) tea.Cmd {
	eligible := m.eligibleCleanup()
	switch key {
	case "j", "down":
		m.cleanupCursor = min(max(0, len(eligible)-1), m.cleanupCursor+1)
	case "k", "up":
		m.cleanupCursor = max(0, m.cleanupCursor-1)
	case " ", "space":
		if m.cleanupCursor < len(eligible) {
			id := eligible[m.cleanupCursor].ID
			m.cleanupTicked[id] = !m.cleanupTicked[id]
		}
	case "a":
		all := true
		for _, item := range eligible {
			all = all && m.cleanupTicked[item.ID]
		}
		for _, item := range eligible {
			m.cleanupTicked[item.ID] = !all
		}
	case "enter":
		var chosen []string
		for _, item := range eligible {
			if m.cleanupTicked[item.ID] {
				chosen = append(chosen, item.ID)
			}
		}
		if len(chosen) == 0 {
			m.message = "Nothing ticked · Space ticks an item, Esc closes"
			return nil
		}
		return m.runCleanup(chosen)
	case "esc", "q", "ctrl+c":
		m.actions.Discard(m.cleanup.ID)
		m.cleanup = nil
		if m.actionCancel != nil {
			m.actionCancel()
			m.actionCancel = nil
		}
		m.message = "Cancelled"
		return m.resumeInterruptedRefresh()
	}
	return nil
}

func (m *Model) runCleanup(chosen []string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	if m.actionCancel != nil {
		m.actionCancel()
	}
	m.actionCancel, m.actionCtx = cancel, ctx
	m.running = true
	m.runningAction = app.Cleanup
	m.actionTotal = len(chosen)
	m.message = ""
	m.results = map[string]app.Event{}
	id, generation, actions := m.cleanup.ID, m.actionGeneration, m.actions
	m.cleanup = nil
	events := make(chan app.Event, 64)
	m.events = events
	run := func() tea.Msg {
		results, err := actions.ExecuteCleanup(ctx, id, chosen, func(event app.Event) { events <- event })
		close(events)
		return batchDoneMsg{generation: generation, results: results, err: err}
	}
	return tea.Batch(run, m.nextEvent())
}

// cleanupGroupColor matches a review heading to its repository's icon in the
// list. Cleanup names the group by the path Git reports, which is the parent
// row's path unless symlinks differ; then the path itself picks the color.
func (m *Model) cleanupGroupColor(group string) string {
	for _, row := range m.rows {
		if row.Path == group {
			return m.groupColor(row)
		}
	}
	return identityColor(group)
}

func (m *Model) cleanupLabel(item app.CleanupItem) string { return m.cleanupItemLabel(item, true) }

// cleanupItemLabel names an item; withOID adds a branch's short commit, which
// the kept list leaves out so the reason keeps its room.
func (m *Model) cleanupItemLabel(item app.CleanupItem, withOID bool) string {
	switch item.Kind {
	case app.PruneStale:
		return fmt.Sprintf("prune %s", plural(len(item.Stale), "stale worktree record", "stale worktree records"))
	case app.RemoveWorktree:
		label := "remove worktree " + gitcli.SafeText(filepath.Base(item.Path))
		if item.Branch != "" {
			label += "  " + m.style(gitcli.SafeText(item.Branch), muted, false)
		}
		return label
	case app.DeleteBranch:
		label := "delete branch " + gitcli.SafeText(item.Branch)
		if withOID {
			label += "  " + m.style(shortCommit(item.OID), muted, false)
		}
		return label
	}
	return gitcli.SafeText(m.targetName(item.Path))
}

// wrapSpaces wraps text at spaces only: ansi.Wrap also breaks after hyphens,
// which would split a command such as "git branch rescue/old-spike <commit>".
// The first line holds first cells and later lines rest (rest <= first); a
// word longer than its line is cut at rest cells.
func wrapSpaces(text string, first, rest int) []string {
	words := strings.Split(text, " ")
	var lines []string
	line, limit := words[0], first
	flush := func() {
		if ansi.StringWidth(line) > limit {
			lines = append(lines, strings.Split(ansi.Hardwrap(line, rest, true), "\n")...)
		} else {
			lines = append(lines, line)
		}
		limit = rest
	}
	for _, word := range words[1:] {
		if ansi.StringWidth(line+" "+word) <= limit {
			line += " " + word
			continue
		}
		flush()
		line = word
	}
	flush()
	return lines
}

// cleanupChecks lists every fact an assessment rests on, such as
// "✓ working tree clean · no ignored files · merged into origin/main".
func (m *Model) cleanupChecks(item app.CleanupItem) string {
	var facts []string
	for _, r := range item.Assessment.Reasons {
		facts = append(facts, r.Text)
	}
	return m.symbols().clean + " " + strings.Join(facts, " · ")
}

// cleanupView lists ticked-by-default eligible items per repository, then
// what stays and why. Only ticked item IDs reach ExecuteCleanup.
func (m *Model) cleanupView() tea.View {
	eligible := m.eligibleCleanup()
	groups := map[string]bool{}
	for _, item := range m.cleanup.Items {
		groups[item.Group] = true
	}
	title := fmt.Sprintf("Clean up %s?", plural(len(eligible), "item", "items"))
	if len(groups) > 1 {
		title = fmt.Sprintf("Clean up %s in %s?", plural(len(eligible), "item", "items"), plural(len(groups), "repository", "repositories"))
	}
	w := min(96, m.width-4)
	inner := max(1, w-4)
	lines := []string{m.style(title, ink, true)}
	// One line is reserved for the highlighted item's checks.
	room := max(3, m.height-11)
	start := max(0, min(m.cleanupCursor-room/2, len(eligible)-room))
	group := ""
	for i := start; i < min(len(eligible), start+room); i++ {
		item := eligible[i]
		if item.Group != group {
			group = item.Group
			lines = append(lines, m.style(m.symbols().repo, m.cleanupGroupColor(group), false)+" "+m.style(gitcli.SafeText(m.targetName(group)), accent, true))
		}
		box := "[ ]"
		if m.cleanupTicked[item.ID] {
			box = "[x]"
		}
		pointer := "  "
		if i == m.cleanupCursor {
			pointer = m.symbols().pointer + " "
		}
		lines = append(lines, m.between(pointer+box+" "+m.cleanupLabel(item), m.style(item.Assessment.Summary(), muted, false), inner))
		if i == m.cleanupCursor && len(item.Assessment.Reasons) > 1 {
			lines = append(lines, m.style(ansi.Truncate("      "+m.cleanupChecks(item), inner, "…"), muted, false))
		}
	}
	// Failed planning steps come first and always show. The other kept items
	// follow from most to least restrictive and fill the remaining budget of
	// four lines.
	var kept []app.CleanupItem
	failed := 0
	for _, item := range m.cleanup.Items {
		if !item.Eligible() && item.Failed() {
			kept = append(kept, item)
			failed++
		}
	}
	for _, status := range []app.CleanupStatus{app.CleanupBlocked, app.CleanupUnknown, app.CleanupNeedsReview, ""} {
		for _, item := range m.cleanup.Items {
			if !item.Eligible() && !item.Failed() && item.Assessment.Status == status {
				kept = append(kept, item)
			}
		}
	}
	shown := max(4, failed)
	for i, item := range kept {
		if i == 0 {
			lines = append(lines, "", m.style("kept", amber, true))
		}
		if i == shown {
			lines = append(lines, m.style(fmt.Sprintf("  +%d more kept", len(kept)-shown), muted, false))
			break
		}
		color, tag := amber, item.Assessment.Status.Label()
		switch {
		case item.Failed():
			color, tag = danger, "failed"
		case item.Assessment.Status == app.CleanupNeedsReview:
			color = muted
		}
		// Reasons wrap instead of truncating: a kept prune item ends with the
		// command that saves a stranded commit, which must stay readable.
		// The first line keeps the full width; continuations indent further.
		text := fmt.Sprintf("%-7s  %s: %s", tag, m.cleanupItemLabel(item, false), item.Assessment.Summary())
		for j, line := range wrapSpaces(text, max(1, inner-2), max(1, inner-4)) {
			indent := "    "
			if j == 0 {
				indent = "  "
			}
			lines = append(lines, m.style(indent+line, color, false))
		}
	}
	footer := "Space toggle · ↑↓ move · " + m.symbols().enter + " clean up · Esc cancel"
	if len(eligible) == 0 {
		footer = "Nothing can be cleaned up safely · Esc close"
	}
	lines = append(lines, "", m.style(footer, muted, false))
	if m.width < 24 || m.height < 6 {
		return m.screen(lines)
	}
	return m.overlay(lines, w)
}
