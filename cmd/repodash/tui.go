package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"io"
	"os"
	"repodash/internal/app"
	"repodash/internal/config"
	"repodash/internal/discovery"
	gitcli "repodash/internal/git"
	"repodash/internal/tui"
	"strings"
	"time"
)

func runTUI(ctx context.Context, cfg config.Config, ws config.Workspace, noColor bool, out, errOut io.Writer) int {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(errOut, "Interactive dashboard requires a terminal. Run repodash status [ROOT ...] or repodash status --json [ROOT ...].")
		return 2
	}
	_, envNoColor := os.LookupEnv("NO_COLOR")
	noColor = noColor || envNoColor
	read := gitcli.Runner{Timeout: time.Duration(cfg.StatusTimeoutSeconds) * time.Second}
	actions := app.NewActions(gitcli.Service{Read: read, Write: gitcli.Runner{Timeout: time.Duration(cfg.ActionTimeoutSeconds) * time.Second}}, cfg.ActionWorkers)
	load := func(ctx context.Context) (app.Snapshot, error) {
		snapshot, err := app.Load(ctx, discovery.Options{Roots: ws.Paths, MaxDepth: ws.MaxDepth, IgnoreDirs: ws.IgnoreDirs}, read, cfg.StatusWorkers)
		for i := range snapshot.Rows {
			snapshot.Rows[i].LastFetch = actions.LastFetch(snapshot.Rows[i].Path)
		}
		return snapshot, err
	}
	model := tui.New(ctx, load, noColor)
	model.EnableActions(actions)
	model.EnableDetails(read.Details)
	model.Configure(strings.Join(ws.Paths, ", "), cfg.UI.Icons, cfg.UI.DefaultFocus)
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(out)}
	if noColor {
		opts = append(opts, tea.WithColorProfile(colorprofile.NoTTY))
	}
	_, err := tea.NewProgram(model, opts...).Run()
	if err != nil {
		fmt.Fprintln(errOut, gitcli.SafeText(err.Error()))
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return model.ExitCode()
}
