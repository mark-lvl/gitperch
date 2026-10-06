package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// CleanupKind names what a cleanup item does.
type CleanupKind string

const (
	CleanupGroup   CleanupKind = "inspect"
	PruneStale     CleanupKind = "prune stale worktrees"
	RemoveWorktree CleanupKind = "remove worktree"
	DeleteBranch   CleanupKind = "delete branch"
)

// CleanupItem is one reviewable cleanup step.
type CleanupItem struct {
	ID       string
	Group    string // main worktree path as Git reports it
	Kind     CleanupKind
	Path     string // worktree path; group path for prune and branches
	Branch   string
	OID      string
	Stale    []string // PruneStale: reviewed stale worktree paths, sorted
	Base     string   // e.g. refs/remotes/origin/main
	BaseName string   // e.g. origin/main, or "main (local default)"
	Eligible bool
	Reason   string
	Failed   bool
}

// CleanupPreview is a detached copy of the pending cleanup plan.
type CleanupPreview struct {
	ID    uint64
	Items []CleanupItem
}

// CleanupGit is the Git surface cleanup needs beyond fetch, push and pull.
type CleanupGit interface {
	ActionGit
	SupportsWorktreeInventory(context.Context) bool
	Worktrees(context.Context, string) ([]gitcli.Worktree, error)
	ResolveRemote(context.Context, string, gitcli.Metadata, string) (gitcli.FetchTarget, error)
	RemoteDefaultRef(context.Context, string, string) (string, error)
	ResolveCommit(context.Context, string, string) (string, error)
	IsAncestor(context.Context, string, string, string) (bool, error)
	IgnoredFiles(context.Context, string) ([]string, error)
	PruneWorktrees(context.Context, string) error
	RemoveWorktree(context.Context, string, string) error
	LocalBranches(context.Context, string) ([]gitcli.Branch, error)
	DeleteBranch(context.Context, string, string, string) error
}

var ErrCleanupUnsupported = errors.New("worktree cleanup needs Git 2.36 or newer")

// CleanupSupported lets the TUI hide Clean up on older Git.
func (a *Actions) CleanupSupported(ctx context.Context) bool {
	git, ok := a.service.(CleanupGit)
	return ok && git.SupportsWorktreeInventory(ctx)
}

type plannedCleanup struct {
	item   CleanupItem
	common string
}

type cleanupBase struct {
	ref, name, reason string
	branch            string // raw default branch name, never shown; skipped when deleting branches
	failed            bool
}

func kindOrder(k CleanupKind) int {
	return map[CleanupKind]int{CleanupGroup: 0, PruneStale: 1, RemoveWorktree: 2, DeleteBranch: 3}[k]
}

// PlanCleanup groups paths by common Git directory and plans each group:
// fetch, resolve the default ref, then classify worktrees (and, in phase 3,
// branches). Like Plan it holds the single active preview until Execute or
// Discard.
func (a *Actions) PlanCleanup(ctx context.Context, paths []string) (CleanupPreview, error) {
	git, ok := a.service.(CleanupGit)
	if !ok || !git.SupportsWorktreeInventory(ctx) {
		return CleanupPreview{}, ErrCleanupUnsupported
	}
	if len(paths) == 0 {
		return CleanupPreview{}, errors.New("select repositories explicitly before a batch action")
	}
	a.mu.Lock()
	if a.active {
		a.mu.Unlock()
		return CleanupPreview{}, errors.New("another preview or batch is active")
	}
	a.active = true
	a.nextID++
	id := a.nextID
	a.mu.Unlock()
	done := false
	defer func() {
		if !done {
			a.mu.Lock()
			a.active = false
			a.mu.Unlock()
		}
	}()

	type group struct {
		path     string
		metadata gitcli.Metadata
	}
	var groups []group
	var planned []plannedCleanup
	seen := map[string]bool{}
	paths = slices.Clone(paths)
	sort.Strings(paths)
	for _, path := range paths {
		m, err := a.service.Metadata(ctx, path)
		if err != nil {
			planned = append(planned, plannedCleanup{item: CleanupItem{ID: string(CleanupGroup) + "\x00" + path, Group: path, Kind: CleanupGroup, Path: path, Reason: gitcli.SafeText(err.Error()), Failed: true}})
			continue
		}
		if seen[m.CommonDir] {
			continue
		}
		seen[m.CommonDir] = true
		groups = append(groups, group{path, m})
	}
	results := make([][]plannedCleanup, len(groups))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(a.workers, max(1, len(groups))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lock := a.lockFor(groups[i].metadata.CommonDir)
				select {
				case <-ctx.Done():
					continue
				case <-lock:
				}
				results[i] = a.planCleanupGroup(ctx, git, groups[i].path, groups[i].metadata)
				lock <- struct{}{}
			}
		}()
	}
	for i := range groups {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return CleanupPreview{}, err
	}
	for _, r := range results {
		planned = append(planned, r...)
	}
	sort.SliceStable(planned, func(i, j int) bool {
		a, b := planned[i].item, planned[j].item
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if kindOrder(a.Kind) != kindOrder(b.Kind) {
			return kindOrder(a.Kind) < kindOrder(b.Kind)
		}
		return a.Path+a.Branch < b.Path+b.Branch
	})
	a.mu.Lock()
	a.pendingID = id
	a.pendingCleanup = planned
	if a.pendingCleanup == nil {
		a.pendingCleanup = []plannedCleanup{}
	}
	a.mu.Unlock()
	done = true
	preview := CleanupPreview{ID: id, Items: make([]CleanupItem, len(planned))}
	for i, p := range planned {
		preview.Items[i] = p.item
		preview.Items[i].Stale = slices.Clone(p.item.Stale)
	}
	return preview, nil
}

