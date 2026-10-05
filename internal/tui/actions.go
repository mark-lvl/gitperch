package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"repodash/internal/app"
	gitcli "repodash/internal/git"
	"sort"
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
		m.message = fmt.Sprintf("Batch finished · %d succeeded · %d skipped · %d failed/uncertain · d details", succeeded, skipped, failed)
		if msg.err != nil {
			m.actionFailed = true
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
		m.syncIntent = ""
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
		lines, footer := m.previewContent()
		_, _, _, page := m.documentParts(lines, footer)
		m.previewLineOffset += max(1, page)
	case "pgup":
		lines, footer := m.previewContent()
		_, _, _, page := m.documentParts(lines, footer)
		m.previewLineOffset = max(0, m.previewLineOffset-max(1, page))
	}
	if m.preview != nil {
		lines, footer := m.previewContent()
		m.previewLineOffset = min(m.previewLineOffset, m.documentMaxOffset(lines, footer))
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
		m.message = "Fetching reviewed scope before final synchronization preview"
		return func() tea.Msg {
			preview, err := actions.PrepareSync(ctx, id, action)
			return previewMsg{generation: generation, preview: preview, err: err}
		}
	}
	m.running = true
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

func (m *Model) previewContent() ([]string, string) {
	p := m.preview
	eligible := 0
	for _, target := range p.Targets {
		if target.Eligible {
			eligible++
		}
	}
	title := "Review fetch"
	step := "Review targets → confirm fetch"
	if m.syncIntent != "" {
		title = "Fetch scope before " + string(m.syncIntent)
		step = "STEP 1/2 · Review fetch → fetch → review " + string(m.syncIntent) + " → confirm"
	} else if len(p.Targets) > 0 && p.Targets[0].Action != app.Fetch {
		title = "Review " + string(p.Targets[0].Action)
		step = "STEP 2/2 · Fetch complete → review exact changes → confirm"
	}
	lines := []string{
		m.style(" repodash / "+title, accent, true),
		m.rule(max(1, m.width)),
		" " + step,
		fmt.Sprintf(" %d targets · %d eligible · %d skipped", len(p.Targets), eligible, len(p.Targets)-eligible),
		" j/k targets · PgUp/Dn scroll details", "",
	}
	if len(p.Targets) > 1 {
		lines = append(lines, " Selected repositories")
		for _, target := range p.Targets {
			state := "skip"
			if target.Eligible {
				state = "ready"
			}
			lines = append(lines, " "+truncatePath(gitcli.SafeText(target.Path), max(10, m.width/2))+" · "+gitcli.SafeText(target.Branch)+" · "+state)
		}
		lines = append(lines, "")
	}
	if len(p.Targets) > 0 {
		target := p.Targets[min(m.previewCursor, len(p.Targets)-1)]
		state, color := "SKIPPED", amber
		if target.Eligible {
			state, color = "ELIGIBLE", accent
		}
		lines = append(lines,
			m.style(fmt.Sprintf(" Target %d/%d: %s", m.previewCursor+1, len(p.Targets), gitcli.SafeText(target.Path)), ink, true),
			m.style(" "+state+" · "+gitcli.SafeText(string(target.Action)), color, true),
			" Reason: "+gitcli.SafeText(target.Reason), "",
			" Branch: "+gitcli.SafeText(target.Branch),
			" Remote: "+gitcli.SafeText(target.Remote),
			" URL: "+gitcli.SafeText(target.URL),
		)
		if target.Scope != "" {
			lines = append(lines, " Scope: "+gitcli.SafeText(target.Scope))
		}
		if target.Commit != "" {
			lines = append(lines, " Exact commit: "+gitcli.SafeText(target.Commit))
		}
		if target.DirtyExcluded {
			lines = append(lines, m.style(" Uncommitted changes are excluded", amber, true))
		}
	}
	lines = append(lines, "", " Eligible targets run independently. Skipped targets remain skipped.", " Best effort, no rollback. Repository state is revalidated before execution.")
	footer := fmt.Sprintf(" Enter confirms %d eligible targets · Esc cancels", eligible)
	if eligible == 0 {
		footer = " Enter records skips; no eligible targets · Esc cancels"
	}
	if m.syncIntent != "" {
		footer = " Enter fetches reviewed scope · Esc cancels\n Final push/pull needs a second confirmation"
	}
	return lines, footer
}

func (m *Model) previewView() tea.View {
	lines, footer := m.previewContent()
	return m.documentView(lines, footer, m.previewLineOffset)
}
