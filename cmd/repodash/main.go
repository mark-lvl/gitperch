package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("repodash", flag.ContinueOnError)
	fs.SetOutput(errOut)
	showVersion := fs.Bool("version", false, "print version")
	fs.Usage = func() {
		fmt.Fprintln(errOut, "Usage: repodash [--help] [--version]\nTerminal dashboard for local Git repositories (under development).")
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
