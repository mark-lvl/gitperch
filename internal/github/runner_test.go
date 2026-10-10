package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeGH writes a gh executable that records its arguments (NUL-separated),
// working directory and environment in dir, prints stdout and exits with
// code. Tests never run the real gh: CI runners have one installed.
func fakeGH(t *testing.T, stdout string, code int) (executable, dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stdout"), []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	executable = filepath.Join(dir, "gh")
	script := "#!/bin/sh\n" +
		"printf '%s\\0' \"$@\" > '" + dir + "/args'\n" +
		"pwd -P > '" + dir + "/pwd'\n" +
		"env > '" + dir + "/env'\n" +
		"cat '" + dir + "/stdout'\n" +
		"echo 'first problem' >&2\n" +
		"exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable, dir
}

// recorded reads what fakeGH saved under name; args are split at NUL.
func recorded(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func recordedArgs(t *testing.T, dir string) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(recorded(t, dir, "args"), "\x00"), "\x00")
}

func TestRunUsesNeutralDirectoryAndHardenedEnvironment(t *testing.T) {
	gh, dir := fakeGH(t, "ok\n", 0)
	t.Setenv("GH_FORCE_TTY", "1")
	t.Setenv("GH_REPO", "someone/else")
	t.Setenv("GH_DEBUG", "api")
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	t.Setenv("GH_HOST", "ghe.example.com")
	t.Setenv("GH_PAGER", "less")
	t.Setenv("PAGER", "less")
	t.Setenv("DEBUG", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("GH_TOKEN", "kept-token")
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	t.Setenv("BROWSER", "w3m")
	t.Setenv("GH_BROWSER", "firefox")
	t.Chdir(t.TempDir())
	out, err := (Runner{Executable: gh}).Run(context.Background(), "api", "graphql")
	if err != nil || string(out.Stdout) != "ok\n" {
		t.Fatalf("out %q, %v", out.Stdout, err)
	}
	if got := recordedArgs(t, dir); strings.Join(got, " ") != "api graphql" {
		t.Fatalf("args %q", got)
	}
	want, _ := filepath.EvalSymlinks(os.TempDir())
	if got := strings.TrimSpace(recorded(t, dir, "pwd")); got != want {
		t.Fatalf("gh ran in %q, want %q", got, want)
	}
	env := "\n" + recorded(t, dir, "env")
	for _, absent := range []string{"GH_FORCE_TTY=", "GH_REPO=", "GH_DEBUG=", "GIT_DIR=", "GH_HOST=", "GH_PAGER=", "PAGER=", "DEBUG=", "CLICOLOR_FORCE="} {
		if strings.Contains(env, "\n"+absent) {
			t.Errorf("environment kept %s", absent)
		}
	}
	for _, present := range []string{"GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "CLICOLOR=0", "GH_TOKEN=kept-token",
		"HTTPS_PROXY=http://proxy.example:3128", "BROWSER=w3m", "GH_BROWSER=firefox"} {
		if !strings.Contains(env, "\n"+present+"\n") {
			t.Errorf("environment lacks %s", present)
		}
	}
}

func TestRunClassifiesFailures(t *testing.T) {
	gh, _ := fakeGH(t, "", 4)
	if _, err := (Runner{Executable: gh}).Run(context.Background(), "api"); !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("exit 4: %v", err)
	}
	gh, _ = fakeGH(t, "", 1)
	if _, err := (Runner{Executable: gh}).Run(context.Background(), "api"); err == nil || !strings.Contains(err.Error(), "gh api: exit status 1: first problem") {
		t.Fatalf("exit 1: %v", err)
	}
	gh, _ = fakeGH(t, strings.Repeat("x", 64), 0)
	if _, err := (Runner{Executable: gh, OutputLimit: 16}).Run(context.Background(), "api"); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("limit: %v", err)
	}
	slow := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nexec sleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := (Runner{Executable: slow, Timeout: 50 * time.Millisecond}).Run(context.Background(), "api"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	missing := Runner{Executable: filepath.Join(t.TempDir(), "missing-gh")}
	if _, err := missing.Run(context.Background(), "api"); !errors.Is(err, ErrNotInstalled) || missing.Available() {
		t.Fatalf("missing gh: %v", err)
	}
	if !(Runner{Executable: gh}).Available() {
		t.Fatal("fake gh not available")
	}
}
