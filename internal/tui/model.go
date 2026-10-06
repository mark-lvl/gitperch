// Package tui implements the interactive terminal dashboard.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

type loader func(context.Context) (app.Snapshot, error)

// Model holds the dashboard state. New supplies asynchronous refresh behavior
// through load so the view remains independent of configuration and Git setup.
type Model struct {
	ctx              context.Context
	load             loader
	noColor          bool
	iconMode         string
	workspace        string
	palette          bool
	loadDetails      detailLoader
	detailCache      map[string]detailResult
	detailStale      map[string]bool // cached before the latest snapshot; shown until reloaded
	detailPath       string
	detailGeneration uint64
	detailCancel     context.CancelFunc
	detailTab        int
	loadPatch        patchLoader
	patch            *patchResult
	patchPath        string
	patchCancel      context.CancelFunc
	patchGeneration  uint64
	patchStale       bool // patch predates the latest snapshot; shown until reloaded
	paletteQuery     string
	paletteCursor    int
	lazyGitAvailable bool
	closing          bool
	rows             []app.Row
	warnings         []string
	selected         map[string]bool
	actionTotal      int // targets in the running batch
	highlight        int
	filter           string
	filtering        bool
	scope            int
	attentionFirst   bool
	help             bool
	helpOffset       int
	details          bool
	detailOffset     int
	contextOffset    int
	message          string
	loadErr          string
	loading          bool
	width            int
	height           int
	scroll           int
	generation       uint64
	loadCancel       context.CancelFunc
	actions          *app.Actions
	preparing        bool
	running          bool
	preview          *app.Preview
	syncIntent       app.Action
	runningAction    app.Action
	actionCancel     context.CancelFunc
	actionCtx        context.Context
	actionGeneration uint64
	events           chan app.Event
	results          map[string]app.Event
	interrupted      bool
	actionFailed     bool
	clock            func() time.Time // header clock and relative ages; fixed in captures
	ticking          bool
	spinning         bool // a spinnerMsg is scheduled
	spinnerFrame     int
	autoRefresh      time.Duration // 0 disables automatic status refresh
	autoGeneration   uint64        // only the newest scheduled autoRefreshMsg counts
	refreshUntil     time.Time     // keeps a quick refresh's indicator readable instead of a blink
	// refreshInterrupted records that an action cancelled an in-flight load,
	// which must resume if the action ends without a batch to refresh after.
	refreshInterrupted bool
}

type snapshotMsg struct {
	generation uint64
	snapshot   app.Snapshot
	err        error
}

type childExitedMsg struct{ err error }

// clockMsg redraws the header clock and relative ages; it never reads Git.
type clockMsg struct{}

// spinnerMsg advances the busy spinner; it is scheduled only while busy.
type spinnerMsg struct{}

// autoRefreshMsg reloads local status after the configured idle interval.
type autoRefreshMsg struct{ generation uint64 }

const spinnerInterval = 100 * time.Millisecond

// refreshIndicatorMin is the shortest time the refresh indicator stays up.
const refreshIndicatorMin = time.Second

func (m *Model) now() time.Time { return m.clock() }

func (m *Model) busy() bool { return m.loading || m.preparing || m.running }

// refreshing reports whether the refresh indicator shows: during the load and
// for the rest of its minimum display time.
func (m *Model) refreshing() bool { return m.loading || m.now().Before(m.refreshUntil) }

func (m *Model) spin() tea.Cmd {
	m.spinning = true
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerMsg{} })
}

// The tick starts with the first snapshot rather than in Init, so Init stays a
// single cancellable load. Ticks align to the minute to keep the clock exact.
func (m *Model) tick() tea.Cmd {
	m.ticking = true
	wait := time.Minute - time.Duration(m.now().Second())*time.Second
	return tea.Tick(wait, func(time.Time) tea.Msg { return clockMsg{} })
}

// scheduleAutoRefresh restarts the interval, so it always counts from the
// latest completed load, whether manual or automatic.
func (m *Model) scheduleAutoRefresh() tea.Cmd {
	if m.autoRefresh <= 0 || m.closing {
		return nil
	}
	m.autoGeneration++
	generation := m.autoGeneration
	return tea.Tick(m.autoRefresh, func(time.Time) tea.Msg { return autoRefreshMsg{generation} })
}

