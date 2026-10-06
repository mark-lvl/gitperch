package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type previewMsg struct {
	generation uint64
	preview    app.Preview
	err        error
}
type progressMsg struct {
	generation uint64
	event      app.Event
}
type batchDoneMsg struct {
	generation uint64
	results    []app.Event
	err        error
}

func (m *Model) EnableActions(actions *app.Actions) { m.actions = actions }

func (m *Model) actionMessage(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case previewMsg:
		if msg.generation != m.actionGeneration {
			if m.actions != nil {
				m.actions.Discard(msg.preview.ID)
			}
			return true, nil
		}
		m.preparing = false
		if msg.err == nil && m.actionCtx != nil && m.actionCtx.Err() != nil {
			m.actions.Discard(msg.preview.ID)
			m.message = "Preview preparation cancelled"
			return true, nil
		}
		if msg.err != nil {
			m.message = msg.err.Error()
			if m.actionCancel != nil {
				m.actionCancel()
				m.actionCancel = nil
			}
			return true, nil
		}
		m.preview = &msg.preview
		for _, target := range msg.preview.Targets {
			if target.PreflightFailed {
				m.actionFailed = true
			}
		}
		return true, m.settlePreview()
	case progressMsg:
		if msg.generation != m.actionGeneration || !m.running {
			return true, nil
		}
		m.results[msg.event.Path] = msg.event
		return true, m.nextEvent()
	case batchDoneMsg:
		if msg.generation != m.actionGeneration {
			return true, nil
		}
		m.running = false
		m.events = nil
		for _, result := range msg.results {
			m.results[result.Path] = result
			if result.State == app.Failed || result.State == app.OutcomeUnknown || result.State == app.Cancelled {
				m.actionFailed = true
			}
		}
		if m.actionCancel != nil {
			m.actionCancel()
			m.actionCancel = nil
		}
		succeeded, skipped, failed := 0, 0, 0
		for _, result := range msg.results {
			switch result.State {
			case app.Succeeded:
				succeeded++
			case app.Skipped:
				skipped++
			default:
				failed++
			}
		}
		m.message = fmt.Sprintf("%s finished · %d succeeded", capitalize(string(m.runningAction)), succeeded)
		if skipped > 0 {
			m.message += fmt.Sprintf(" · %d skipped", skipped)
		}
		if failed > 0 {
			m.message += fmt.Sprintf(" · %d failed/uncertain", failed)
		}
		if skipped+failed > 0 {
			m.message += " · d details"
		}
		if msg.err != nil {
			m.actionFailed = true
			m.message += " · " + msg.err.Error()
		}
		m.preview = nil
		return true, m.refresh()
	}
	return false, nil
}

func (m *Model) preparePreview(action app.Action, paths []string) tea.Cmd {
	if len(paths) == 0 {
		m.message = "Select repositories explicitly with Space or a before an action"
		return nil
	}
	sort.Strings(paths)
	m.actionTotal = len(paths)
	if m.loadCancel != nil {
		m.loadCancel()
		m.loadCancel = nil
	}
	m.generation++
	m.loading = false
	m.refreshUntil = time.Time{}
	m.actionGeneration++
	generation := m.actionGeneration
	ctx, cancel := context.WithCancel(m.ctx)
	m.actionCancel = cancel
	m.actionCtx = ctx
	m.preparing = true
	m.details = false
	m.help = false
	verb := action
	if m.syncIntent != "" {
		verb = m.syncIntent
	}
	m.message = fmt.Sprintf("Preparing %s for %s", verb, plural(len(paths), "repository", "repositories"))
	actions := m.actions
	return func() tea.Msg {
		preview, err := actions.Plan(ctx, action, paths)
		return previewMsg{generation: generation, preview: preview, err: err}
	}
}

// settlePreview asks only where a confirmation protects something. Fetch runs
// at once, including the fetch that scopes a push or pull, and a plan with
// nothing eligible closes with its reason instead of opening a popup.
func (m *Model) settlePreview() tea.Cmd {
	eligible, reason := 0, ""
	for _, target := range m.preview.Targets {
		if target.Eligible {
			eligible++
		} else if reason == "" {
			reason = target.Reason
		}
	}
	action := app.Fetch
	if m.syncIntent != "" {
		action = m.syncIntent
	} else if len(m.preview.Targets) > 0 {
		action = m.preview.Targets[0].Action
	}
	if eligible == 0 {
		m.discardPreview()
		m.message = "Nothing to " + string(action)
		if reason != "" {
			m.message += ": " + gitcli.SafeText(reason)
		}
		return nil
	}
	if m.preview.Targets[0].Action == app.Fetch {
		return m.confirmPreview()
	}
	m.message = ""
	return nil
}

func (m *Model) discardPreview() {
	m.actions.Discard(m.preview.ID)
	m.preview = nil
	m.syncIntent = ""
	if m.actionCancel != nil {
		m.actionCancel()
		m.actionCancel = nil
	}
}

