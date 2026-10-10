package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/mark-lvl/gitperch/internal/app"
	"github.com/mark-lvl/gitperch/internal/config"
	"github.com/mark-lvl/gitperch/internal/discovery"
	gitcli "github.com/mark-lvl/gitperch/internal/git"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// version is stamped into release builds with -ldflags "-X main.version=X.Y.Z".
var version = "dev"

// displayVersion falls back to the module version Go records for
// `go install github.com/mark-lvl/gitperch/cmd/gitperch@vX.Y.Z`.
func displayVersion(stamped string, info *debug.BuildInfo) string {
	if stamped != "dev" || info == nil {
		return stamped
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	return stamped
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return runContext(ctx, args, out, errOut)
}

func runContext(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("gitperch", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print version")
	asJSON := fs.Bool("json", false, "status: emit machine-readable JSON")
	configPath := fs.String("config", "", "explicit TOML configuration path")
	workspace := fs.String("workspace", "", "configured workspace name")
	depth := fs.Int("max-depth", 4, "override maximum descendant depth (root is zero)")
	noColor := fs.Bool("no-color", false, "disable dashboard color")
	withGitHub := fs.Bool("github", false, "status: add pull request state from GitHub through gh")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: gitperch [OPTIONS] [ROOT ...]\n       gitperch status [OPTIONS] [ROOT ...]\nTerminal dashboard for local Git repositories.")
		fs.PrintDefaults()
	}
	statusMode := false
	if len(args) > 0 && args[0] == "status" {
		statusMode = true
		args = args[1:]
	}
	// The flag package stops at the first positional argument; keep parsing so
	// options may follow roots. A literal "--" still ends option parsing.
	var roots []string
	firstRootLiteral := false // the first root followed "--", so it is no subcommand
	for {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			firstRootLiteral = len(roots) == 0
			roots = append(roots, rest...)
			break
		}
		roots = append(roots, rest[0])
		args = rest[1:]
	}
	if !statusMode && !firstRootLiteral && len(roots) > 0 && roots[0] == "status" {
		statusMode = true
		roots = roots[1:]
	}
	if *showVersion {
		info, _ := debug.ReadBuildInfo()
		fmt.Fprintln(out, "gitperch", displayVersion(version, info))
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
	ws, err := cfg.Resolve(*workspace, roots, cwd)
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
		if *withGitHub {
			fmt.Fprintln(errOut, "--github requires status")
			return 2
		}
		return runTUI(ctx, cfg, ws, *noColor, out, errOut)
	}
	read := gitcli.Runner{Timeout: time.Duration(cfg.StatusTimeoutSeconds) * time.Second}
	var gh *app.GitHub
	if *withGitHub {
		if gh, _, err = newGitHub(ctx, cfg, read); err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	snapshot, err := app.Load(ctx, discovery.Options{Roots: ws.Paths, MaxDepth: ws.MaxDepth, IgnoreDirs: ws.IgnoreDirs}, read, cfg.StatusWorkers)
	if err != nil {
		fmt.Fprintln(errOut, gitcli.SafeText(err.Error()))
		if ctx.Err() != nil {
			return 130
		}
		return 2
	}
	rows := snapshot.Rows
	if gh != nil {
		gh.LookupAll(ctx, rows)
		gh.Annotate(rows)
		failed := 0
		for _, row := range rows {
			if row.GitHub != nil && row.GitHub.Error != "" {
				failed++
			}
		}
		if failed > 0 {
			// Supplementary data: failed lookups never change the exit code.
			fmt.Fprintf(errOut, "github: %d pull request lookup(s) failed; see each repository's GitHub error\n", failed)
		}
	}
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