// autoRefreshPaused avoids reloading under the user while work runs or while a
// view that would lose its loaded content (details, diff, confirmation) is open.
func (m *Model) autoRefreshPaused() bool {
	return m.busy() || m.preview != nil || m.details || m.palette
}

// New creates a TUI model. load must honor its context; refreshing cancels an
// older load and ignores any late result from it.
func New(ctx context.Context, load func(context.Context) (app.Snapshot, error), noColor bool) *Model {
	if ctx == nil {
		ctx = context.Background()
	}
	_, lazyGitErr := exec.LookPath("lazygit")
	return &Model{
		lazyGitAvailable: lazyGitErr == nil,
		clock:            time.Now,
		detailCache:      make(map[string]detailResult),
		detailStale:      make(map[string]bool),
		ctx:              ctx,
		load:             load,
		noColor:          noColor,
		selected:         make(map[string]bool),
		width:            80,
		height:           24,
	}
}

// SetAutoRefresh enables periodic local status refresh; zero or less disables it.
func (m *Model) SetAutoRefresh(interval time.Duration) { m.autoRefresh = interval }

// Configure uses the existing TOML configuration rather than a second settings source.
func (m *Model) Configure(workspace, icons string, focus bool) {
	m.workspace = gitcli.SafeText(workspace)
	m.iconMode = icons
	if focus {
		m.scope = 1
	}
}

func (m *Model) Init() tea.Cmd { return m.refresh() }

func (m *Model) refresh() tea.Cmd {
	if m.load == nil {
		m.loadErr = "no dashboard loader configured"
		return nil
	}
	if m.loadCancel != nil {
		m.loadCancel()
	}
	m.generation++
	generation := m.generation
	ctx, cancel := context.WithCancel(m.ctx)
	m.loadCancel = cancel
	m.loading = true
	m.refreshInterrupted = false
	m.refreshUntil = m.now().Add(refreshIndicatorMin)
	return func() tea.Msg {
		snapshot, err := m.load(ctx)
		return snapshotMsg{generation: generation, snapshot: snapshot, err: err}
	}
}

