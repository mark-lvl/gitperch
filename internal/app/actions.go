package app

import (
	"context"
	"errors"
	"fmt"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/repository"
	"sort"
	"sync"
	"time"
)

type Action string

const (
	Fetch   Action = "fetch"
	Push    Action = "push"
	Pull    Action = "fast-forward pull"
	Cleanup Action = "clean up"
)

type State string

const (
	Queued         State = "queued"
	Running        State = "running"
	Succeeded      State = "succeeded"
	Skipped        State = "skipped"
	Failed         State = "failed"
	Cancelled      State = "cancelled"
	OutcomeUnknown State = "outcome unknown"
)

type ActionGit interface {
	Metadata(context.Context, string) (gitcli.Metadata, error)
	ResolveFetch(context.Context, string, gitcli.Metadata) (gitcli.FetchTarget, error)
	Fetch(context.Context, string, gitcli.FetchTarget) error
	Inspect(context.Context, string) repository.Status
}

type Target struct {
	Path            string
	Action          Action
	Remote          string
	URL             string // Display-safe; executable targets are stored privately.
	Branch          string
	Scope           string
	Commit          string
	Eligible        bool
	Reason          string
	DirtyExcluded   bool
	PreflightFailed bool
}
type Preview struct {
	ID      uint64
	Targets []Target
}
type Event struct {
	Path    string
	Item    string // cleanup item ID; empty for fetch, push and pull
	State   State
	Message string
	Status  repository.Status
}
type plannedTarget struct {
	display  Target
	metadata gitcli.Metadata
	fetch    gitcli.FetchTarget
	upstream gitcli.UpstreamTarget
	push     gitcli.PushTarget
	commit   string
}

// Actions owns one pending preview or batch at a time. Returned previews are
// detached copies; callers cannot change executable targets after review.
type Actions struct {
	service        ActionGit
	workers        int
	mu             sync.Mutex
	active         bool
	nextID         uint64
	pendingID      uint64
	pending        []plannedTarget
	pendingCleanup []plannedCleanup
	locks          map[string]chan struct{}
	lastFetch      map[string]time.Time
}

func NewActions(service ActionGit, workers int) *Actions {
	return &Actions{service: service, workers: max(1, min(workers, 64)), locks: map[string]chan struct{}{}, lastFetch: map[string]time.Time{}}
}

func (a *Actions) Plan(ctx context.Context, action Action, paths []string) (Preview, error) {
	if action != Fetch && action != Push && action != Pull {
		return Preview{}, fmt.Errorf("unsupported action %q", action)
	}
	if len(paths) == 0 {
		return Preview{}, errors.New("select repositories explicitly before a batch action")
	}
	a.mu.Lock()
	if a.active {
		a.mu.Unlock()
		return Preview{}, errors.New("another preview or batch is active")
	}
	a.active = true
	a.nextID++
	id := a.nextID
	a.mu.Unlock()
	succeeded := false
	defer func() {
		if !succeeded {
			a.mu.Lock()
			a.active = false
			a.mu.Unlock()
		}
	}()
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	var planned []plannedTarget
	seen := map[string]bool{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return Preview{}, err
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		p := a.planOne(ctx, action, path, nil)
		planned = append(planned, p)
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	a.mu.Lock()
	a.pendingID = id
	a.pending = planned
	a.mu.Unlock()
	succeeded = true
	preview := Preview{ID: id, Targets: make([]Target, len(planned))}
	for i, p := range planned {
		preview.Targets[i] = p.display
	}
	return preview, nil
}

func (a *Actions) Discard(id uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id != 0 && a.pendingID == id && (len(a.pending) > 0 || a.pendingCleanup != nil) {
		a.pending, a.pendingCleanup = nil, nil
		a.pendingID = 0
		a.active = false
	}
}

func (a *Actions) lockFor(common string) chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ch := a.locks[common]; ch != nil {
		return ch
	}
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	a.locks[common] = ch
	return ch
}