// resolveBase fetches the cleanup remote and finds the default ref. Without
// remotes the local main, else master, is the base.
func (a *Actions) resolveBase(ctx context.Context, git CleanupGit, path string, m gitcli.Metadata, fetch bool) cleanupBase {
	if len(m.Remotes) == 0 {
		for _, name := range []string{"main", "master"} {
			if _, err := git.ResolveCommit(ctx, path, "refs/heads/"+name); err == nil {
				return cleanupBase{ref: "refs/heads/" + name, name: name + " (local default)", branch: name}
			}
		}
		return cleanupBase{reason: "no remote and no local main or master branch"}
	}
	remote := ""
	switch {
	case slices.Contains(m.Remotes, "origin"):
		remote = "origin"
	case len(m.Remotes) == 1:
		remote = m.Remotes[0]
	default:
		return cleanupBase{reason: "several remotes and none named origin"}
	}
	if fetch {
		target, err := git.ResolveRemote(ctx, path, m, remote)
		if err == nil {
			err = git.Fetch(ctx, path, target)
		}
		if err != nil {
			return cleanupBase{reason: "preflight fetch failed: " + gitcli.SafeText(err.Error()), failed: true}
		}
	}
	ref, err := git.RemoteDefaultRef(ctx, path, remote)
	if errors.Is(err, gitcli.ErrNoDefaultRef) {
		return cleanupBase{reason: fmt.Sprintf("default branch unknown — run git remote set-head %s -a", gitcli.SafeText(remote))}
	}
	if err == nil {
		_, err = git.ResolveCommit(ctx, path, ref)
	}
	if err != nil {
		return cleanupBase{reason: "default branch unknown: " + gitcli.SafeText(err.Error())}
	}
	branch, ok := strings.CutPrefix(ref, "refs/remotes/"+remote+"/")
	if !ok || branch == "" {
		// Without the branch name the default branch could not be protected.
		return cleanupBase{reason: "default branch unknown: " + gitcli.SafeText(ref) + " is outside refs/remotes/" + gitcli.SafeText(remote) + "/"}
	}
	return cleanupBase{ref: ref, name: gitcli.SafeText(strings.TrimPrefix(ref, "refs/remotes/")), branch: branch}
}

