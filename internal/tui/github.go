package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mark-lvl/gitperch/internal/app"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// githubMsg reports a finished background lookup. Its results are in the
// shared cache, which applyGitHub copies onto the rows.
type githubMsg struct{}

// EnableGitHub turns on pull request lookups through gh; without it the
// dashboard never runs gh.
func (m *Model) EnableGitHub(g *app.GitHub) {
	m.github = g
	m.githubCtx, m.githubCancel = context.WithCancel(m.ctx)
}

// forceGitHub makes the next snapshot recheck GitHub, at most every
// app.GitHubMinGap; automatic refresh never calls it.
func (m *Model) forceGitHub() { m.githubForce = true }

// githubLookups copies cached results onto the rows and starts the lookups
// that are due; each reports back with a githubMsg.
func (m *Model) githubLookups() tea.Cmd {
	if m.github == nil || m.closing {
		return nil
	}
	m.applyGitHub()
	force := m.githubForce
	m.githubForce = false
	var cmds []tea.Cmd
	for _, group := range m.github.Due(m.rows, force) {
		m.githubPending++
		ctx, g := m.githubCtx, m.github
		cmds = append(cmds, func() tea.Msg {
			g.Lookup(ctx, group)
			return githubMsg{}
		})
	}
	return tea.Batch(cmds...)
}

// applyGitHub annotates the rows, which can change attention and therefore
// order and Focus, then keeps the highlighted repository and the selection as
// a refresh does.
func (m *Model) applyGitHub() {
	path := ""
	if row := m.highlightedRow(); row != nil {
		path = row.Path
	}
	oldSelection := m.selected
	m.github.Annotate(m.rows)
	m.restoreView(path, oldSelection)
}

// openPullRequest is the row's current pull request when it is open.
func openPullRequest(row app.Row) *app.PullRequest {
	if pr := row.CurrentPullRequest(); pr != nil && pr.State == app.PullRequestOpen {
		return pr
	}
	return nil
}

// pullRequestState names a pull request's state, with its base once merged.
func pullRequestState(pr app.PullRequest) (string, string) {
	switch {
	case pr.State == app.PullRequestMerged:
		return "merged into " + gitcli.SafeText(pr.Base), success
	case pr.State == app.PullRequestClosed:
		return "closed", muted
	case pr.Draft:
		return "draft", muted
	}
	return "open", success
}

func checksLabel(checks string) (string, string) {
	switch checks {
	case app.ChecksFailing:
		return "checks failing", danger
	case app.ChecksPassing:
		return "checks passing", success
	case app.ChecksPending:
		return "checks pending", muted
	}
	return "", ""
}

func reviewLabel(review string) (string, string) {
	switch review {
	case app.ReviewChangesRequested:
		return "changes requested", amber
	case app.ReviewApproved:
		return "approved", success
	case app.ReviewRequired:
		return "review required", muted
	}
	return "", ""
}

// pullRequestAge is when a pull request last changed: its merge, else its
// latest update.
func (m *Model) pullRequestAge(pr app.PullRequest) string {
	if pr.State == app.PullRequestMerged && !pr.MergedAt.IsZero() {
		return relativeTime(m.now(), pr.MergedAt)
	}
	return relativeTime(m.now(), pr.UpdatedAt)
}

// hasPullRequestLine reports whether the preview card shows a pull request
// line for row: it has a pull request or a GitHub error to report.
func hasPullRequestLine(row app.Row) bool {
	return row.GitHub != nil && (row.CurrentPullRequest() != nil || row.GitHub.Error != "")
}

// pullRequestLine is the preview card's summary of the row's pull request,
// such as "PR #42 · open · checks failing · changes requested · 3h ago".
func (m *Model) pullRequestLine(row app.Row, w int) string {
	g, pr := row.GitHub, row.CurrentPullRequest()
	if g == nil || pr == nil && g.Error == "" {
		return ""
	}
	if pr == nil {
		return m.style(cell("GitHub: "+gitcli.SafeText(g.Error), w), amber, false)
	}
	dot := m.style(" · ", muted, false)
	state, color := pullRequestState(*pr)
	parts := []string{m.style(fmt.Sprintf("PR #%d", pr.Number), accent, true), m.style(state, color, false)}
	if pr.State == app.PullRequestOpen {
		if label, color := checksLabel(pr.Checks); label != "" {
			parts = append(parts, m.style(label, color, false))
		}
		if label, color := reviewLabel(pr.Review); label != "" {
			parts = append(parts, m.style(label, color, false))
		}
	}
	parts = append(parts, m.style(m.pullRequestAge(*pr), muted, false))
	if g.Error != "" {
		parts = append(parts, m.style("checked "+relativeTime(m.now(), g.CheckedAt), amber, false))
	}
	return cell(strings.Join(parts, dot), w)
}