func (m *Model) previewKey(key string) tea.Cmd {
	switch key {
	case "enter":
		return m.confirmPreview()
	case "esc", "q", "ctrl+c":
		m.discardPreview()
		m.message = "Cancelled"
	}
	return nil
}

func (m *Model) confirmPreview() tea.Cmd {
	if m.preview == nil || m.actions == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	if m.actionCancel != nil {
		m.actionCancel()
	}
	m.actionCancel = cancel
	m.actionCtx = ctx
	if m.syncIntent != "" {
		id, action, actions := m.preview.ID, m.syncIntent, m.actions
		m.syncIntent = ""
		m.preview = nil
		m.preparing = true
		m.actionGeneration++
		generation := m.actionGeneration
		m.message = "Fetching before " + string(action)
		return func() tea.Msg {
			preview, err := actions.PrepareSync(ctx, id, action)
			return previewMsg{generation: generation, preview: preview, err: err}
		}
	}
	m.running = true
	m.runningAction = m.preview.Targets[0].Action
	m.message = ""
	m.results = map[string]app.Event{}
	id, generation, actions := m.preview.ID, m.actionGeneration, m.actions
	m.preview = nil
	events := make(chan app.Event, 64)
	m.events = events
	run := func() tea.Msg {
		results, err := actions.Execute(ctx, id, func(event app.Event) { events <- event })
		close(events)
		return batchDoneMsg{generation: generation, results: results, err: err}
	}
	return tea.Batch(run, m.nextEvent())
}

func (m *Model) nextEvent() tea.Cmd {
	events, generation := m.events, m.actionGeneration
	return func() tea.Msg {
		if events == nil {
			return nil
		}
		event, ok := <-events
		if !ok {
			return nil
		}
		return progressMsg{generation: generation, event: event}
	}
}

// previewView confirms a push or pull in a small popup: one line per eligible
// repository, then skips with their reasons. Full plans stay in the backend.
func (m *Model) previewView() tea.View {
	p := m.preview
	action := p.Targets[0].Action
	var ready, skipped []app.Target
	for _, target := range p.Targets {
		if target.Eligible {
			ready = append(ready, target)
		} else {
			skipped = append(skipped, target)
		}
	}
	verb := capitalize(string(action))
	title := verb + " " + m.targetName(ready[0].Path) + "?"
	if len(ready) > 1 {
		title = verb + " " + plural(len(ready), "repository", "repositories") + "?"
	}
	w := min(64, m.width-4)
	inner := max(1, w-4)
	lines := []string{m.style(title, ink, true)}
	room := max(1, m.height-7)
	nameWidth := 0
	for _, target := range ready {
		nameWidth = max(nameWidth, min(24, ansi.StringWidth(m.targetName(target.Path))))
	}
	dirty := false
	for i, target := range ready {
		if i == room-1 && len(ready) > room {
			lines = append(lines, m.style(fmt.Sprintf("+%d more", len(ready)-i), muted, false))
			break
		}
		dirty = dirty || target.DirtyExcluded
		lines = append(lines, m.between(cell(m.targetName(target.Path), nameWidth)+"  "+m.style(syncRoute(target), muted, false), m.style(shortCommit(target.Commit), muted, false), inner))
	}
	for i, target := range skipped {
		if i == 2 && len(skipped) > 3 {
			lines = append(lines, m.style(fmt.Sprintf("+%d more skipped", len(skipped)-i), amber, false))
			break
		}
		lines = append(lines, m.style("skip "+m.targetName(target.Path)+": "+gitcli.SafeText(target.Reason), amber, false))
	}
	if dirty {
		lines = append(lines, m.style("Uncommitted changes stay local", muted, false))
	}
	lines = append(lines, "", m.style(m.symbols().enter+" "+string(action)+"   Esc cancel", muted, false))
	if m.width < 24 || m.height < 6 {
		return m.screen(lines)
	}
	return m.overlay(lines, w)
}

// targetName prefers the dashboard name for a target path.
func (m *Model) targetName(path string) string {
	for _, row := range m.rows {
		if row.Path == path {
			return gitcli.SafeText(row.Name)
		}
	}
	return gitcli.SafeText(filepath.Base(path))
}

// syncRoute renders a reviewed scope such as "main → origin/main".
func syncRoute(target app.Target) string {
	from, to, ok := strings.Cut(target.Scope, " -> ")
	if !ok {
		return gitcli.SafeText(target.Branch)
	}
	from, to = strings.TrimPrefix(from, "refs/heads/"), strings.TrimPrefix(to, "refs/heads/")
	if target.Action == app.Pull {
		from = target.Remote + "/" + from
	} else {
		to = target.Remote + "/" + to
	}
	return gitcli.SafeText(from + " → " + to)
}

func shortCommit(oid string) string { return gitcli.SafeText(oid[:min(7, len(oid))]) }

func capitalize(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
