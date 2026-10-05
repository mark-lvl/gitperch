package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"repodash/internal/app"
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
		{[]string{"--version"}, 0, "repodash dev"},
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
	if code := run(nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "repodash status") {
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
