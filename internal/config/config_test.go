package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsAndMissingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := Load(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusWorkers != 8 || cfg.ActionWorkers != 2 || cfg.StatusTimeoutSeconds != 15 || cfg.ActionTimeoutSeconds != 120 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.Workspaces) != 0 {
		t.Fatalf("expected no workspaces, got %+v", cfg.Workspaces)
	}
	if _, err := Load(path, true); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("explicit missing config should fail, got %v", err)
	}
}

func TestLoadResolvesPathsAndAppliesOmittedDefaults(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "settings", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `default_workspace = "personal"

[[workspace]]
name = "personal"
paths = ["relative/projects", "~/work"]
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.Workspaces[0]
	if w.MaxDepth != DefaultMaxDepth || len(w.IgnoreDirs) != len(defaultIgnoreDirs) {
		t.Fatalf("omitted workspace defaults not applied: %+v", w)
	}
	want := []string{filepath.Join(filepath.Dir(configPath), "relative", "projects"), filepath.Join(base, "home", "work")}
	for i := range want {
		if w.Paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, w.Paths[i], want[i])
		}
	}
}

func TestLoadWorkerCapAndExplicitValues(t *testing.T) {
	path := writeConfig(t, `status_workers = 1000
action_workers = 3
status_timeout_seconds = 21
action_timeout_seconds = 240
`)
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatusWorkers != MaxWorkers || cfg.ActionWorkers != 3 || cfg.StatusTimeoutSeconds != 21 || cfg.ActionTimeoutSeconds != 240 {
		t.Fatalf("explicit values not applied or workers not capped: %+v", cfg)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name, body, message string
	}{
		{"toml", "status_workers = [", "parse config"},
		{"unknown key", "status_worker = 8", "strict mode"},
		{"zero status workers", "status_workers = 0", "status_workers must be positive"},
		{"negative action workers", "action_workers = -2", "action_workers must be positive"},
		{"zero status timeout", "status_timeout_seconds = 0", "status_timeout_seconds must be positive"},
		{"negative action timeout", "action_timeout_seconds = -1", "action_timeout_seconds must be positive"},
		{"timeout overflow", "status_timeout_seconds = 9223372036854775807", "maximum representable duration"},
		{"negative depth", "[[workspace]]\nname='x'\nmax_depth=-1", "max_depth must not be negative"},
		{"empty workspace", "[[workspace]]\nname='  '", "empty name"},
		{"duplicate workspace", "[[workspace]]\nname='x'\n[[workspace]]\nname='x'", "duplicate workspace"},
		{"unknown default", "default_workspace='missing'", "not configured"},
		{"empty path", "[[workspace]]\nname='x'\npaths=['']", "empty path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body), true)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("Load error = %v, want containing %q", err, tc.message)
			}
		})
	}
}

func TestResolveSelectionOverridesAndCWDallback(t *testing.T) {
	cfg := Config{
		DefaultWorkspace: "personal",
		Workspaces: []Workspace{
			{Name: "personal", Paths: []string{filepath.Join(t.TempDir(), "configured")}, MaxDepth: 9, IgnoreDirs: []string{"skip"}},
			{Name: "work", Paths: []string{filepath.Join(t.TempDir(), "work")}, MaxDepth: 2},
		},
	}
	cwd := filepath.Join(t.TempDir(), "run")
	absoluteRoot := filepath.Join(t.TempDir(), "absolute")
	w, err := cfg.Resolve("", []string{"relative", absoluteRoot}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "personal" || w.MaxDepth != 9 || len(w.Paths) != 2 || w.Paths[0] != filepath.Join(cwd, "relative") || w.Paths[1] != absoluteRoot {
		t.Fatalf("positional roots should override only paths: %+v", w)
	}
	w, err = cfg.Resolve("work", nil, cwd)
	if err != nil || w.Name != "work" || len(w.Paths) != 1 || w.Paths[0] != cfg.Workspaces[1].Paths[0] {
		t.Fatalf("explicit workspace selection = %+v, %v", w, err)
	}
	if _, err := cfg.Resolve("unknown", nil, cwd); err == nil || !strings.Contains(err.Error(), "unknown workspace") {
		t.Fatalf("unknown workspace should fail, got %v", err)
	}
	empty := Config{}
	w, err = empty.Resolve("", nil, cwd)
	if err != nil || len(w.Paths) != 1 || w.Paths[0] != cwd {
		t.Fatalf("empty config should fall back to cwd: %+v, %v", w, err)
	}
	pathless := Config{Workspaces: []Workspace{{Name: "bare", MaxDepth: 1}}}
	if _, err := pathless.Resolve("bare", nil, cwd); err == nil || !strings.Contains(err.Error(), "has no paths") {
		t.Fatalf("pathless workspace should fail, got %v", err)
	}
	if w, err := pathless.Resolve("bare", []string{"r"}, cwd); err != nil || w.MaxDepth != 1 || w.Paths[0] != filepath.Join(cwd, "r") {
		t.Fatalf("pathless workspace with roots = %+v, %v", w, err)
	}
}

func TestResolveExpandsTildeInCLIPaths(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	cwd := filepath.Join(t.TempDir(), "ignored")
	w, err := (Config{}).Resolve("", []string{"~/projects", "~someone/project"}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if w.Paths[0] != filepath.Join(base, "projects") {
		t.Fatalf("tilde path = %q", w.Paths[0])
	}
	if w.Paths[1] != filepath.Join(cwd, "~someone", "project") {
		t.Fatalf("arbitrary shell tilde expansion occurred: %q", w.Paths[1])
	}
}

func TestExplicitConfigTildeAndEmptyCLIRoot(t *testing.T) {
	d := t.TempDir()
	t.Setenv("HOME", d)
	if err := os.WriteFile(filepath.Join(d, "config.toml"), []byte("status_workers=3"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("~/config.toml", true)
	if err != nil || cfg.StatusWorkers != 3 {
		t.Fatalf("config tilde: %+v %v", cfg, err)
	}
	if _, err := cfg.Resolve("", []string{""}, d); err == nil {
		t.Fatal("accepted empty CLI root")
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUISettings(t *testing.T) {
	for _, mode := range []string{"ascii", "unicode", "nerd"} {
		cfg, err := Load(writeConfig(t, "[ui]\nicons = \""+mode+"\"\ndefault_focus = true\n"), true)
		if err != nil || cfg.UI.Icons != mode || !cfg.UI.DefaultFocus {
			t.Fatalf("%s: %+v %v", mode, cfg.UI, err)
		}
	}
	if _, err := Load(writeConfig(t, "[ui]\nicons = \"invalid\"\n"), true); err == nil {
		t.Fatal("invalid icons accepted")
	}
}

func TestUIRefreshSeconds(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[ui]\nicons = \"ascii\"\n"), true)
	if err != nil || cfg.UI.RefreshSeconds != DefaultRefreshSeconds {
		t.Fatalf("default: %+v %v", cfg.UI, err)
	}
	for body, want := range map[string]int{"refresh_seconds = 0\n": 0, "refresh_seconds = 90\n": 90} {
		cfg, err := Load(writeConfig(t, "[ui]\n"+body), true)
		if err != nil || cfg.UI.RefreshSeconds != want {
			t.Fatalf("%q: %+v %v", body, cfg.UI, err)
		}
	}
	for _, body := range []string{"refresh_seconds = -1\n", "refresh_seconds = 2\n", "refresh_seconds = 100000\n"} {
		if _, err := Load(writeConfig(t, "[ui]\n"+body), true); err == nil {
			t.Fatalf("%q accepted", body)
		}
	}
}

func TestLoadGitHubSettings(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"), false)
	if err != nil || !cfg.GitHub.Enabled || len(cfg.GitHub.Hosts) != 0 {
		t.Fatalf("defaults: %+v, %v", cfg.GitHub, err)
	}
	cfg, err = Load(writeConfig(t, "[github]\nenabled = false\nhosts = [\"GHE.Example.com\", \"git.corp.example.\"]\n"), true)
	if err != nil || cfg.GitHub.Enabled || strings.Join(cfg.GitHub.Hosts, ",") != "ghe.example.com,git.corp.example" {
		t.Fatalf("explicit: %+v, %v", cfg.GitHub, err)
	}
	if cfg, err := Load(writeConfig(t, "[github]\nhosts = []\n"), true); err != nil || !cfg.GitHub.Enabled {
		t.Fatalf("enabled stays the default: %+v, %v", cfg.GitHub, err)
	}
	for _, host := range []string{"https://ghe.example.com", "ghe.example.com/team", "ghe.example.com:8443", " ", "", "-ghe.example.com", "ghe..example.com", "ghe example.com"} {
		if _, err := Load(writeConfig(t, "[github]\nhosts = [\""+host+"\"]\n"), true); err == nil || !strings.Contains(err.Error(), "github.hosts entry") {
			t.Errorf("host %q: %v", host, err)
		}
	}
	if _, err := Load(writeConfig(t, "[github]\ntoken = \"x\"\n"), true); err == nil {
		t.Error("unknown github key accepted")
	}
}
