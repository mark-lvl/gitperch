package app

import (
	"context"
	"errors"
	"fmt"
	gitcli "repodash/internal/git"
	"repodash/internal/repository"
	"strings"
)

type SyncGit interface {
	ActionGit
	ResolveUpstream(context.Context, string, gitcli.Metadata) (gitcli.UpstreamTarget, error)
	ResolvePush(context.Context, string, gitcli.Metadata, gitcli.UpstreamTarget) (gitcli.PushTarget, error)
	UpstreamCommit(context.Context, string, gitcli.UpstreamTarget) (string, error)
	Push(context.Context, string, gitcli.PushTarget) error
	FastForward(context.Context, string, string) error
}

func syncEligibility(s repository.Status, action Action) error {
	if s.Error != "" {
		return fmt.Errorf("inspection failed: %s", s.Error)
	}
	if s.Detached || s.Unborn || s.Branch == "" {
		return errors.New("detached or unborn branch")
	}
	if s.Conflicts > 0 {
		return errors.New("conflicts prevent synchronization")
	}
	if s.Operation != "" {
		return fmt.Errorf("operation in progress: %s", s.Operation)
	}
	if action == Pull && s.Dirty() {
		return errors.New("fast-forward pull requires a clean index and worktree")
	}
	return nil
}

func samePlanState(m, expected gitcli.Metadata) bool {
	return m.CommonDir == expected.CommonDir && m.ConfigHash == expected.ConfigHash && m.Status.HeadOID == expected.Status.HeadOID && m.Status.Branch == expected.Status.Branch && m.Status.Detached == expected.Status.Detached && m.Status.Unborn == expected.Status.Unborn
}

func (a *Actions) planOne(ctx context.Context, action Action, path string, expected *gitcli.Metadata) plannedTarget {
	p := plannedTarget{display: Target{Path: path, Action: action}}
	m, err := a.service.Metadata(ctx, path)
	p.metadata = m
	p.display.Branch = m.Status.Branch
	if err == nil && expected != nil && !samePlanState(m, *expected) {
		err = errors.New("repository or target configuration changed since fetch-scope preview")
	}
	if action == Fetch {
		if err == nil {
			p.fetch, err = a.service.ResolveFetch(ctx, path, m)
		}
		if err == nil {
			p.display.Remote = p.fetch.Remote
			p.display.URL = gitcli.SafeText(p.fetch.URL)
			p.display.Scope = strings.Join(m.Values("remote."+p.fetch.Remote+".fetch"), ", ")
			p.display.Reason = "Prune remote-tracking branches; do not fetch/prune tags or recurse into submodules"
		}
	} else {
		sync, ok := a.service.(SyncGit)
		if err == nil && !ok {
			err = errors.New("synchronization service unavailable")
		}
		if err == nil {
			err = syncEligibility(m.Status, action)
		}
		if err == nil {
			p.upstream, err = sync.ResolveUpstream(ctx, path, m)
			p.fetch = p.upstream.Fetch
		}
		if err == nil {
			p.display.Remote = p.fetch.Remote
			p.display.Branch = p.upstream.Branch
			p.display.URL = gitcli.SafeText(p.fetch.URL)
			if action == Push {
				p.push, err = sync.ResolvePush(ctx, path, m, p.upstream)
				p.display.URL = gitcli.SafeText(p.push.URL)
			}
		}
		if err == nil {
			err = a.service.Fetch(ctx, path, p.fetch)
			p.display.PreflightFailed = err != nil
			if err == nil {
				a.markFetched(path)
			}
		}
		if err == nil {
			fresh, e := a.service.Metadata(ctx, path)
			err = e
			if err == nil && !samePlanState(fresh, m) {
				err = errors.New("repository or configuration changed during preflight fetch")
			}
			m = fresh
			p.metadata = m
		}
		if err == nil {
			err = syncEligibility(m.Status, action)
		}
		if err == nil {
			upstream, e := sync.ResolveUpstream(ctx, path, m)
			err = e
			if err == nil && upstream != p.upstream {
				err = errors.New("upstream changed during preflight")
			}
		}
		if err == nil && !m.Status.ComparisonKnown {
			err = errors.New("upstream comparison is unknown after fetch; no valid tracking commit")
		}
		if err == nil && action == Push {
			if m.Status.Behind > 0 {
				err = errors.New("upstream is ahead or histories diverged; push skipped")
			} else if m.Status.Ahead <= 0 {
				err = errors.New("no outgoing commits to push")
			}
			if err == nil {
				p.push, err = sync.ResolvePush(ctx, path, m, p.upstream)
				p.display.Commit = m.Status.HeadOID
				p.display.Scope = p.push.SourceRef + " -> " + p.push.DestinationRef
				p.display.URL = gitcli.SafeText(p.push.URL)
				p.display.DirtyExcluded = m.Status.Dirty()
				p.display.Reason = "Push only reviewed existing commits; uncommitted changes are excluded; no force or implicit tags"
			}
		}
		if err == nil && action == Pull {
			if m.Status.Ahead > 0 && m.Status.Behind > 0 {
				err = errors.New("histories diverged; fast-forward-only integration is impossible")
			} else if m.Status.Behind == 0 {
				err = errors.New("equal or ahead-only branch needs no integration")
			}
			if err == nil {
				p.commit, err = sync.UpstreamCommit(ctx, path, p.upstream)
				p.display.Commit = p.commit
				p.display.Scope = p.upstream.Branch + " -> refs/heads/" + m.Status.Branch
				p.display.Reason = "Integrate exactly the reviewed commit with fast-forward-only and autostash disabled"
			}
		}
	}
	if err != nil {
		p.display.Reason = gitcli.SafeText(err.Error())
	} else {
		p.display.Eligible = true
	}
	return p
}