func (a *Actions) planCleanupGroup(ctx context.Context, git CleanupGit, path string, m gitcli.Metadata) []plannedCleanup {
	base := a.resolveBase(ctx, git, path, m, true)
	worktrees, err := git.Worktrees(ctx, path)
	if err != nil {
		return []plannedCleanup{{common: m.CommonDir, item: CleanupItem{ID: string(CleanupGroup) + "\x00" + path, Group: path, Kind: CleanupGroup, Path: path, Reason: gitcli.SafeText(err.Error()), Failed: true}}}
	}
	if len(worktrees) == 0 {
		return []plannedCleanup{{common: m.CommonDir, item: CleanupItem{ID: string(CleanupGroup) + "\x00" + path, Group: path, Kind: CleanupGroup, Path: path, Reason: "no worktrees listed", Failed: true}}}
	}
	group := worktrees[0].Path
	var out []plannedCleanup
	add := func(item CleanupItem) {
		item.Group, item.Base, item.BaseName = group, base.ref, base.name
		item.ID = string(item.Kind) + "\x00" + group + "\x00" + item.Path + "\x00" + item.Branch
		out = append(out, plannedCleanup{item: item, common: m.CommonDir})
	}
	if base.failed { // eligible prune items below do not depend on the base
		add(CleanupItem{Kind: CleanupGroup, Path: group, Reason: base.reason, Failed: true})
	}
	var stale []string
	for _, wt := range worktrees {
		if wt.Main {
			continue
		}
		if isStale(wt) {
			if wt.Locked { // never pruned by Git; the user must unlock it first
				add(CleanupItem{Kind: RemoveWorktree, Path: wt.Path, Branch: wt.Branch, Reason: lockReason(wt) + " (directory missing)"})
			} else {
				stale = append(stale, wt.Path)
			}
			continue
		}
		item := CleanupItem{Kind: RemoveWorktree, Path: wt.Path, Branch: wt.Branch, OID: wt.HeadOID}
		item.Reason = a.worktreeBlocker(ctx, git, wt, base, "")
		item.Eligible = item.Reason == ""
		if item.Eligible {
			item.Reason = "merged into " + base.name
		}
		add(item)
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		add(CleanupItem{Kind: PruneStale, Path: group, Stale: stale, Eligible: true, Reason: fmt.Sprintf("%d stale worktree record(s)", len(stale))})
	}
	if base.ref == "" {
		return out
	}
	// A worktree planned for removal frees its branch, per worktree: the same
	// branch can be checked out in several. A stale record frees it too, because
	// prune runs first, unless it is locked (prune never removes those).
	removed := map[string]bool{}
	for _, p := range out {
		if p.item.Kind == RemoveWorktree && p.item.Eligible {
			removed[p.item.Path] = true
		}
	}
	checkedOut := map[string]string{}
	for _, wt := range worktrees {
		if wt.Branch != "" && holdsBranch(wt) && !removed[wt.Path] {
			checkedOut[wt.Branch] = wt.Path
		}
	}
	busy := operationBlocker(ctx, git, worktrees)
	branches, err := git.LocalBranches(ctx, path)
	if err != nil {
		add(CleanupItem{Kind: CleanupGroup, Path: group, Reason: "branch list failed: " + gitcli.SafeText(err.Error()), Failed: true})
		return out
	}
	for _, b := range branches {
		if b.Name == base.branch {
			continue
		}
		merged, err := git.IsAncestor(ctx, path, b.OID, base.ref)
		item := CleanupItem{Kind: DeleteBranch, Path: group, Branch: b.Name, OID: b.OID}
		switch {
		case err != nil:
			item.Reason = "merge check failed: " + gitcli.SafeText(err.Error())
		case merged && busy != "":
			item.Reason = busy
		case merged && checkedOut[b.Name] != "":
			item.Reason = "merged but checked out in " + gitcli.SafeText(filepath.Base(checkedOut[b.Name]))
		case merged:
			item.Eligible, item.Reason = true, "merged into "+base.name
		case b.Gone:
			item.Reason = "upstream gone but not merged into " + base.name + " — squash merge?"
		default:
			continue // ordinary unmerged branch: not a cleanup candidate
		}
		add(item)
	}
	return out
}

// isStale reports a linked worktree whose record outlived its directory.
// Planning and revalidation share it so the reviewed set compares equal.
func isStale(wt gitcli.Worktree) bool { return wt.Prunable || missingDir(wt.Path) }

// holdsBranch reports whether a worktree keeps its branch checked out. Only a
// prunable record frees it; a locked one stays (its directory may be on a
// drive that is not mounted) and prune never removes it.
func holdsBranch(wt gitcli.Worktree) bool { return !isStale(wt) || wt.Locked }

// operationBlocker returns why no branch may be deleted while a worktree is
// mid rebase, bisect, merge or similar: Git then lists that worktree as
// detached, so its branch looks free although it is in use. An unreadable
// worktree blocks too. "" means every inspectable worktree is idle.
func operationBlocker(ctx context.Context, git CleanupGit, worktrees []gitcli.Worktree) string {
	for _, wt := range worktrees {
		if wt.Bare || isStale(wt) {
			continue
		}
		name := gitcli.SafeText(filepath.Base(wt.Path))
		s := git.Inspect(ctx, wt.Path)
		switch {
		case s.Error != "":
			return "inspection failed in " + name + ": " + gitcli.SafeText(s.Error)
		case s.Operation != "":
			return "operation in progress in " + name
		}
	}
	return ""
}

