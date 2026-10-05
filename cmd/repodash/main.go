package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"repodash/internal/app"
	"repodash/internal/config"
	"repodash/internal/discovery"
	gitcli "repodash/internal/git"
	"time"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return runContext(ctx, args, out, errOut)
}

func runContext(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("repodash", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print version")
	asJSON := fs.Bool("json", false, "status: emit machine-readable JSON")
	configPath := fs.String("config", "", "explicit TOML configuration path")
	workspace := fs.String("workspace", "", "configured workspace name")
	depth := fs.Int("max-depth", 4, "override maximum descendant depth (root is zero)")
	noColor := fs.Bool("no-color", false, "disable dashboard color")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: repodash [OPTIONS] [ROOT ...]\n       repodash status [OPTIONS] [ROOT ...]\nTerminal dashboard for local Git repositories.")
		fs.PrintDefaults()
	}
	statusMode := false
	if len(args) > 0 && args[0] == "status" {
		statusMode = true
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 && fs.Arg(0) == "status" {
		statusMode = true
		rest := append([]string(nil), fs.Args()[1:]...)
		if err := fs.Parse(rest); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
	}
	if *showVersion {
		fmt.Fprintln(out, "repodash", version)
		return 0
	}
	explicit, depthSet := false, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			explicit = true
		}
		if f.Name == "max-depth" {
			depthSet = true
		}
	})
	cfg, err := config.Load(*configPath, explicit)
	if err != nil {
		fmt.Fprintln(errOut, gitcli.SafeText(err.Error()))
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	// Getwd keeps symlink components from $PWD, which discovery rejects as
	// symlink ancestors. The working directory itself was not user-named, so
	// resolve it; explicitly supplied symlink roots are still skipped.
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	ws, err := cfg.Resolve(*workspace, fs.Args(), cwd)
	if err != nil {
		fmt.Fprintln(errOut, gitcli.SafeText(err.Error()))
		return 2
	}
	if depthSet {
		if *depth < 0 {
			fmt.Fprintln(errOut, "max-depth must not be negative")
			return 2
		}
		ws.MaxDepth = *depth
	}
	if !statusMode {
		if *asJSON {
			fmt.Fprintln(errOut, "--json requires status")
			return 2
		}
		return runTUI(ctx, cfg, ws, *noColor, out, errOut)
	}
	snapshot, err := app.Load(ctx, discovery.Options{Roots: ws.Paths, MaxDepth: ws.MaxDepth, IgnoreDirs: ws.IgnoreDirs}, gitcli.Runner{Timeout: time.Duration(cfg.StatusTimeoutSeconds) * time.Second}, cfg.StatusWorkers)
	if err != nil {
		fmt.Fprintln(errOut, gitcli.SafeText(err.Error()))
		if ctx.Err() != nil {
			return 130
		}
		return 2
	}
	rows := snapshot.Rows
	if *asJSON {
		err = app.WriteJSON(out, rows, snapshot.Warnings)
	} else {
		err = app.WriteTable(out, rows)
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, warning := range snapshot.Warnings {
		fmt.Fprintf(errOut, "warning: %s: %s\n", gitcli.SafeText(warning.Path), gitcli.SafeText(warning.Message))
	}
	if len(snapshot.Warnings) > 0 {
		return 1
	}
	for _, row := range rows {
		if row.Status.Error != "" {
			return 1
		}
	}
	return 0
}
