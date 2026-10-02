package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const DefaultOutputLimit = 4 << 20

type Runner struct {
	Executable  string
	Timeout     time.Duration
	OutputLimit int
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

// Run always uses argument arrays, a deadline, closed input, and isolated routing.
func (r Runner) Run(ctx context.Context, path string, args ...string) (Output, error) {
	if len(args) == 0 {
		return Output{}, errors.New("Git command is required")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bin := r.Executable
	if bin == "" {
		bin = "git"
	}
	argv := append([]string{"--no-optional-locks", "-c", "credential.interactive=false", "-C", path}, args...)
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Env = cleanEnvironment(os.Environ())
	cmd.WaitDelay = time.Second
	limit := r.OutputLimit
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	stdout, stderr := &limitedBuffer{limit: limit}, &limitedBuffer{limit: limit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	out := Output{Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return out, errors.New("Git output exceeded capture limit")
	}
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, SafeText(string(out.Stderr)))
	}
	return out, nil
}

func cleanEnvironment(env []string) []string {
	blocked := map[string]bool{"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true, "GIT_COMMON_DIR": true, "GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_NAMESPACE": true, "GIT_CEILING_DIRECTORIES": true, "GIT_CONFIG": true, "GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true, "GIT_TERMINAL_PROMPT": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true, "SSH_ASKPASS_REQUIRE": true, "GIT_SSH_COMMAND": true}
	result := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !blocked[key] && !strings.HasPrefix(key, "GIT_CONFIG_KEY_") && !strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			result = append(result, item)
		}
	}
	// Preserve SSH_AUTH_SOCK and normal credential helpers. BatchMode prevents
	// SSH passwords/passphrases; authenticate with an agent in a normal shell.
	return append(result, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false", "SSH_ASKPASS_REQUIRE=never", "GIT_SSH_COMMAND=ssh -oBatchMode=yes")
}