func lockReason(wt gitcli.Worktree) string {
	if wt.LockReason != "" {
		return "locked: " + gitcli.SafeText(wt.LockReason)
	}
	return "locked"
}

// worktreeBlocker returns why a linked worktree must stay, or "" when it is
// clean, unlocked, free of ignored files and merged into the base. A non-empty
// wantOID also requires the inspected HEAD to be that commit, so the commit
// that is merge-checked is the reviewed one.
func (a *Actions) worktreeBlocker(ctx context.Context, git CleanupGit, wt gitcli.Worktree, base cleanupBase, wantOID string) string {
	if wt.Locked {
		return lockReason(wt)
	}
	s := git.Inspect(ctx, wt.Path)
	switch {
	case s.Error != "":
		return "inspection failed: " + gitcli.SafeText(s.Error)
	case s.Operation != "":
		return "operation in progress: " + gitcli.SafeText(s.Operation)
	case s.Conflicts > 0:
		return fmt.Sprintf("%d conflicts", s.Conflicts)
	case s.Dirty():
		return fmt.Sprintf("dirty (%d files)", s.Changes+s.Untracked)
	case s.Unborn:
		return "no commits"
	case wantOID != "" && s.HeadOID != wantOID:
		return "worktree HEAD moved since review"
	}
	ignored, err := git.IgnoredFiles(ctx, wt.Path)
	switch {
	case errors.Is(err, gitcli.ErrOutputLimit):
		return "too many ignored files to list"
	case err != nil:
		return "could not list ignored files: " + gitcli.SafeText(err.Error())
	case len(ignored) > 0:
		shown := ignored[:min(3, len(ignored))]
		text := fmt.Sprintf("%d ignored file(s) (%s", len(ignored), gitcli.SafeText(strings.Join(shown, ", ")))
		if len(ignored) > 3 {
			text += ", …"
		}
		return text + ")"
	}
	if base.ref == "" {
		return base.reason
	}
	merged, err := git.IsAncestor(ctx, wt.Path, s.HeadOID, base.ref)
	if err != nil {
		return "merge check failed: " + gitcli.SafeText(err.Error())
	}
	if !merged {
		return "not merged into " + base.name
	}
	return ""
}

// ExecuteCleanup runs the chosen eligible items of the pending cleanup.
// Groups run concurrently; items within a group run in kind order under the
// group's lock, each revalidated immediately before it runs.
func (a *Actions) ExecuteCleanup(ctx context.Context, id uint64, chosen []string, emit func(Event)) ([]Event, error) {
	a.mu.Lock()
	if !a.active || a.pendingID != id || a.pendingCleanup == nil {
		a.mu.Unlock()
		return nil, errors.New("preview is no longer active")
	}
	items := a.pendingCleanup
	a.pendingCleanup = nil
	a.pendingID = 0
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.active = false; a.mu.Unlock() }()
	if emit == nil {
		emit = func(Event) {}
	}
	git := a.service.(CleanupGit)
	want := map[string]bool{}
	for _, id := range chosen {
		want[id] = true
	}
	byGroup := map[string][]plannedCleanup{}
	var order []string
	for _, p := range items {
		if !p.item.Eligible || !want[p.item.ID] {
			continue
		}
		if byGroup[p.item.Group] == nil {
			order = append(order, p.item.Group)
		}
		byGroup[p.item.Group] = append(byGroup[p.item.Group], p)
		emit(Event{Path: p.item.Path, Item: p.item.ID, State: Queued})
	}
	var mu sync.Mutex
	var results []Event
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(a.workers, max(1, len(order))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range jobs {
				for _, p := range byGroup[group] {
					event := a.executeCleanupItem(ctx, git, p, emit)
					emit(event)
					mu.Lock()
					results = append(results, event)
					mu.Unlock()
				}
			}
		}()
	}
	for _, g := range order {
		jobs <- g
	}
	close(jobs)
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool { return results[i].Item < results[j].Item })
	return results, ctx.Err()
}

