package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/config"
	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"github.com/mark-lvl/gitperch/internal/tui"
	"io"
	"os"
	"strings"
	"time"
)

func runTUI(ctx context.Context, cfg config.Config, ws config.Workspace, noColor bool, out, errOut io.Writer) int {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(errOut, "Interactive dashboard requires a terminal. Run gitperch status [ROOT ...] or gitperch status --json [ROOT ...].")
		return 2
	}
	_, envNoColor := os.LookupEnv("NO_COLOR")
	noColor = noColor || envNoColor
	read := gitcli.Runner{Timeout: time.Duration(cfg.StatusTimeoutSeconds) * time.Second}
	actions := app.NewActions(gitcli.Service{Read: read, Write: gitcli.Runner{Timeout: time.Duration(cfg.ActionTimeoutSeconds) * time.Second}}, cfg.ActionWorkers)
	restoreLog, restoreLogErr := app.DefaultRestoreLogPath()
	actions.SetRestoreLog(restoreLog)
	load := func(ctx context.Context) (app.Snapshot, error) {
		snapshot, err := app.Load(ctx, discovery.Options{Roots: ws.Paths, MaxDepth: ws.MaxDepth, IgnoreDirs: ws.IgnoreDirs}, read, cfg.StatusWorkers)
		for i := range snapshot.Rows {
			snapshot.Rows[i].LastFetch = actions.LastFetch(snapshot.Rows[i].Path)
		}
		return snapshot, err
	}
	model := tui.New(ctx, load, noColor)
	model.EnableActions(actions)
	model.EnableCleanup(actions.CleanupSupported(ctx))
	model.EnableDetails(read.Details)
	model.EnablePatch(read.Patch)
	model.Configure(strings.Join(ws.Paths, ", "), cfg.UI.Icons, cfg.UI.DefaultFocus)
	model.SetAutoRefresh(time.Duration(cfg.UI.RefreshSeconds) * time.Second)
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(out)}
	if noColor {
		opts = append(opts, tea.WithColorProfile(colorprofile.NoTTY))
	}
	_, err := tea.NewProgram(model, opts...).Run()
	commands, logged := model.RestoreCommands()
	printRestoreCommands(errOut, commands, logged, restoreLog, restoreLogErr)
	if err != nil {
		fmt.Fprintln(errOut, gitcli.SafeText(err.Error()))
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return model.ExitCode()
}

// printRestoreCommands repeats, once the dashboard has closed, how to recreate
// each branch deleted this session, and where the commands were recorded.
func printRestoreCommands(w io.Writer, commands []string, logged bool, logPath string, logErr error) {
	if len(commands) == 0 {
		return
	}
	fmt.Fprintln(w, "Deleted branches can be restored with:")
	for _, command := range commands {
		fmt.Fprintln(w, "  "+command)
	}
	switch {
	case logErr != nil:
		fmt.Fprintln(w, "These commands were not recorded: "+gitcli.SafeText(logErr.Error()))
	case !logged:
		fmt.Fprintln(w, "Some of these commands could not be recorded in "+gitcli.SafeText(logPath))
	default:
		fmt.Fprintln(w, "They are also recorded in "+gitcli.SafeText(logPath))
	}
}
