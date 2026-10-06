package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RepoDetails is an on-demand read model, separate from inexpensive workspace
// inspection. Agents can later supply their own metadata through a similar seam.
type ChangedFile struct {
	Code, Path     string
	Added, Deleted int
	Binary         bool
}
type Commit struct {
	OID, Subject string
	Time         time.Time
}
type RepoDetails struct {
	Files   []ChangedFile
	Commits []Commit
}

func (r Runner) Details(ctx context.Context, path string) (RepoDetails, error) {
	var d RepoDetails
	out, err := r.Run(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return d, err
	}
	parts := strings.Split(string(out.Stdout), "\x00")
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		code, name := entry[:2], entry[3:]
		if strings.ContainsAny(code, "RC") {
			i++
		} // -z puts the destination first.
		d.Files = append(d.Files, ChangedFile{Code: code, Path: name})
	}
	var passes []string
	for _, args := range [][]string{{"diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z"}, {"diff", "--cached", "--no-ext-diff", "--no-textconv", "--numstat", "-z"}} {
		out, err = r.Run(ctx, path, args...)
		if err != nil {
			return d, err
		}
		passes = append(passes, string(out.Stdout))
	}
	if err := addLineStats(d.Files, passes...); err != nil {
		return d, err
	}
	// An unborn branch legitimately has no history.
	_, headErr := r.Run(ctx, path, "rev-parse", "--verify", "--quiet", "HEAD")
	var exitErr *exec.ExitError
	if errors.As(headErr, &exitErr) && exitErr.ExitCode() == 1 {
		return d, nil
	}
	if headErr != nil {
		return d, headErr
	}
	out, err = r.Run(ctx, path, "log", "-30", "--format=%h%x00%ct%x00%s%x00")
	if err != nil {
		return d, err
	}
	fields := strings.Split(string(out.Stdout), "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		c := Commit{OID: strings.TrimSpace(fields[i]), Subject: fields[i+2]}
		if seconds, err := strconv.ParseInt(strings.TrimSpace(fields[i+1]), 10, 64); err == nil {
			c.Time = time.Unix(seconds, 0)
		}
		d.Commits = append(d.Commits, c)
	}
	return d, nil
}

// addLineStats adds each `git diff --numstat -z` pass to the files it names.
// A rename's empty path field is followed by its source and destination.
func addLineStats(files []ChangedFile, passes ...string) error {
	// Status lists each path once; an index keeps large change sets linear.
	index := make(map[string]int, len(files))
	for i, file := range files {
		index[file.Path] = i
	}
	for _, pass := range passes {
		stats := strings.Split(pass, "\x00")
		for i := 0; i < len(stats); i++ {
			fields := strings.SplitN(stats[i], "\t", 3)
			if len(fields) != 3 {
				continue
			}
			name := fields[2]
			if name == "" && i+2 < len(stats) {
				name = stats[i+2]
				i += 2
			}
			j, ok := index[name]
			if !ok {
				continue
			}
			if fields[0] == "-" {
				files[j].Binary = true
				continue
			}
			a, e1 := strconv.Atoi(fields[0])
			b, e2 := strconv.Atoi(fields[1])
			if e1 != nil || e2 != nil {
				return fmt.Errorf("invalid Git line statistics")
			}
			files[j].Added += a
			files[j].Deleted += b
		}
	}
	return nil
}

// Patch is separate from the preview read model so browsing repositories never
// loads and caches multi-megabyte patches. Output and deadlines use the runner.
func (r Runner) Patch(ctx context.Context, path string) (string, error) {
	var patch strings.Builder
	for _, args := range [][]string{{"diff", "--color=never", "--no-ext-diff", "--no-textconv"}, {"diff", "--cached", "--color=never", "--no-ext-diff", "--no-textconv"}} {
		out, err := r.Run(ctx, path, args...)
		patch.Write(out.Stdout)
		if err != nil {
			return patch.String(), err
		}
	}
	return patch.String(), nil
}
