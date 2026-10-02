package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"repodash/internal/discovery"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "status" {
		return status(args[1:], out, errOut)
	}
	fs := flag.NewFlagSet("repodash", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print version")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: repodash [--help] [--version]\n       repodash status [--max-depth N] [ROOT ...]\nTerminal dashboard for local Git repositories (under development).")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(out, "repodash", version)
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errOut, "unexpected argument:", fs.Arg(0))
		return 2
	}
	fs.Usage()
	return 0
}

func status(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(errOut)
	depth := fs.Int("max-depth", 4, "maximum descendant depth (root is depth zero)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	roots := fs.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	result, err := discovery.Scan(ctx, discovery.Options{Roots: roots, MaxDepth: *depth, IgnoreDirs: []string{"node_modules", "vendor", "target", ".cache", ".next", "dist", "build"}})
	if err != nil {
		fmt.Fprintln(errOut, err)
		if ctx.Err() != nil {
			return 130
		}
		return 2
	}
	fmt.Fprintln(out, "Discovery only — Git state not inspected yet.")
	for _, repo := range result.Repositories {
		fmt.Fprintf(out, "%q\t%q\n", repo.Name, repo.Path)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(errOut, "warning: %q: %s\n", warning.Path, warning.Message)
	}
	if len(result.Warnings) > 0 {
		return 1
	}
	return 0
}
