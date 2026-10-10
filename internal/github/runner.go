// Package github runs the GitHub CLI (gh) for read-only pull request lookups
// and for opening pages in the browser. It is the only package that starts gh.
package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	gitcli "github.com/mark-lvl/gitperch/internal/git"
)

// DefaultOutputLimit caps each of gh's output streams.
const DefaultOutputLimit = 4 << 20

var (
	// ErrNotInstalled reports that no gh executable was found.
	ErrNotInstalled = errors.New("gh is not installed")
	// ErrNotAuthenticated reports gh's exit status 4, "authentication required".
	ErrNotAuthenticated = errors.New("gh is not logged in")
	// ErrOutputLimit reports output beyond the capture limit.
	ErrOutputLimit = errors.New("gh output exceeded capture limit")
)

// Runner starts gh with argument arrays, a deadline, closed input, bounded
// output, a neutral working directory and a hardened environment.
type Runner struct {
	Executable  string        // "" finds gh on PATH
	Timeout     time.Duration // 0 means 15 seconds
	OutputLimit int           // 0 means DefaultOutputLimit
}

type Output struct{ Stdout, Stderr []byte }

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil // Drain pipes even after reaching the capture limit.
}

func (r Runner) executable() (string, error) {
	name := r.Executable
	if name == "" {
		name = "gh"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ErrNotInstalled
	}
	return path, nil
}

// Available reports whether gh can be started.
func (r Runner) Available() bool {
	_, err := r.executable()
	return err == nil
}

// Run starts gh outside any repository, so gh never reads a scanned
// repository's Git configuration; callers name the repository explicitly.
func (r Runner) Run(ctx context.Context, args ...string) (Output, error) {
	if len(args) == 0 {
		return Output{}, errors.New("gh command is required")
	}
	bin, err := r.executable()
	if err != nil {
		return Output{}, err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = environment(os.Environ())
	cmd.WaitDelay = time.Second
	limit := r.OutputLimit
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	stdout, stderr := &limitedBuffer{limit: limit}, &limitedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	out := Output{Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return out, ErrOutputLimit
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 4 {
		return out, ErrNotAuthenticated
	}
	if err != nil {
		return out, fmt.Errorf("gh %s: %w%s", args[0], err, firstLine(out.Stderr))
	}
	return out, nil
}

// firstLine is ": " and the first non-empty line of stderr, made safe for
// display, or "" when stderr is empty.
func firstLine(stderr []byte) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return ": " + gitcli.SafeText(line)
		}
	}
	return ""
}

// blockedEnvironment changes what gh prints, where it pages, which repository
// it targets or whether it prompts; environment sets its own values instead.
var blockedEnvironment = map[string]bool{
	"GH_FORCE_TTY": true, "CLICOLOR_FORCE": true, "GH_DEBUG": true, "DEBUG": true,
	"GH_REPO": true, "GH_HOST": true, "GH_PAGER": true, "PAGER": true,
	"GH_PROMPT_DISABLED": true, "GH_NO_UPDATE_NOTIFIER": true, "GH_NO_EXTENSION_UPDATE_NOTIFIER": true,
	"GH_SPINNER_DISABLED": true, "NO_COLOR": true, "CLICOLOR": true,
}

// environment keeps gh's credentials, configuration directory, proxies and
// browser while removing Git routing and anything that would make output
// interactive, colored or debug-annotated.
func environment(env []string) []string {
	env = gitcli.ChildEnvironment(env)
	result := make([]string, 0, len(env)+6)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !blockedEnvironment[key] {
			result = append(result, item)
		}
	}
	return append(result, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1", "GH_SPINNER_DISABLED=1", "NO_COLOR=1", "CLICOLOR=0")
}
