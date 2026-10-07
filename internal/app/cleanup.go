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
	"github.com/mark-lvl/gitperch/internal/repository"
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
	ID         string
	Group      string // main worktree path as Git reports it
	Kind       CleanupKind
	Path       string // worktree path; group path for prune and branches
	Branch     string
	OID        string
	Stale      []string // PruneStale: reviewed stale worktree paths, sorted
	Base       string   // e.g. refs/remotes/origin/main
	BaseName   string   // e.g. origin/main, or "main (local default)"
	Assessment CleanupAssessment
}

// Eligible reports whether the item may run: every check passed.
func (i CleanupItem) Eligible() bool { return i.Assessment.Allowed() }

// Failed reports a planning step that failed for a whole repository, such as
// its preflight fetch; such items are never runnable.
func (i CleanupItem) Failed() bool { return i.Kind == CleanupGroup }

// groupFailure reports a planning step that failed for the repository at path.
func groupFailure(path string, code CleanupCode, text string) CleanupItem {
	return CleanupItem{ID: string(CleanupGroup) + "\x00" + path, Group: path, Kind: CleanupGroup, Path: path, Assessment: Assess(CleanupReason{Code: code, Text: text})}
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
	Operation(context.Context, string) (string, error)
	ResolveRemote(context.Context, string, gitcli.Metadata, string) (gitcli.FetchTarget, error)
	RemoteDefaultRef(context.Context, string, string) (string, error)
	ResolveCommit(context.Context, string, string) (string, error)
	IsAncestor(context.Context, string, string, string) (bool, error)
	ReachableFromRefs(context.Context, string, string) (bool, error)
	IgnoredFiles(context.Context, string) ([]string, error)
	HiddenChanges(context.Context, string) ([]string, error)
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
	ref, name string
	reason    CleanupReason // why ref is empty
	branch    string        // raw default branch name, never shown; skipped when deleting branches
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
			planned = append(planned, plannedCleanup{item: groupFailure(path, CleanupInspectionFailed, gitcli.SafeText(err.Error()))})
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
		preview.Items[i].Assessment.Reasons = slices.Clone(p.item.Assessment.Reasons)
	}
	return preview, nil
}

// resolveBase fetches the cleanup remote and finds the default ref; see
// defaultBase.
func (a *Actions) resolveBase(ctx context.Context, git CleanupGit, path string, m gitcli.Metadata) cleanupBase {
	return defaultBase(ctx, git, path, m.Remotes, func(remote string) error {
		target, err := git.ResolveRemote(ctx, path, m, remote)
		if err == nil {
			err = git.Fetch(ctx, path, target)
		}
		return err
	})
}

// baseResolver reads the refs that name a repository's default branch.
type baseResolver interface {
	RemoteDefaultRef(context.Context, string, string) (string, error)
	ResolveCommit(context.Context, string, string) (string, error)
}

// defaultBase finds the default branch merges are judged against: the
// remote's HEAD for origin or a sole remote, or without remotes the local
// main, else master. A non-nil fetch refreshes the remote first; without it
// only local refs are read.
func defaultBase(ctx context.Context, git baseResolver, path string, remotes []string, fetch func(string) error) cleanupBase {
	unknown := func(text string) cleanupBase {
		return cleanupBase{reason: CleanupReason{Code: CleanupDefaultBranchUnknown, Text: text}}
	}
	if len(remotes) == 0 {
		for _, name := range []string{"main", "master"} {
			if _, err := git.ResolveCommit(ctx, path, "refs/heads/"+name); err == nil {
				return cleanupBase{ref: "refs/heads/" + name, name: name + " (local default)", branch: name}
			}
		}
		return unknown("no remote and no local main or master branch")
	}
	remote := ""
	switch {
	case slices.Contains(remotes, "origin"):
		remote = "origin"
	case len(remotes) == 1:
		remote = remotes[0]
	default:
		return unknown("several remotes and none named origin")
	}
	if fetch != nil {
		if err := fetch(remote); err != nil {
			return cleanupBase{reason: CleanupReason{Code: CleanupFetchFailed, Text: "preflight fetch failed: " + gitcli.SafeText(err.Error())}}
		}
	}
	ref, err := git.RemoteDefaultRef(ctx, path, remote)
	if errors.Is(err, gitcli.ErrNoDefaultRef) {
		return unknown(fmt.Sprintf("default branch unknown — run git remote set-head %s -a", gitcli.SafeText(remote)))
	}
	if err == nil {
		_, err = git.ResolveCommit(ctx, path, ref)
	}
	if err != nil {
		return unknown("default branch unknown: " + gitcli.SafeText(err.Error()))
	}
	branch, ok := strings.CutPrefix(ref, "refs/remotes/"+remote+"/")
	if !ok || branch == "" {
		// Without the branch name the default branch could not be protected.
		return unknown("default branch unknown: " + gitcli.SafeText(ref) + " is outside refs/remotes/" + gitcli.SafeText(remote) + "/")
	}
	return cleanupBase{ref: ref, name: gitcli.SafeText(strings.TrimPrefix(ref, "refs/remotes/")), branch: branch}
}

