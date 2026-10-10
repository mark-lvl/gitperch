package main

import (
	"context"
	"errors"

	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/config"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/github"
)

// newGitHub builds the pull request service and the gh runner behind it when
// the integration can run: enabled in the configuration, Git 2.36 or newer
// and gh on PATH. The error names the first condition that is not met.
func newGitHub(ctx context.Context, cfg config.Config, read gitcli.Runner) (*app.GitHub, github.Runner, error) {
	runner := github.Runner{Timeout: read.Timeout}
	switch {
	case !cfg.GitHub.Enabled:
		return nil, runner, errors.New("GitHub integration is disabled ([github] enabled = false)")
	case !read.SupportsWorktreeInventory(ctx):
		return nil, runner, errors.New("GitHub integration needs Git 2.36 or newer")
	case !runner.Available():
		return nil, runner, errors.New("GitHub integration needs the gh CLI on PATH (https://cli.github.com)")
	}
	return app.NewGitHub(read, github.Client{Runner: runner}, cfg.GitHub.Hosts), runner, nil
}
