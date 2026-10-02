package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"sort"
	"strings"
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
		m.previewCursor = 0
		m.previewLineOffset = 0
		return true, nil
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
		}
		if m.actionCancel != nil {
			m.actionCancel()
			m.actionCancel = nil
		}
		m.message = "Batch finished; results remain in d details"
		if msg.err != nil {
			m.message += " · " + msg.err.Error()
		}
		m.preview = nil
		return true, m.refresh()
	}
	return false, nil
}

func (m *Model) preparePreview(action app.Action) tea.Cmd {
	var paths []string
	for path, selected := range m.selected {
		if selected {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		m.message = "Select repositories explicitly with Space or a before an action"
		return nil
	}
	sort.Strings(paths)
	if m.loadCancel != nil {
		m.loadCancel()
		m.loadCancel = nil
	}
	m.generation++
	m.loading = false
	m.actionGeneration++
	generation := m.actionGeneration
	ctx, cancel := context.WithCancel(m.ctx)
	m.actionCancel = cancel
	m.actionCtx = ctx
	m.preparing = true
	m.details = false
	m.help = false
	m.message = fmt.Sprintf("Preparing %s preview for %d repositories", action, len(paths))
	actions := m.actions
	return func() tea.Msg {
		preview, err := actions.Plan(ctx, action, paths)
		return previewMsg{generation: generation, preview: preview, err: err}
	}
}

func (m *Model) previewKey(key string) tea.Cmd {
	switch key {
	case "enter":
		return m.confirmPreview()
	case "esc", "q", "ctrl+c":
		m.actions.Discard(m.preview.ID)
		m.preview = nil
		if m.actionCancel != nil {
			m.actionCancel()
			m.actionCancel = nil
		}
		m.message = "Preview cancelled"
	case "j", "down":
		m.previewCursor = min(m.previewCursor+1, max(0, len(m.preview.Targets)-1))
		m.previewLineOffset = 0
	case "k", "up":
		m.previewCursor = max(0, m.previewCursor-1)
		m.previewLineOffset = 0
	case "pgdown":
		m.previewLineOffset += max(1, m.height-2)
	case "pgup":
		m.previewLineOffset = max(0, m.previewLineOffset-max(1, m.height-2))
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
	m.running = true
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

func (m *Model) previewView() tea.View {
	width, height := max(1, m.width), max(1, m.height)
	p := m.preview
	eligible := 0
	for _, target := range p.Targets {
		if target.Eligible {
			eligible++
		}
	}
	header := fmt.Sprintf("Preview: %d targets · %d eligible · j/k targets · PgUp/Dn details", len(p.Targets), eligible)
	var lines []string
	if len(p.Targets) > 0 {
		target := p.Targets[min(m.previewCursor, len(p.Targets)-1)]
		state := "SKIPPED"
		if target.Eligible {
			state = "ELIGIBLE"
		}
		detail := []string{fmt.Sprintf("Target %d/%d: %s", m.previewCursor+1, len(p.Targets), target.Path), fmt.Sprintf("%s %s · remote:%s · branch:%s", state, target.Action, target.Remote, target.Branch), "URL: " + target.URL, "Reason: " + target.Reason}
		if target.Scope != "" {
			detail = append(detail, "Scope: "+target.Scope)
		}
		if target.Commit != "" {
			detail = append(detail, "Exact commit: "+target.Commit)
		}
		if target.DirtyExcluded {
			detail = append(detail, "Uncommitted changes are excluded")
		}
		for _, line := range detail {
			lines = append(lines, strings.Split(ansi.Wrap(gitcli.SafeText(line), width, "/"), "\n")...)
		}
	}
	footer := "Enter confirms batch · Esc cancels · best effort, no rollback"
	page := max(1, height-2)
	offset := min(m.previewLineOffset, max(0, len(lines)-page))
	lines = append([]string{header}, lines[offset:min(len(lines), offset+page)]...)
	lines = append(lines, footer)
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}