func (a *Actions) markFetched(path string) {
	a.mu.Lock()
	a.lastFetch[path] = time.Now().UTC()
	a.mu.Unlock()
}
func (a *Actions) LastFetch(path string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastFetch[path]
}

// Execute confirms only the private plan referenced by ID. emit must be safe
// for concurrent callers and should consume events promptly.
func (a *Actions) Execute(ctx context.Context, id uint64, emit func(Event)) ([]Event, error) {
	a.mu.Lock()
	if !a.active || a.pendingID != id || len(a.pending) == 0 {
		a.mu.Unlock()
		return nil, errors.New("preview is no longer active")
	}
	items := a.pending
	a.pending = nil
	a.pendingID = 0
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.active = false; a.mu.Unlock() }()
	results := make([]Event, len(items))
	jobs := make(chan int)
	if emit == nil {
		emit = func(Event) {}
	}
	for _, p := range items {
		emit(Event{Path: p.display.Path, State: Queued})
	}
	var wg sync.WaitGroup
	for range min(a.workers, len(items)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = a.executeOne(ctx, items[i], emit)
				emit(results[i])
			}
		}()
	}
	for i := range items {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results, ctx.Err()
}

func (a *Actions) executeOne(ctx context.Context, p plannedTarget, emit func(Event)) Event {
	result := Event{Path: p.display.Path}
	if !p.display.Eligible {
		result.State = Skipped
		if p.display.PreflightFailed {
			result.State = Failed
		}
		result.Message = p.display.Reason
		return result
	}
	lock := a.lockFor(p.metadata.CommonDir)
	select {
	case <-ctx.Done():
		result.State = Cancelled
		result.Message = ctx.Err().Error()
		return result
	case <-lock:
	}
	defer func() { lock <- struct{}{} }()
	if ctx.Err() != nil {
		result.State = Cancelled
		result.Message = ctx.Err().Error()
		return result
	}
	current, err := a.service.Metadata(ctx, p.display.Path)
	if err == nil && (current.CommonDir != p.metadata.CommonDir || current.ConfigHash != p.metadata.ConfigHash || current.Status.HeadOID != p.metadata.Status.HeadOID || current.Status.Branch != p.metadata.Status.Branch || current.Status.Detached != p.metadata.Status.Detached || current.Status.Unborn != p.metadata.Status.Unborn) {
		err = errors.New("repository or target configuration changed since preview")
	}
	if err == nil {
		target, e := a.service.ResolveFetch(ctx, p.display.Path, current)
		err = e
		if err == nil && target != p.fetch {
			err = errors.New("fetch target changed since preview")
		}
	}
	if err == nil && p.display.Action != Fetch {
		err = a.revalidateSync(ctx, p, current)
	}
	if err != nil {
		result.State = Skipped
		if ctx.Err() != nil {
			result.State = Cancelled
		}
		result.Message = gitcli.SafeText(err.Error())
		return result
	}
	emit(Event{Path: p.display.Path, State: Running, Message: string(p.display.Action) + " " + p.display.Remote})
	switch p.display.Action {
	case Fetch:
		err = a.service.Fetch(ctx, p.display.Path, p.fetch)
	case Push:
		err = a.service.(SyncGit).Push(ctx, p.display.Path, p.push)
	case Pull:
		err = a.service.(SyncGit).FastForward(ctx, p.display.Path, p.commit)
	}
	if err != nil {
		result.State = Failed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.State = Cancelled
			if p.display.Action == Push || p.display.Action == Pull {
				result.State = OutcomeUnknown
			}
		}
		if errors.Is(err, gitcli.ErrOutputLimit) && p.display.Action != Fetch {
			result.State = OutcomeUnknown
		}
		result.Message = gitcli.SafeText(err.Error())
		return result
	}
	if p.display.Action == Fetch {
		a.markFetched(p.display.Path)
	}
	result.State = Succeeded
	result.Message = string(p.display.Action) + " completed for " + p.display.Remote
	result.Status = a.service.Inspect(ctx, p.display.Path)
	return result
}