func (a *Actions) executeCleanupItem(ctx context.Context, git CleanupGit, p plannedCleanup, emit func(Event)) Event {
	item := p.item
	result := Event{Path: item.Path, Item: item.ID}
	lock := a.lockFor(p.common)
	select {
	case <-ctx.Done():
		result.State, result.Message = Cancelled, ctx.Err().Error()
		return result
	case <-lock:
	}
	defer func() { lock <- struct{}{} }()
	if err := a.revalidateCleanup(ctx, git, item); err != nil {
		result.State, result.Message = Skipped, gitcli.SafeText(err.Error())
		if ctx.Err() != nil {
			result.State = Cancelled
		}
		return result
	}
	emit(Event{Path: item.Path, Item: item.ID, State: Running, Message: string(item.Kind)})
	var err error
	switch item.Kind {
	case PruneStale:
		err = git.PruneWorktrees(ctx, item.Group)
	case RemoveWorktree:
		err = git.RemoveWorktree(ctx, item.Group, item.Path)
	case DeleteBranch:
		err = git.DeleteBranch(ctx, item.Group, item.Branch, item.OID)
		if err != nil && strings.HasPrefix(err.Error(), "branch deleted;") {
			// The ref is gone; report success and keep the recovery command.
			result.State = Succeeded
			result.Message = deletedBranchMessage(item) + " · " + gitcli.SafeText(err.Error())
			return result
		}
	default:
		err = fmt.Errorf("unsupported cleanup item %q", item.Kind)
	}
	if err != nil {
		result.State = Failed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, gitcli.ErrOutputLimit) {
			result.State = OutcomeUnknown
		}
		result.Message = gitcli.SafeText(err.Error())
		return result
	}
	result.State = Succeeded
	switch item.Kind {
	case PruneStale:
		result.Message = fmt.Sprintf("pruned %d stale worktree record(s)", len(item.Stale))
	case RemoveWorktree:
		result.Message = "removed worktree " + gitcli.SafeText(item.Path)
	case DeleteBranch:
		result.Message = deletedBranchMessage(item)
	}
	return result
}

func deletedBranchMessage(item CleanupItem) string {
	b := gitcli.SafeText(item.Branch)
	return fmt.Sprintf("deleted %s · restore: git branch %s %s", b, b, item.OID)
}

func (a *Actions) revalidateCleanup(ctx context.Context, git CleanupGit, item CleanupItem) error {
	worktrees, err := git.Worktrees(ctx, item.Group)
	if err != nil {
		return err
	}
	switch item.Kind {
	case PruneStale:
		var stale []string
		for _, wt := range worktrees {
			if !wt.Main && isStale(wt) && !wt.Locked {
				stale = append(stale, wt.Path)
			}
		}
		sort.Strings(stale)
		if !slices.Equal(stale, item.Stale) {
			return errors.New("stale worktrees changed since review")
		}
		return nil
	case RemoveWorktree:
		for _, wt := range worktrees {
			if wt.Path != item.Path {
				continue
			}
			if wt.Main || isStale(wt) {
				return errors.New("worktree changed since review")
			}
			if wt.HeadOID != item.OID {
				return errors.New("worktree HEAD moved since review")
			}
			if blocker := a.worktreeBlocker(ctx, git, wt, cleanupBase{ref: item.Base, name: item.BaseName}, item.OID); blocker != "" {
				return errors.New(blocker)
			}
			return nil
		}
		return errors.New("worktree no longer exists")
	case DeleteBranch:
		// update-ref does not look at worktrees, so check them here.
		for _, wt := range worktrees {
			if wt.Branch == item.Branch && holdsBranch(wt) {
				return fmt.Errorf("checked out in %s", filepath.Base(wt.Path))
			}
		}
		if busy := operationBlocker(ctx, git, worktrees); busy != "" {
			return errors.New(busy)
		}
		branches, err := git.LocalBranches(ctx, item.Group)
		if err != nil {
			return err
		}
		for _, b := range branches {
			if b.Name != item.Branch {
				continue
			}
			if b.OID != item.OID {
				return errors.New("branch moved since review")
			}
			merged, err := git.IsAncestor(ctx, item.Group, b.OID, item.Base)
			if err != nil {
				return err
			}
			if !merged {
				return errors.New("no longer merged into " + item.BaseName)
			}
			return nil
		}
		return errors.New("branch no longer exists")
	}
	return fmt.Errorf("unsupported cleanup item %q", item.Kind)
}