// PrepareSync turns a reviewed fetch scope into a final push/pull preview. It
// fetches only targets whose local state/config still match the original scope.
// The final integration/push always needs its own confirmation.
func (a *Actions) PrepareSync(ctx context.Context, id uint64, action Action) (Preview, error) {
	if action != Push && action != Pull {
		return Preview{}, errors.New("a synchronization action is required")
	}
	a.mu.Lock()
	if !a.active || a.pendingID != id || len(a.pending) == 0 {
		a.mu.Unlock()
		return Preview{}, errors.New("fetch-scope preview is no longer active")
	}
	items := a.pending
	for _, p := range items {
		if p.display.Action != Fetch {
			a.mu.Unlock()
			return Preview{}, errors.New("review fetch scope first")
		}
	}
	a.pending = nil
	a.pendingID = 0
	a.nextID++
	finalID := a.nextID
	a.mu.Unlock()
	completed := false
	defer func() {
		if !completed {
			a.mu.Lock()
			a.active = false
			a.mu.Unlock()
		}
	}()
	planned := make([]plannedTarget, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return Preview{}, err
		}
		if !item.display.Eligible {
			item.display.Action = action
			planned = append(planned, item)
			continue
		}
		planned = append(planned, a.planOne(ctx, action, item.display.Path, &item.metadata))
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	a.mu.Lock()
	a.pendingID = finalID
	a.pending = planned
	a.mu.Unlock()
	completed = true
	preview := Preview{ID: finalID, Targets: make([]Target, len(planned))}
	for i, p := range planned {
		preview.Targets[i] = p.display
	}
	return preview, nil
}

func (a *Actions) revalidateSync(ctx context.Context, p plannedTarget, m gitcli.Metadata) error {
	sync := a.service.(SyncGit)
	if err := syncEligibility(m.Status, p.display.Action); err != nil {
		return err
	}
	upstream, err := sync.ResolveUpstream(ctx, p.display.Path, m)
	if err != nil {
		return err
	}
	if upstream != p.upstream {
		return errors.New("upstream changed since preview")
	}
	if !m.Status.ComparisonKnown {
		return errors.New("upstream comparison became unknown")
	}
	if p.display.Action == Push {
		target, err := sync.ResolvePush(ctx, p.display.Path, m, upstream)
		if err != nil {
			return err
		}
		if target != p.push {
			return errors.New("push target changed since preview")
		}
		if m.Status.Behind > 0 || m.Status.Ahead <= 0 {
			return errors.New("outgoing comparison changed since preview")
		}
	} else {
		if m.Status.Ahead > 0 || m.Status.Behind <= 0 {
			return errors.New("fast-forward comparison changed since preview")
		}
		commit, err := sync.UpstreamCommit(ctx, p.display.Path, upstream)
		if err != nil {
			return err
		}
		if commit != p.commit {
			return errors.New("upstream commit changed since preview")
		}
	}
	return nil
}
