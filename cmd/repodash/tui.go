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
	load := func(ctx context.Context) (app.Snapshot, error) {
		return app.Load(ctx, discovery.Options{Roots: ws.Paths, MaxDepth: ws.MaxDepth, IgnoreDirs: ws.IgnoreDirs}, gitcli.Runner{Timeout: time.Duration(cfg.StatusTimeoutSeconds) * time.Second}, cfg.StatusWorkers)
	}
	model := tui.New(ctx, load, noColor)
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
	return 0
}