func (m *Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	// Notices and detail arrival can change available rows without a resize.
	previousPath := ""
	if row := m.highlightedRow(); row != nil {
		previousPath = row.Path
	}
	defer func() {
		m.keepHighlightVisible()
		m.contextOffset = min(m.contextOffset, m.maxContextOffset())
		if row := m.highlightedRow(); row == nil || row.Path != previousPath {
			m.contextOffset = 0
		}
		if extra := m.ensureDetail(); extra != nil {
			cmd = tea.Batch(cmd, extra)
		}
		if extra := m.ensurePatch(); extra != nil {
			cmd = tea.Batch(cmd, extra)
		}
		// Every busy state starts through Update, so one check here keeps the
		// spinner going without each start site scheduling it.
		if (m.busy() || m.refreshing()) && !m.spinning && !m.closing {
			cmd = tea.Batch(cmd, m.spin())
		}
	}()
	if handled, cmd := m.actionMessage(msg); handled {
		return m, cmd
	}
	switch msg := msg.(type) {
	case patchMsg:
		if msg.generation == m.patchGeneration {
			m.patch = &msg.result
			m.patchStale = false
			m.patchPath = ""
			if m.patchCancel != nil {
				m.patchCancel()
				m.patchCancel = nil
			}
		}
	case detailMsg:
		if msg.generation == m.detailGeneration {
			m.detailCache[msg.path] = msg.result
			delete(m.detailStale, msg.path)
			m.detailPath = ""
			if m.detailCancel != nil {
				m.detailCancel()
				m.detailCancel = nil
			}
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.width < 1 {
			m.width = 1
		}
		if m.height < 1 {
			m.height = 1
		}
		m.keepHighlightVisible()
	case snapshotMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		m.applySnapshot(msg.snapshot)
		m.loading = false
		if m.loadCancel != nil {
			m.loadCancel()
			m.loadCancel = nil
		}
		if msg.err != nil {
			m.loadErr = msg.err.Error()
		} else {
			m.loadErr = ""
		}
		next := m.scheduleAutoRefresh()
		if !m.ticking && !m.closing {
			next = tea.Batch(next, m.tick())
		}
		return m, next
	case autoRefreshMsg:
		if msg.generation != m.autoGeneration || m.closing {
			return m, nil
		}
		if m.autoRefreshPaused() {
			return m, m.scheduleAutoRefresh()
		}
		return m, m.refresh()
	case childExitedMsg:
		if msg.err != nil {
			m.message = "Child process: " + msg.err.Error()
		} else {
			m.message = "Child process finished"
		}
		return m, m.refresh()
	case clockMsg:
		if m.closing {
			return m, nil
		}
		return m, m.tick()
	case spinnerMsg:
		m.spinning = false
		if !(m.busy() || m.refreshing()) || m.closing {
			m.spinnerFrame = 0
			return m, nil
		}
		m.spinnerFrame++
		return m, m.spin()
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *Model) applySnapshot(snapshot app.Snapshot) {
	oldHighlight := ""
	visible := m.visibleRows()
	if m.highlight >= 0 && m.highlight < len(visible) {
		oldHighlight = m.rows[visible[m.highlight]].Path
	}
	oldSelection := m.selected
	m.rows = append([]app.Row(nil), snapshot.Rows...)
	if m.detailCancel != nil {
		m.detailCancel()
		m.detailCancel = nil
	}
	m.detailGeneration++
	m.detailPath = ""
	// Earlier details stay on screen until their reload arrives, so a refresh
	// does not blank the preview; repositories that disappeared are dropped.
	present := make(map[string]bool, len(m.rows))
	for _, row := range m.rows {
		present[row.Path] = true
	}
	for path := range m.detailCache {
		if present[path] {
			m.detailStale[path] = true
		} else {
			delete(m.detailCache, path)
			delete(m.detailStale, path)
		}
	}
	m.cancelPatch()
	m.patchStale = m.patch != nil
	m.warnings = m.warnings[:0]
	for _, warning := range snapshot.Warnings {
		m.warnings = append(m.warnings, gitcli.SafeText(warning.Path+": "+warning.Message))
	}
	m.selected = make(map[string]bool)
	for _, row := range m.rows {
		if oldSelection[row.Path] && matches(row, m.filter) && m.inScope(row) {
			m.selected[row.Path] = true
		}
	}
	indices := m.visibleRows()
	m.highlight = 0
	if oldHighlight != "" {
		for i, index := range indices {
			if m.rows[index].Path == oldHighlight {
				m.highlight = i
				break
			}
		}
	}
	if len(indices) == 0 {
		m.scroll = 0
	} else {
		m.keepHighlightVisible()
	}
}

func (m *Model) key(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.preparing || m.running {
		if key == "q" || key == "ctrl+c" || key == "esc" {
			if m.actionCancel != nil {
				m.actionCancel()
			}
			m.message = "Cancellation requested; waiting for outcomes"
		}
		return nil
	}
	if m.preview != nil {
		return m.previewKey(key)
	}
	if key == "ctrl+c" {
		m.closeReads()
		m.interrupted = true
		if m.loadCancel != nil {
			m.loadCancel()
			m.loadCancel = nil
		}
		return tea.Quit
	}
	if m.palette {
		return m.paletteKey(msg)
	}
	if m.help {
		switch key {
		case "?", "esc":
			m.help = false
		case "q":
			m.closeReads()
			if m.loadCancel != nil {
				m.loadCancel()
			}
			return tea.Quit
		case "j", "down":
			m.helpOffset++
		case "k", "up":
			m.helpOffset = max(0, m.helpOffset-1)
		case "pgdown":
			_, _, _, page := m.documentParts(m.helpContent(), helpFooter)
			m.helpOffset += max(1, page)
		case "pgup":
			_, _, _, page := m.documentParts(m.helpContent(), helpFooter)
			m.helpOffset = max(0, m.helpOffset-max(1, page))
		case "home":
			m.helpOffset = 0
		}
		m.helpOffset = min(m.helpOffset, m.documentMaxOffset(m.helpContent(), helpFooter))
		return nil
	}
	if m.details {
		switch key {
		case "?":
			m.help = true
			m.helpOffset = 0
		case "q":
			m.closeReads()
			return tea.Quit
		case "tab", "shift+tab":
			step := 1
			if key == "shift+tab" {
				step = 3
			}
			m.detailTab = (m.detailTab + step) % 4
			m.detailOffset = 0
		case ":", "ctrl+k":
			m.palette = true
			m.paletteQuery = ""
			m.paletteCursor = 0
		case "p":
			return m.executeCommand("push")
		case "l":
			return m.executeCommand("pull")
		case "f":
			return m.executeCommand("fetch")
		case "o":
			return m.launchShell()
		case "g":
			return m.launchLazyGit()
		case "r":
			return m.refresh()
		case "d":
			m.detailTab = 1
			m.detailOffset = 0
		case "esc":
			m.details = false
		case "j", "down":
			m.detailOffset++
		case "pgdown":
			_, _, _, page := m.detailParts()
			m.detailOffset += max(1, page)
		case "pgup":
			_, _, _, page := m.detailParts()
			m.detailOffset = max(0, m.detailOffset-max(1, page))
		case "home":
			m.detailOffset = 0
		case "k", "up":
			if m.detailOffset > 0 {
				m.detailOffset--
			}
		}
		_, body, _, page := m.detailParts()
		m.detailOffset = min(m.detailOffset, max(0, len(body)-max(1, page)))
		return nil
	}
	if m.filtering {
		switch key {
		case "enter":
			if m.highlightedRow() == nil {
				return nil
			}
			m.filtering = false
			m.details = true
			m.detailTab = 0
			m.detailOffset = 0
		case "esc":
			m.filtering = false
			m.filter = ""
			m.clearSelection()
			m.highlight, m.scroll = 0, 0
		case "down", "up":
			indices := m.visibleRows()
			if key == "down" {
				m.highlight = min(max(0, len(indices)-1), m.highlight+1)
			} else {
				m.highlight = max(0, m.highlight-1)
			}
		case "backspace":
			text := []rune(m.filter)
			if len(text) > 0 {
				m.filter = string(text[:len(text)-1])
				m.clearSelection()
				m.highlight, m.scroll = 0, 0
			}
		default:
			// Text is set only for printable input, including shifted keys;
			// Ctrl and Alt combinations arrive without it.
			if text := msg.Key().Text; text != "" {
				m.filter += text
				m.clearSelection()
				m.highlight, m.scroll = 0, 0
			}
		}
		return nil
	}
	indices := m.visibleRows()
	switch key {
	case "q":
		m.closeReads()
		if m.loadCancel != nil {
			m.loadCancel()
			m.loadCancel = nil
		}
		return tea.Quit
	case ":", "ctrl+k":
		m.palette = true
		m.paletteQuery = ""
		m.paletteCursor = 0
	case "?":
		m.help = true
		m.helpOffset = 0
	case "d":
		// Diagnostics stay reachable when no repository row is visible.
		if m.highlightedRow() == nil && !m.hasDiagnostics() {
			return nil
		}
		m.detailTab = 1
		m.details = true
		m.detailOffset = 0
	case "esc":
		if m.help {
			m.help = false
		} else if m.filter != "" {
			m.filter = ""
			m.clearSelection()
			m.highlight, m.scroll = 0, 0
		} else {
			m.results = nil
			m.message = ""
		}
	case "/":
		m.filtering = true
		m.filter = ""
		m.clearSelection()
		m.highlight, m.scroll = 0, 0
	case "r":
		m.message = ""
		return m.refresh()
	case "tab", "shift+tab":
		return m.executeCommand("focus")
	case "[":
		m.contextOffset = max(0, m.contextOffset-1)
	case "]":
		if row := m.highlightedRow(); row != nil {
			if _, ok := m.detailCache[row.Path]; ok {
				m.contextOffset = min(m.maxContextOffset(), m.contextOffset+1)
			}
		}
	case "s":
		path := ""
		if row := m.highlightedRow(); row != nil {
			path = row.Path
		}
		m.attentionFirst = !m.attentionFirst
		for i, index := range m.visibleRows() {
			if m.rows[index].Path == path {
				m.highlight = i
				break
			}
		}
		m.keepHighlightVisible()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// Narrow layouts number the first nine rows; the keys work at any width.
		if index := int(msg.String()[0] - '1'); index < len(m.visibleRows()) {
			m.highlight = index
			m.keepHighlightVisible()
		}
	case "home":
		m.highlight = 0
		m.keepHighlightVisible()
	case "end":
		m.highlight = max(0, len(indices)-1)
		m.keepHighlightVisible()
	case "pgdown":
		m.highlight = min(max(0, len(indices)-1), m.highlight+m.pageSize())
		m.keepHighlightVisible()
	case "pgup":
		m.highlight = max(0, m.highlight-m.pageSize())
		m.keepHighlightVisible()
	case "j", "down":
		if m.highlight+1 < len(indices) {
			m.highlight++
			m.keepHighlightVisible()
		}
	case "k", "up":
		if m.highlight > 0 {
			m.highlight--
			m.keepHighlightVisible()
		}
	case " ", "space":
		if row := m.highlightedRow(); row != nil {
			m.selected[row.Path] = !m.selected[row.Path]
			if !m.selected[row.Path] {
				delete(m.selected, row.Path)
			}
		}
	case "a":
		allSelected := len(indices) > 0
		for _, index := range indices {
			if !m.selected[m.rows[index].Path] {
				allSelected = false
				break
			}
		}
		if allSelected {
			m.clearSelection()
		} else {
			m.clearSelection()
			for _, index := range indices {
				m.selected[m.rows[index].Path] = true
			}
		}
	case "f", "p", "l":
		if len(m.selected) == 0 && m.actions != nil && (key == "p" || key == "l") {
			id := "push"
			if key == "l" {
				id = "pull"
			}
			return m.executeCommand(id)
		}
		if m.actions == nil {
			m.message = "This action is not available yet"
			return nil
		}
		m.syncIntent = ""
		if key == "p" {
			m.syncIntent = app.Push
		} else if key == "l" {
			m.syncIntent = app.Pull
		}
		return m.preparePreview(app.Fetch, m.selectedPaths())
	case "enter":
		if m.highlightedRow() == nil {
			return nil
		}
		m.details = true
		m.detailTab = 0
		m.detailOffset = 0
	case "o":
		return m.launchShell()
	case "g":
		return m.launchLazyGit()
	}
	return nil
}

// ExitCode reports command interruption or partial/operation failure after the
// dashboard closes. Dismissing displayed results does not erase a failed batch.
func (m *Model) ExitCode() int {
	if m.interrupted {
		return 130
	}
	if m.actionFailed || m.loadErr != "" || len(m.warnings) > 0 {
		return 1
	}
	for _, row := range m.rows {
		if row.Status.Error != "" {
			return 1
		}
	}
	return 0
}

func (m *Model) launchShell() tea.Cmd {
	row := m.highlightedRow()
	if row == nil {
		m.message = "Select a repository first"
		return nil
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-i")
	cmd.Dir = row.Path
	cmd.Env = gitcli.ChildEnvironment(os.Environ())
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return childExitedMsg{err: err} })
}

func (m *Model) launchLazyGit() tea.Cmd {
	row := m.highlightedRow()
	if row == nil {
		m.message = "Select a repository first"
		return nil
	}
	path, err := exec.LookPath("lazygit")
	if err != nil {
		m.message = "LazyGit is not installed or not on PATH"
		return nil
	}
	cmd := exec.Command(path)
	cmd.Dir = row.Path
	cmd.Env = gitcli.ChildEnvironment(os.Environ())
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return childExitedMsg{err: err} })
}

