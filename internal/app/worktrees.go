package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
)

// WorktreeInfo places a row within its repository's worktrees. MainPath is
// the Path of the group's parent row, so the TUI groups rows by it.
type WorktreeInfo struct {
	Main           bool   `json:"main"`
	Linked         bool   `json:"linked"`
	Bare           bool   `json:"bare,omitempty"`
	Locked         bool   `json:"locked,omitempty"`
	LockReason     string `json:"lock_reason,omitempty"`
	Prunable       bool   `json:"prunable,omitempty"`
	PrunableReason string `json:"prunable_reason,omitempty"`
	MainPath       string `json:"main_path"`
	OutsideRoots   bool   `json:"outside_roots,omitempty"`
}

// WorktreeLister is implemented by Git services that can list worktrees.
type WorktreeLister interface {
	SupportsWorktreeInventory(context.Context) bool
	Worktrees(context.Context, string) ([]gitcli.Worktree, error)
}

// identity compares paths the way Git reports them (symlinks resolved) while
// rows keep the path discovery found them under.
func identity(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func missingDir(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

func underRoots(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(identity(root), identity(path))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// attachWorktrees lists worktrees once per common Git directory, adds rows
// for worktrees the scan could not reach and labels every member. A listing
// failure leaves that group's rows unlabelled and adds a warning.
func attachWorktrees(ctx context.Context, rows []Row, service GitService, workers int, roots []string) ([]Row, []discovery.Warning) {
	lister, ok := service.(WorktreeLister)
	if !ok || !lister.SupportsWorktreeInventory(ctx) {
		return rows, nil // older Git: the feature is off, without warnings
	}
	groups := map[string][]int{}
	var order []string
	for i, row := range rows {
		common := row.Status.CommonDir
		if row.Status.Error != "" || common == "" {
			continue
		}
		if _, seen := groups[common]; !seen {
			order = append(order, common)
		}
		groups[common] = append(groups[common], i)
	}
	lists := make([][]gitcli.Worktree, len(order))
	errs := make([]error, len(order))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(1, min(workers, len(order))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lists[i], errs[i] = lister.Worktrees(ctx, rows[groups[order[i]][0]].Path)
			}
		}()
	}
	for i := range order {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var warnings []discovery.Warning
	var extra []repository.Repository
	type pending struct {
		info WorktreeInfo
		wt   gitcli.Worktree
	}
	added := map[string]pending{}
	for g, common := range order {
		if errs[g] == nil && len(lists[g]) == 0 {
			errs[g] = errors.New("no worktrees listed")
		}
		if errs[g] != nil {
			warnings = append(warnings, discovery.Warning{Path: rows[groups[common][0]].Path, Message: "worktree list: " + gitcli.SafeText(errs[g].Error())})
			continue
		}
		byIdentity := map[string]int{}
		for _, i := range groups[common] {
			byIdentity[identity(rows[i].Path)] = i
		}
		mainPath := lists[g][0].Path
		if i, ok := byIdentity[identity(mainPath)]; ok {
			mainPath = rows[i].Path
		}
		for _, wt := range lists[g] {
			// Git never reports a locked worktree as prunable, even when its
			// directory is gone; it is just as unusable as a row.
			wt.Prunable = wt.Prunable || !wt.Bare && missingDir(wt.Path)
			if wt.Prunable && wt.PrunableReason == "" {
				wt.PrunableReason = "directory missing"
			}
			info := WorktreeInfo{Main: wt.Main, Linked: !wt.Main, Bare: wt.Bare, Locked: wt.Locked, LockReason: gitcli.SafeText(wt.LockReason), Prunable: wt.Prunable, PrunableReason: gitcli.SafeText(wt.PrunableReason), MainPath: mainPath}
			if i, ok := byIdentity[identity(wt.Path)]; ok {
				rows[i].Worktree = &info
				continue
			}
			info.OutsideRoots = !underRoots(wt.Path, roots)
			repo := repository.Repository{Path: wt.Path, Name: filepath.Base(wt.Path)}
			if wt.Main {
				repo.Path = mainPath
			}
			added[repo.Path] = pending{info, wt}
			if !wt.Prunable && !wt.Bare {
				extra = append(extra, repo)
			}
		}
	}
	inspected, _ := Inspect(ctx, extra, service, max(1, workers))
	for _, row := range inspected {
		p := added[row.Path]
		row.Worktree = &p.info
		rows = append(rows, row)
		delete(added, row.Path)
	}
	for path, p := range added { // stale records and bare main repositories
		info := p.info
		rows = append(rows, Row{Repository: repository.Repository{Path: path, Name: filepath.Base(path)}, Status: repository.Status{Branch: p.wt.Branch, HeadOID: p.wt.HeadOID, Detached: p.wt.Detached}, Worktree: &info})
	}
	sortRows(rows)
	return rows, warnings
}
