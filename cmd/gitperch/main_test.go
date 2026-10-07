package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mark-lvl/gitperch/internal/app"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestInvocation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		text string
	}{
		{[]string{"--help"}, 0, "Usage:"},
		{[]string{"--version"}, 0, "gitperch "},
		{[]string{"--unknown"}, 2, "flag provided but not defined"},
	} {
		var out, errOut bytes.Buffer
		if got := run(tc.args, &out, &errOut); got != tc.code {
			t.Fatalf("%v: exit %d, want %d", tc.args, got, tc.code)
		}
		if !strings.Contains(out.String()+errOut.String(), tc.text) {
			t.Fatalf("%v: missing %q", tc.args, tc.text)
		}
	}
}

func TestDisplayVersion(t *testing.T) {
	module := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}
	for _, tc := range []struct {
		stamped string
		info    *debug.BuildInfo
		want    string
	}{
		{"0.2.0", module("v0.1.0"), "0.2.0"},
		{"dev", module("v0.1.0"), "0.1.0"},
		{"dev", module("(devel)"), "dev"},
		{"dev", module(""), "dev"},
		{"dev", nil, "dev"},
	} {
		if got := displayVersion(tc.stamped, tc.info); got != tc.want {
			t.Fatalf("displayVersion(%q, %v) = %q, want %q", tc.stamped, tc.info, got, tc.want)
		}
	}
}

func TestWorkspaceCLIAndExitCodes(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", d)
	configFile := filepath.Join(d, "config.toml")
	if err := os.WriteFile(configFile, []byte("default_workspace = 'demo'\n[[workspace]]\nname = 'demo'\npaths = ['repos']\nmax_depth = 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	roots := filepath.Join(d, "repos")
	if err := os.Mkdir(roots, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(d, "absent"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for i := 0; i < 24; i++ {
		p := filepath.Join(roots, fmt.Sprintf("repo-%02d", i))
		if out, err := exec.Command("git", "init", "-q", "-b", "main", p).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	var out, errOut bytes.Buffer
	start := time.Now()
	if code := run([]string{"--config", configFile, "status", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, &errOut)
	}
	var report app.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Repositories) != 24 {
		t.Fatalf("got %d rows", len(report.Repositories))
	}
	t.Logf("24-repository discover+inspect+JSON: %s", time.Since(start))
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"status", "--config", configFile, "--workspace", "unknown"}, 2},
		{[]string{"status", "--config", filepath.Join(d, "missing")}, 2},
		{[]string{"status", "--max-depth", "-1"}, 2},
		{[]string{"status", "--json", filepath.Join(d, "missing"), filepath.Join(roots, "repo-00")}, 1},
		{[]string{"status", "--json", "--config", configFile, filepath.Join(roots, "repo-00")}, 0},
		{[]string{"status", filepath.Join(roots, "repo-00"), "--json"}, 0},
		{[]string{"--json", "status", filepath.Join(roots, "repo-00")}, 0},
		{[]string{"status", "--json", "--", filepath.Join(roots, "repo-00")}, 0},
		{[]string{"status", "--json", "--", "--json"}, 1},
	} {
		out.Reset()
		errOut.Reset()
		if got := run(tc.args, &out, &errOut); got != tc.code {
			t.Fatalf("%v: got %d want %d: %s", tc.args, got, tc.code, &errOut)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := runContext(ctx, []string{"status", roots}, &out, &errOut); code != 130 {
		t.Fatalf("cancel exit=%d", code)
	}
}

func TestDefaultRequiresTerminalAndJSONRequiresStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "gitperch status") {
		t.Fatalf("default: exit %d, %s", code, &errOut)
	}
	errOut.Reset()
	if code := run([]string{"--json"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "requires status") {
		t.Fatalf("json: exit %d, %s", code, &errOut)
	}
}

func TestSymlinkedWorkingDirectoryIsScanned(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", d)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(d, "absent"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	real := filepath.Join(d, "real")
	if out, err := exec.Command("git", "init", "-q", "-b", "main", filepath.Join(real, "repo")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	link := filepath.Join(d, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)
	t.Setenv("PWD", link)
	var out, errOut bytes.Buffer
	if code := run([]string{"status", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, &errOut)
	}
	var report app.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Repositories) != 1 {
		t.Fatalf("got %d rows", len(report.Repositories))
	}
}

func TestStatusAfterDoubleDashIsARoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	// "--" ends options, so "status" names a directory for the dashboard,
	// which needs a terminal here.
	if code := run([]string{"--", "status"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "requires a terminal") {
		t.Fatalf("-- status: exit %d, stdout %q, stderr %q", code, &out, &errOut)
	}
	// Options may still precede the subcommand.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"--max-depth", "0", "status", t.TempDir()}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "REPOSITORY") {
		t.Fatalf("options before status: exit %d, stdout %q, stderr %q", code, &out, &errOut)
	}
}

func TestPrintRestoreCommands(t *testing.T) {
	var out strings.Builder
	printRestoreCommands(&out, nil, true, "/state/gitperch/cleanup.log", nil)
	if out.Len() != 0 {
		t.Fatalf("nothing deleted printed %q", out.String())
	}
	cmds := []string{"git -C /r branch one 1111111111111111111111111111111111111111"}
	printRestoreCommands(&out, cmds, true, "/state/gitperch/cleanup.log", nil)
	if text := out.String(); !strings.Contains(text, "Deleted branches can be restored with:\n  "+cmds[0]+"\n") || !strings.Contains(text, "also recorded in /state/gitperch/cleanup.log") {
		t.Fatalf("printed %q", text)
	}
	out.Reset()
	printRestoreCommands(&out, cmds, false, "/state/gitperch/cleanup.log", nil)
	if text := out.String(); !strings.Contains(text, "Not every command was recorded in /state/gitperch/cleanup.log") {
		t.Fatalf("printed %q", text)
	}
	out.Reset()
	printRestoreCommands(&out, cmds, false, "", errors.New("no home directory"))
	if text := out.String(); !strings.Contains(text, cmds[0]) || !strings.Contains(text, "not recorded: no home directory") {
		t.Fatalf("printed %q", text)
	}
}