func (m *Model) clearSelection() { m.selected = make(map[string]bool) }

func (m *Model) hasDiagnostics() bool {
	return len(m.warnings) > 0 || m.loadErr != "" || len(m.results) > 0
}

func (m *Model) selectedPaths() []string {
	paths := make([]string, 0, len(m.selected))
	for path, selected := range m.selected {
		if selected {
			paths = append(paths, path)
		}
	}
	return paths
}

func (m *Model) highlightedRow() *app.Row {
	indices := m.visibleRows()
	if m.highlight < 0 || m.highlight >= len(indices) {
		return nil
	}
	return &m.rows[indices[m.highlight]]
}

func (m *Model) visibleRows() []int {
	indices := make([]int, 0, len(m.rows))
	for i, row := range m.rows {
		if matches(row, m.filter) && m.inScope(row) {
			indices = append(indices, i)
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		a, b := m.rows[indices[i]], m.rows[indices[j]]
		if m.attentionFirst && m.attentionRank(a) != m.attentionRank(b) {
			return m.attentionRank(a) > m.attentionRank(b)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	return indices
}

func matches(row app.Row, filter string) bool {
	filter = strings.ToLower(filter)
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(row.Name), filter) ||
		strings.Contains(strings.ToLower(row.Path), filter) ||
		strings.Contains(strings.ToLower(row.Status.Branch), filter)
}

func (m *Model) keepHighlightVisible() {
	indices := m.visibleRows()
	if m.highlight < 0 {
		m.highlight = 0
	}
	if m.highlight >= len(indices) && len(indices) > 0 {
		m.highlight = len(indices) - 1
	}
	page := m.pageSize()
	if m.highlight < m.scroll {
		m.scroll = m.highlight
	} else if m.highlight >= m.scroll+page {
		m.scroll = m.highlight - page + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *Model) pageSize() int {
	return m.layout().slots
}

const detailsFooter = " ↑↓ scroll · Tab section · o shell · : actions · Esc back"

func (m *Model) detailsContent() []string {
	content := []string{m.style(" gitperch / Diagnostics", accent, true), m.rule(max(1, m.width))}
	if row := m.highlightedRow(); row != nil {
		content = append(content, "Path: "+gitcli.SafeText(row.Path), m.detailLine(*row))
		if row.Status.Error != "" {
			content = append(content, m.style("Error: "+gitcli.SafeText(row.Status.Error), danger, false))
		}
	}
	if m.loadErr != "" {
		content = append(content, "Load error: "+gitcli.SafeText(m.loadErr))
	}
	if len(m.results) > 0 {
		content = append(content, "", m.style(" BATCH RESULTS", accent, true))
		for _, row := range m.rows {
			if result, ok := m.results[row.Path]; ok {
				content = append(content, gitcli.SafeText(result.Path)+": "+string(result.State)+" · "+gitcli.SafeText(result.Message))
			}
		}
	}
	if len(m.warnings) > 0 {
		content = append(content, "", m.style(" WORKSPACE WARNINGS", amber, true))
		content = append(content, m.warnings...)
	}
	if len(content) == 2 {
		content = append(content, "No repository details or warnings")
	}
	return content
}

// detailBodyWidth leaves room for the section column when it is shown.
func (m *Model) detailBodyWidth() int {
	if inset := m.detailNavWidth(); inset > 0 {
		return max(1, m.width-inset-1)
	}
	return max(1, m.width)
}

// detailParts splices the tracked patch, wrapped once per width, into the
// wrapped details document.
func (m *Model) detailParts() (header, body, foot []string, page int) {
	content, patchAt := m.repositoryDetails()
	width := m.detailBodyWidth()
	if patchAt < 0 {
		return m.documentPartsAt(content, detailsFooter, width)
	}
	header, body, foot, page = m.documentPartsAt(content[:patchAt], detailsFooter, width)
	body = append(body, m.patch.wrappedLines(width)...)
	return header, append(body, wrapBody(content[patchAt:], width)...), foot, page
}

// detailsView scrolls the section content beside a fixed section column, or
// below inline tabs on narrow terminals.
func (m *Model) detailsView() tea.View {
	header, body, foot, page := m.detailParts()
	offset := min(max(0, m.detailOffset), max(0, len(body)-max(1, page)))
	body = fitLines(body[offset:min(len(body), offset+page)], page)
	lines := append([]string(nil), header...)
	if m.detailNavWidth() == 0 {
		return m.screen(append(append(lines, body...), foot...))
	}
	nav := m.detailNav(page)
	for i := range body {
		lines = append(lines, nav[i]+" "+body[i])
	}
	return m.screen(append(lines, foot...))
}

func (m *Model) detailLine(row app.Row) string {
	status := row.Status
	branch := status.Branch
	if status.Detached {
		branch = "(detached)"
	}
	if status.Unborn {
		branch += " (unborn)"
	}
	upstream := status.Upstream
	if upstream == "" {
		upstream = "none"
	}
	ahead, behind := "unknown", "unknown"
	if status.ComparisonKnown {
		ahead, behind = fmt.Sprint(status.Ahead), fmt.Sprint(status.Behind)
	}
	comparisonLabel := "locally known refs"
	operation := status.Operation
	if operation == "" {
		operation = "idle"
	}
	line := fmt.Sprintf("Branch: %s · changes:%d untracked:%d conflicts:%d · ahead:%s behind:%s (%s) · upstream:%s · operation:%s",
		gitcli.SafeText(branch), status.Changes, status.Untracked, status.Conflicts, ahead, behind, comparisonLabel, gitcli.SafeText(upstream), gitcli.SafeText(operation))
	if !row.LastFetch.IsZero() {
		line += " · last fetch:" + row.LastFetch.Format("15:04:05")
	}
	if result, ok := m.results[row.Path]; ok {
		line += " · " + string(result.State) + ": " + gitcli.SafeText(result.Message)
	}
	return line
}
