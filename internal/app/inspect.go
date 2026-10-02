// Package app coordinates discovery, inspection, and later Git actions.
package app

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"repodash/internal/repository"
)

const maxInspectionWorkers = 64

type GitService interface {
	Inspect(context.Context, string) repository.Status
}

type Row struct {
	repository.Repository
	Status repository.Status `json:"status"`
}

// Inspect inspects repositories with a bounded worker pool. workers must be
// positive; values above 64 are capped. Results are stable-sorted by display
// name and path regardless of input ordering. On cancellation, unfinished
// rows carry an explicit error and the returned error is ctx.Err().
func Inspect(ctx context.Context, repos []repository.Repository, service GitService, workers int) ([]Row, error) {
	if ctx == nil {
		return nil, fmt.Errorf("inspection: nil context")
	}
	if workers < 1 {
		return nil, fmt.Errorf("inspection: worker count must be positive")
	}
	if service == nil {
		return nil, fmt.Errorf("inspection: Git service is required")
	}

	ordered := append([]repository.Repository(nil), repos...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Name != ordered[j].Name {
			return ordered[i].Name < ordered[j].Name
		}
		return ordered[i].Path < ordered[j].Path
	})
	rows := make([]Row, len(ordered))
	for i, repo := range ordered {
		rows[i].Repository = repo
	}
	if len(ordered) == 0 {
		return rows, ctx.Err()
	}
	if workers > maxInspectionWorkers {
		workers = maxInspectionWorkers
	}
	if workers > len(ordered) {
		workers = len(ordered)
	}

	type job struct {
		index int
		repo  repository.Repository
	}
	type answer struct {
		index     int
		status    repository.Status
		cancelled bool
	}
	jobs := make(chan job)
	answers := make(chan answer, len(ordered))
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for item := range jobs {
				status := repository.Status{}
				cancelled := false
				if ctx.Err() != nil {
					status.Error = "inspection cancelled"
					cancelled = true
				} else {
					status = service.Inspect(ctx, item.repo.Path)
					if ctx.Err() != nil {
						status.Error = "inspection cancelled"
						cancelled = true
					}
				}
				answers <- answer{index: item.index, status: status, cancelled: cancelled}
			}
		}()
	}

	dispatched := make([]bool, len(ordered))
	for i, repo := range ordered {
		select {
		case jobs <- job{index: i, repo: repo}:
			dispatched[i] = true
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	group.Wait()
	close(answers)
	completed := make([]bool, len(ordered))
	for answer := range answers {
		rows[answer.index].Status = answer.status
		completed[answer.index] = !answer.cancelled
	}
	for i := range rows {
		if !dispatched[i] || !completed[i] {
			rows[i].Status.Error = "inspection cancelled"
		}
	}
	return rows, ctx.Err()
}