// pullRequestLines is the details Overview section for the row's pull
// requests; nil when no lookup applies.
func (m *Model) pullRequestLines(row app.Row) []string {
	g := row.GitHub
	if g == nil {
		return nil
	}
	lines := []string{"", m.style(" Pull request", accent, true)}
	pr := row.CurrentPullRequest()
	if pr == nil {
		if g.Error != "" {
			return append(lines, m.style(" GitHub: "+gitcli.SafeText(g.Error), amber, false))
		}
		return append(lines, m.style(" none for "+gitcli.SafeText(g.Branch)+" on "+gitcli.SafeText(g.Repository), muted, false))
	}
	state, color := pullRequestState(*pr)
	lines[1] += m.style(" · "+state, color, false)
	title := fmt.Sprintf(" #%d %s", pr.Number, gitcli.SafeText(pr.Title))
	if pr.Draft {
		title += m.style(" (draft)", muted, false)
	}
	lines = append(lines, title, m.style(" "+gitcli.SafeText(pr.Base)+" ← "+gitcli.SafeText(g.Branch)+" · "+gitcli.SafeText(pr.BaseRepository), muted, false))
	var facts []string
	if label, _ := checksLabel(pr.Checks); label != "" {
		facts = append(facts, capitalize(label))
	}
	if label, _ := reviewLabel(pr.Review); label != "" {
		facts = append(facts, capitalize(label))
	}
	if pr.State == app.PullRequestMerged {
		facts = append(facts, "merged "+m.pullRequestAge(*pr))
	} else {
		facts = append(facts, "updated "+m.pullRequestAge(*pr))
	}
	lines = append(lines, " "+strings.Join(facts, " · "), m.style(" "+gitcli.SafeText(pr.URL), muted, false))
	if earlier := g.PullRequests[1:]; len(earlier) > 0 {
		var names []string
		for _, other := range earlier[:min(3, len(earlier))] {
			state, _ := pullRequestState(other)
			names = append(names, fmt.Sprintf("#%d %s", other.Number, state))
		}
		lines = append(lines, m.style(" Earlier: "+strings.Join(names, " · "), muted, false))
	}
	checked := " Checked " + relativeTime(m.now(), g.CheckedAt)
	if g.Error != "" {
		checked += " · GitHub: " + gitcli.SafeText(g.Error)
	}
	return append(lines, m.style(checked, muted, false))
}

// browseCommand builds the gh command that opens a GitHub page.
type browseCommand func(app.BrowseTarget) (*exec.Cmd, error)

// browserExitedMsg reports that gh browse returned; nothing is refreshed.
type browserExitedMsg struct{ err error }

// EnableBrowse lets b open the highlighted row's pull request or repository.
func (m *Model) EnableBrowse(open func(app.BrowseTarget) (*exec.Cmd, error)) { m.browse = open }

// canBrowse reports whether b works for row.
func (m *Model) canBrowse(row app.Row) bool {
	_, ok := row.BrowseTarget()
	return ok && m.browse != nil
}

// openInBrowser runs gh browse with the terminal handed over, since BROWSER
// may name a terminal browser. Without GitHub, b does nothing, as before
// the integration.
func (m *Model) openInBrowser() tea.Cmd {
	if m.browse == nil {
		return nil
	}
	row := m.highlightedRow()
	if row == nil {
		m.message = "Select a repository first"
		return nil
	}
	target, ok := row.BrowseTarget()
	if !ok {
		m.message = "No GitHub repository for this branch"
		return nil
	}
	cmd, err := m.browse(target)
	if err != nil {
		m.message = "gh browse: " + gitcli.SafeText(err.Error())
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return browserExitedMsg{err: err} })
}

// browseLabel names what b opens for row in the command palette.
func browseLabel(row app.Row) string {
	target, _ := row.BrowseTarget()
	if target.Number > 0 {
		return fmt.Sprintf("Open pull request #%d in browser", target.Number)
	}
	return "Open " + gitcli.SafeText(target.Repository) + " on GitHub"
}
