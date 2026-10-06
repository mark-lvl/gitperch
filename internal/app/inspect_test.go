package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark-lvl/gitperch/internal/repository"
)

type inspectFunc func(context.Context, string) repository.Status

func (f inspectFunc) Inspect(ctx context.Context, path string) repository.Status {
	return f(ctx, path)
}

func TestInspectBoundsConcurrencySortsRowsAndKeepsGoingAfterFailure(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32
	service := inspectFunc(func(_ context.Context, path string) repository.Status {
		current := active.Add(1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		time.Sleep(4 * time.Millisecond)
		active.Add(-1)
		if strings.Contains(path, "fail") {
			return repository.Status{Error: "inspection failed"}
		}
		return repository.Status{Branch: path}
	})
	repos := []repository.Repository{
		{Name: "z", Path: "/z"},
		{Name: "same", Path: "/same-b"},
		{Name: "fail", Path: "/fail"},
		{Name: "same", Path: "/same-a"},
		{Name: "a", Path: "/a"},
		{Name: "c", Path: "/c"},
		{Name: "d", Path: "/d"},
	}

	rows, err := Inspect(context.Background(), repos, service, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got > 3 || got < 2 {
		t.Fatalf("peak concurrent inspections = %d, want between 2 and 3", got)
	}
	if len(rows) != len(repos) {
		t.Fatalf("got %d rows, want %d", len(rows), len(repos))
	}
	for i := 1; i < len(rows); i++ {
		prev, current := rows[i-1].Repository, rows[i].Repository
		if prev.Name > current.Name || (prev.Name == current.Name && prev.Path > current.Path) {
			t.Fatalf("rows are not sorted by name then path: %#v", rows)
		}
	}
	failureFound := false
	for _, row := range rows {
		if row.Path == "/fail" && row.Status.Error == "inspection failed" {
			failureFound = true
		}
		if row.Path != "/fail" && row.Status.Error != "" {
			t.Errorf("unexpected error for %s: %q", row.Path, row.Status.Error)
		}
	}
	if !failureFound {
		t.Fatal("failed repository result was lost")
	}
}

func TestInspectCapsWorkerCountAt64(t *testing.T) {
	const count = 70
	started := make(chan struct{}, count)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	service := inspectFunc(func(_ context.Context, _ string) repository.Status {
		current := active.Add(1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return repository.Status{}
	})
	repos := make([]repository.Repository, count)
	for i := range repos {
		repos[i] = repository.Repository{Name: fmt.Sprintf("%02d", i), Path: fmt.Sprintf("/%02d", i)}
	}
	finished := make(chan error, 1)
	go func() {
		_, err := Inspect(context.Background(), repos, service, 200)
		finished <- err
	}()
	for i := 0; i < 64; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatalf("only %d workers started before timeout", i)
		}
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got > 64 {
		t.Fatalf("peak concurrent inspections = %d, want at most 64", got)
	}
}

func TestInspectCancellationMarksUnfinishedRows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 3)
	service := inspectFunc(func(ctx context.Context, path string) repository.Status {
		if path == "/a" {
			return repository.Status{Branch: "finished"}
		}
		started <- path
		<-ctx.Done()
		return repository.Status{Error: ctx.Err().Error()}
	})
	repos := []repository.Repository{
		{Name: "c", Path: "/c"},
		{Name: "a", Path: "/a"},
		{Name: "b", Path: "/b"},
	}
	type outcome struct {
		rows []Row
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		rows, err := Inspect(ctx, repos, service, 1)
		done <- outcome{rows: rows, err: err}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no blocking inspection started")
	}
	cancel()
	select {
	case result := <-done:
		if result.err != context.Canceled {
			t.Fatalf("error = %v, want context.Canceled", result.err)
		}
		if len(result.rows) != len(repos) {
			t.Fatalf("got %d rows, want all %d rows", len(result.rows), len(repos))
		}
		for _, row := range result.rows {
			if row.Path == "/a" {
				if row.Status.Branch != "finished" || row.Status.Error != "" {
					t.Errorf("completed row lost its result: %#v", row)
				}
			} else if row.Status.Error != "inspection cancelled" {
				t.Errorf("unfinished row %s error = %q", row.Path, row.Status.Error)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Inspect did not return after context cancellation")
	}
}

func TestInspectRequiresPositiveWorkerCount(t *testing.T) {
	for _, workers := range []int{0, -1} {
		if _, err := Inspect(context.Background(), nil, inspectFunc(func(context.Context, string) repository.Status { return repository.Status{} }), workers); err == nil {
			t.Errorf("worker count %d unexpectedly accepted", workers)
		}
	}
}

func TestInspectStableOrderingWithEqualNames(t *testing.T) {
	repos := []repository.Repository{{Name: "x", Path: "/z"}, {Name: "x", Path: "/a"}, {Name: "b", Path: "/b"}}
	rows, err := Inspect(context.Background(), repos, inspectFunc(func(context.Context, string) repository.Status { return repository.Status{} }), 2)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(rows))
	for _, row := range rows {
		paths = append(paths, row.Path)
	}
	want := []string{"/b", "/a", "/z"}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Fatalf("ordered paths = %v, want %v", paths, want)
	}
}