func (a *Actions) planCleanupGroup(ctx context.Context, git CleanupGit, path string, m gitcli.Metadata) []plannedCleanup {
	base := a.resolveBase(ctx, git, path, m)
	worktrees, err := git.Worktrees(ctx, path)
	if err != nil {
		return []plannedCleanup{{common: m.CommonDir, item: groupFailure(path, CleanupCheckFailed, gitcli.SafeText(err.Error()))}}
	}
	if len(worktrees) == 0 {
		return []plannedCleanup{{common: m.CommonDir, item: groupFailure(path, CleanupCheckFailed, "no worktrees listed")}}
	}
	group := worktrees[0].Path
	var out []plannedCleanup
	add := func(item CleanupItem) {
		item.Group, item.Base, item.BaseName = group, base.ref, base.name
		item.ID = string(item.Kind) + "\x00" + group + "\x00" + item.Path + "\x00" + item.Branch
		out = append(out, plannedCleanup{item: item, common: m.CommonDir})
	}
	if base.reason.Code == CleanupFetchFailed { // eligible prune items below do not depend on the base
		add(CleanupItem{Kind: CleanupGroup, Path: group, Assessment: Assess(base.reason)})
	}
	var stale []string
	for _, wt := range worktrees {
		if wt.Main {
			continue // assessWorktree refuses it too; listing it would only add noise
		}
		if isStale(wt) {
			if wt.Locked { // never pruned by Git; the user must unlock it first
				locked := lockReason(wt)
				locked.Text += " (directory missing)"
				add(CleanupItem{Kind: RemoveWorktree, Path: wt.Path, Branch: wt.Branch, Assessment: Assess(locked)})
			} else {
				stale = append(stale, wt.Path)
			}
			continue
		}
		item := CleanupItem{Kind: RemoveWorktree, Path: wt.Path, Branch: wt.Branch, OID: wt.HeadOID}
		item.Assessment = assessWorktree(ctx, git, wt, base, nil)
		add(item)
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		add(CleanupItem{Kind: PruneStale, Path: group, Stale: stale, Assessment: assessPrune(ctx, git, path, worktrees, stale)})
	}
	if base.ref == "" {
		return out
	}
	// A worktree planned for removal frees its branch, per worktree: the same
	// branch can be checked out in several. A stale record frees it too, because
	// prune runs first, unless it is locked (prune never removes those).
	removed := map[string]bool{}
	for _, p := range out {
		if p.item.Kind == RemoveWorktree && p.item.Eligible() {
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
		add(CleanupItem{Kind: CleanupGroup, Path: group, Assessment: Assess(CleanupReason{Code: CleanupCheckFailed, Text: "branch list failed: " + gitcli.SafeText(err.Error())})})
		return out
	}
	for _, b := range branches {
		if b.Name == base.branch || b.Symref != "" {
			continue // the default branch, or an alias whose deletion Git must never follow
		}
		merged, err := git.IsAncestor(ctx, path, b.OID, base.ref)
		item := CleanupItem{Kind: DeleteBranch, Path: group, Branch: b.Name, OID: b.OID}
		var reason CleanupReason
		switch {
		case err != nil:
			reason = CleanupReason{Code: CleanupCheckFailed, Text: "merge check failed: " + gitcli.SafeText(err.Error())}
		case merged && busy.Code != "":
			reason = busy
		case merged && checkedOut[b.Name] != "":
			reason = CleanupReason{Code: CleanupCheckedOutElsewhere, Text: "merged but checked out in " + gitcli.SafeText(filepath.Base(checkedOut[b.Name]))}
		case merged:
			reason = CleanupReason{Code: CleanupMerged, Text: "merged into " + base.name}
		case b.Gone:
			reason = CleanupReason{Code: CleanupUpstreamGone, Text: "upstream gone but not merged into " + base.name + " — squash merge?"}
		default:
			continue // ordinary unmerged branch: not a cleanup candidate
		}
		item.Assessment = Assess(reason)
		add(item)
	}
	return out
}

// staleNames lists up to three stale directory names for the review.
func staleNames(stale []string) string {
	var names []string
	for _, p := range stale[:min(3, len(stale))] {
		names = append(names, gitcli.SafeText(filepath.Base(p)))
	}
	text := strings.Join(names, ", ")
	if len(stale) > 3 {
		text += ", …"
	}
	return text
}

// assessPrune allows pruning the stale records only while every record's HEAD
// commit stays reachable: pruning drops a record's reflog, so a commit no
// branch, remote-tracking branch or tag contains would be stranded, and an
// unanswerable check is treated the same. Every such record is named, since
// each needs its own rescue command. Planning and revalidation share it.
func assessPrune(ctx context.Context, git CleanupGit, path string, worktrees []gitcli.Worktree, stale []string) CleanupAssessment {
	var a CleanupAssessment
	for _, wt := range worktrees {
		if wt.Main || !slices.Contains(stale, wt.Path) {
			continue
		}
		if wt.HeadOID == "" || strings.Trim(wt.HeadOID, "0") == "" {
			continue // unborn: no commit to lose
		}
		name := gitcli.SafeText(filepath.Base(wt.Path))
		reachable, err := git.ReachableFromRefs(ctx, path, wt.HeadOID)
		switch {
		case err != nil:
			a.add(CleanupCheckFailed, fmt.Sprintf("stale worktree %s: reachability check failed: %s", name, gitcli.SafeText(err.Error())))
		case !reachable:
			a.add(CleanupUnreferencedCommit, fmt.Sprintf("stale worktree %s holds unreferenced commit %s — create a branch first: %s",
				name, shortOID(wt.HeadOID), rescueCommand(wt)))
		}
	}
	if len(a.Reasons) == 0 {
		a.add(CleanupDirectoryMissing, "directory missing: "+staleNames(stale))
	}
	return a
}

func shortOID(oid string) string { return oid[:min(7, len(oid))] }

// rescueCommand creates a branch at a worktree's HEAD so its commit outlives
// the worktree, run from inside the repository.
func rescueCommand(wt gitcli.Worktree) string {
	return fmt.Sprintf("git branch %s %s", shellQuote("rescue/"+gitcli.SafeText(filepath.Base(wt.Path))), wt.HeadOID)
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
// worktree blocks too. A zero reason means every inspectable worktree is idle.
func operationBlocker(ctx context.Context, git CleanupGit, worktrees []gitcli.Worktree) CleanupReason {
	for _, wt := range worktrees {
		if wt.Bare || isStale(wt) {
			continue
		}
		name := gitcli.SafeText(filepath.Base(wt.Path))
		operation, err := git.Operation(ctx, wt.Path)
		switch {
		case err != nil:
			return CleanupReason{Code: CleanupInspectionFailed, Text: "inspection failed in " + name + ": " + gitcli.SafeText(err.Error())}
		case operation != "":
			return CleanupReason{Code: CleanupOperation, Text: operationName(operation) + " in progress in " + name}
		}
	}
	return CleanupReason{}
}

// fileList summarizes files that keep a worktree, naming up to three.
func fileList(count int, what string, files []string) string {
	shown := files[:min(3, len(files))]
	text := fmt.Sprintf("%d %s (%s", count, what, gitcli.SafeText(strings.Join(shown, ", ")))
	if len(files) > 3 {
		text += ", …"
	}
	return text + ")"
}

func lockReason(wt gitcli.Worktree) CleanupReason {
	if wt.LockReason != "" {
		return CleanupReason{Code: CleanupLocked, Text: "locked: " + gitcli.SafeText(wt.LockReason)}
	}
	return CleanupReason{Code: CleanupLocked, Text: "locked"}
}

// headName names what a worktree has checked out.
func headName(branch string) string {
	if branch == "" {
		return "detached HEAD"
	}
	return "branch " + gitcli.SafeText(branch)
}

// headChange says how a worktree's HEAD differs between two readings, or ""
// when the commit and branch are the same.
func headChange(fromOID, fromBranch, toOID, toBranch, when string) string {
	switch {
	case fromOID != toOID:
		return "worktree HEAD moved " + when
	case fromBranch != toBranch:
		return fmt.Sprintf("worktree switched from %s to %s %s", headName(fromBranch), headName(toBranch), when)
	}
	return ""
}

// assessWorktree decides whether removing a linked worktree loses nothing:
// it must be unlocked, have no operation, conflicts, changes or untracked
// files, hold no ignored files or edits git status hides, and have its HEAD
// reachable from the default branch. Every concern found is reported, except
// that the whole-worktree file listings run only while nothing else keeps it.
// A non-nil reviewed item also requires the worktree to be on the reviewed
// commit and branch. Planning and revalidation share it.
func assessWorktree(ctx context.Context, git CleanupGit, wt gitcli.Worktree, base cleanupBase, reviewed *CleanupItem) CleanupAssessment {
	var as CleanupAssessment
	if wt.Main {
		as.add(CleanupMainWorktree, "main worktree")
		return as
	}
	if reviewed != nil {
		if change := headChange(reviewed.OID, reviewed.Branch, wt.HeadOID, wt.Branch, "since review"); change != "" {
			as.add(CleanupChangedSinceReview, change)
			return as
		}
	}
	if wt.Locked {
		locked := lockReason(wt)
		as.add(locked.Code, locked.Text)
		return as
	}
	s := git.Inspect(ctx, wt.Path)
	if s.Error != "" {
		as.add(CleanupInspectionFailed, "inspection failed: "+gitcli.SafeText(s.Error))
		return as
	}
	// The status must describe the commit Git listed, which is the one the
	// item records and the user reviews.
	if !s.Unborn {
		if change := headChange(wt.HeadOID, wt.Branch, s.HeadOID, s.Branch, "while it was checked"); change != "" {
			as.add(CleanupChangedSinceReview, change)
			return as
		}
	}
	if s.Operation != "" {
		as.add(CleanupOperation, operationName(s.Operation)+" in progress")
	}
	if s.Conflicts > 0 {
		as.add(CleanupConflicts, count(s.Conflicts, "conflicted file"))
	}
	if s.Changes > s.Conflicts {
		as.add(CleanupUncommitted, count(s.Changes-s.Conflicts, "uncommitted file"))
	}
	if s.Untracked > 0 {
		as.add(CleanupUntracked, count(s.Untracked, "untracked file"))
	}
	if s.Unborn {
		as.add(CleanupNoCommits, "no commits yet")
		return as
	}
	if len(as.Reasons) == 0 {
		as.add(CleanupClean, "working tree clean")
		assessLocalFiles(ctx, git, wt.Path, &as)
	}
	assessIntegration(ctx, git, wt.Path, s, base, &as)
	return as
}

// assessLocalFiles looks for files git worktree remove would delete although
// git status reports the worktree clean.
func assessLocalFiles(ctx context.Context, git CleanupGit, path string, as *CleanupAssessment) {
	ignored, err := git.IgnoredFiles(ctx, path)
	switch {
	case errors.Is(err, gitcli.ErrOutputLimit):
		as.add(CleanupIgnoredFiles, "too many ignored files to list")
	case err != nil:
		as.add(CleanupCheckFailed, "could not list ignored files: "+gitcli.SafeText(err.Error()))
	case len(ignored) > 0:
		as.add(CleanupIgnoredFiles, fileList(len(ignored), "ignored file(s)", ignored))
	default:
		as.add(CleanupNoIgnoredFiles, "no ignored files")
	}
	// git status and git worktree remove both overlook edits to files marked
	// assume-unchanged or skip-worktree, so removal would silently drop them.
	hidden, err := git.HiddenChanges(ctx, path)
	switch {
	case errors.Is(err, gitcli.ErrOutputLimit):
		as.add(CleanupCheckFailed, "too many tracked files to check for hidden changes")
	case err != nil:
		as.add(CleanupCheckFailed, "could not check for hidden changes: "+gitcli.SafeText(err.Error()))
	case len(hidden) > 0:
		as.add(CleanupHiddenChanges, fileList(len(hidden), "file(s) marked assume-unchanged or skip-worktree", hidden))
	default:
		as.add(CleanupNoHiddenChanges, "no hidden edits")
	}
}

// assessIntegration asks whether HEAD's commits exist beyond this worktree.
// Merged into the default branch allows removal. Otherwise it says where the
// commits are: only behind a detached HEAD, unpushed or diverged (blocked), or
// possibly elsewhere but not shown merged (needs review: a squash or rebase
// merge looks like this, and so does unfinished work).
func assessIntegration(ctx context.Context, git CleanupGit, path string, s repository.Status, base cleanupBase, as *CleanupAssessment) {
	if base.ref == "" {
		reason := base.reason
		if reason.Code == "" {
			reason = CleanupReason{Code: CleanupDefaultBranchUnknown, Text: "default branch unknown"}
		}
		as.add(reason.Code, reason.Text)
		return
	}
	merged, err := git.IsAncestor(ctx, path, s.HeadOID, base.ref)
	switch {
	case err != nil:
		as.add(CleanupCheckFailed, "merge check failed: "+gitcli.SafeText(err.Error()))
		return
	case merged:
		as.add(CleanupMerged, "merged into "+base.name)
		return
	}
	as.add(CleanupNotMerged, "not merged into "+base.name)
	upstream := gitcli.SafeText(s.Upstream)
	switch {
	case s.Detached:
		reachable, err := git.ReachableFromRefs(ctx, path, s.HeadOID)
		switch {
		case err != nil:
			as.add(CleanupCheckFailed, "reachability check failed: "+gitcli.SafeText(err.Error()))
		case !reachable:
			as.add(CleanupUnreferencedCommit, fmt.Sprintf("detached HEAD %s is on no branch or tag — create one first: %s",
				shortOID(s.HeadOID), rescueCommand(gitcli.Worktree{Path: path, HeadOID: s.HeadOID})))
		}
	case s.Upstream == "":
		as.add(CleanupNoUpstream, "branch has no upstream")
	case !s.ComparisonKnown:
		as.add(CleanupUpstreamGone, "upstream "+upstream+" is gone — squash merge?")
	case s.Ahead > 0 && s.Behind > 0:
		as.add(CleanupDiverged, fmt.Sprintf("diverged from %s: %d ahead, %d behind", upstream, s.Ahead, s.Behind))
	case s.Ahead > 0:
		as.add(CleanupUnpushed, count(s.Ahead, "commit")+" not pushed to "+upstream)
	}
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
		if !p.item.Eligible() || !want[p.item.ID] {
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
	if check := revalidateCleanup(ctx, git, item); !check.Allowed() {
		result.State, result.Message = Skipped, check.Summary()
		if check.Has(CleanupChangedSinceReview) {
			result.Message += " · review again"
		}
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
			a.noteRestore(&result, item)
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
		a.noteRestore(&result, item)
	}
	return result
}

// noteRestore attaches a deleted branch's restore command to its result and
// records it in the restore log. A log failure never undoes the success; the
// message says the command was not recorded.
func (a *Actions) noteRestore(result *Event, item CleanupItem) {
	result.Restore = restoreAnywhere(item)
	logged, err := a.recordRestore(item, result.Restore)
	if err != nil {
		result.Message += " · restore log not written: " + gitcli.SafeText(err.Error())
	}
	result.RestoreLogged = logged
}

// shellQuote makes a Git name safe to paste into a POSIX shell: names made of
// [A-Za-z0-9._/@+-] pass unchanged, anything else is single-quoted with ' as '\”.
func shellQuote(s string) string {
	plain := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._/@+-", r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// deletedBranchMessage reports a deletion with a copy-pasteable restore
// command. When SafeText has to alter the name (control characters, redacted
// credentials) the command no longer names the real branch, and the message
// says so; the commit stays recoverable through its OID.
func deletedBranchMessage(item CleanupItem) string {
	safe := gitcli.SafeText(item.Branch)
	msg := fmt.Sprintf("deleted %s · restore: %s", safe, restoreCommand(item))
	if safe != item.Branch {
		msg += " (the branch name contains unprintable characters or credentials and is shown altered; choose any name)"
	}
	return msg
}

// restoreCommand recreates a deleted branch at its reviewed commit, run from
// inside the repository.
func restoreCommand(item CleanupItem) string {
	return fmt.Sprintf("git branch %s %s", shellQuote(gitcli.SafeText(item.Branch)), item.OID)
}

// restoreAnywhere is restoreCommand with the repository named, so it works
// from any directory once the dashboard has closed. When SafeText must escape
// the repository path or branch name (invisible or control characters, such
// as the ZWNJ common in Persian names), the escaped command would name a path
// that does not exist, so it says so and gives the form to run in place; the
// restore log keeps the exact names.
func restoreAnywhere(item CleanupItem) string {
	repo, branch := gitcli.SafeText(item.Group), gitcli.SafeText(item.Branch)
	command := fmt.Sprintf("git -C %s branch %s %s", shellQuote(repo), shellQuote(branch), item.OID)
	if repo != item.Group || branch != item.Branch {
		command += "  # names shown escaped; inside the repository run: git branch <name> " + item.OID
	}
	return command
}

// revalidateCleanup reassesses an item immediately before it runs, against
// the state the user reviewed. Anything that changed since keeps the item; it
// is never re-planned silently.
func revalidateCleanup(ctx context.Context, git CleanupGit, item CleanupItem) CleanupAssessment {
	changed := func(text string) CleanupAssessment {
		return Assess(CleanupReason{Code: CleanupChangedSinceReview, Text: text})
	}
	failed := func(what string, err error) CleanupAssessment {
		return Assess(CleanupReason{Code: CleanupCheckFailed, Text: what + " failed: " + gitcli.SafeText(err.Error())})
	}
	worktrees, err := git.Worktrees(ctx, item.Group)
	if err != nil {
		return failed("worktree list", err)
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
			return changed("stale worktrees changed since review")
		}
		return assessPrune(ctx, git, item.Group, worktrees, stale)
	case RemoveWorktree:
		for _, wt := range worktrees {
			if wt.Path != item.Path {
				continue
			}
			if !wt.Main && isStale(wt) {
				return changed("worktree directory went missing since review")
			}
			return assessWorktree(ctx, git, wt, cleanupBase{ref: item.Base, name: item.BaseName}, &item)
		}
		return changed("worktree no longer exists")
	case DeleteBranch:
		// update-ref does not look at worktrees, so check them here.
		for _, wt := range worktrees {
			if wt.Branch == item.Branch && holdsBranch(wt) {
				return Assess(CleanupReason{Code: CleanupCheckedOutElsewhere, Text: "checked out in " + gitcli.SafeText(filepath.Base(wt.Path))})
			}
		}
		if busy := operationBlocker(ctx, git, worktrees); busy.Code != "" {
			return Assess(busy)
		}
		branches, err := git.LocalBranches(ctx, item.Group)
		if err != nil {
			return failed("branch list", err)
		}
		for _, b := range branches {
			if b.Name != item.Branch {
				continue
			}
			if b.Symref != "" {
				return changed("branch became a symbolic ref since review")
			}
			if b.OID != item.OID {
				return changed("branch moved since review")
			}
			merged, err := git.IsAncestor(ctx, item.Group, b.OID, item.Base)
			if err != nil {
				return failed("merge check", err)
			}
			if !merged {
				return changed("no longer merged into " + item.BaseName)
			}
			return Assess(CleanupReason{Code: CleanupMerged, Text: "merged into " + item.BaseName})
		}
		return changed("branch no longer exists")
	}
	return Assess(CleanupReason{Code: CleanupCheckFailed, Text: fmt.Sprintf("unsupported cleanup item %q", item.Kind)})
}
