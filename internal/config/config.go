// Package config loads and resolves gitperch's TOML configuration.
package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"bytes"
	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultStatusWorkers        = 8
	DefaultActionWorkers        = 2
	DefaultStatusTimeoutSeconds = 15
	DefaultActionTimeoutSeconds = 120
	MaxWorkers                  = 64
	DefaultMaxDepth             = 4
	DefaultRefreshSeconds       = 30
	MinRefreshSeconds           = 5
)

var defaultIgnoreDirs = []string{"node_modules", "vendor", "target", ".cache", ".next", "dist", "build"}

// Config contains global inspection and action settings and named workspaces.
type Config struct {
	DefaultWorkspace     string
	StatusWorkers        int
	ActionWorkers        int
	StatusTimeoutSeconds int
	ActionTimeoutSeconds int
	UI                   UI
	GitHub               GitHub
	Workspaces           []Workspace
	configDir            string
}

// UI is shared by the terminal presentation only.
type UI struct {
	Icons        string
	DefaultFocus bool
	// RefreshSeconds is the automatic local status refresh interval; 0 disables it.
	RefreshSeconds int
}

// GitHub configures the optional GitHub CLI integration.
type GitHub struct {
	// Enabled allows gh lookups; true unless [github] enabled = false.
	Enabled bool
	// Hosts are GitHub Enterprise Server host names besides github.com,
	// lower-cased.
	Hosts []string
}

// hostName is a bare ASCII DNS name with labels of at most 63 characters: no
// scheme, port, path or whitespace.
var hostName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// Workspace defines discovery settings for a group of roots.
type Workspace struct {
	Name       string
	Paths      []string
	MaxDepth   int
	IgnoreDirs []string
}

// DefaultPath returns the platform's user configuration path for gitperch.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, "gitperch", "config.toml"), nil
}

// Load reads and validates path. An absent implicit default file is equivalent
// to an empty configuration; an explicitly requested file must exist.
func Load(path string, explicit bool) (Config, error) {
	if path == "" {
		if explicit {
			return Config{}, errors.New("config path is empty")
		}
		var err error
		path, err = DefaultPath()
		if err != nil {
			return Config{}, err
		}
	}
	path, err := expandTilde(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return defaults(filepath.Dir(abs)), nil
		}
		return Config{}, fmt.Errorf("read config %q: %w", abs, err)
	}

	var raw rawConfig
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", abs, err)
	}
	cfg := defaults(filepath.Dir(abs))
	if raw.UI != nil {
		cfg.UI.Icons = raw.UI.Icons
		cfg.UI.DefaultFocus = raw.UI.DefaultFocus
		if cfg.UI.Icons == "" {
			cfg.UI.Icons = "unicode"
		}
		switch cfg.UI.Icons {
		case "unicode", "ascii", "nerd":
		default:
			return Config{}, errors.New("ui.icons must be unicode, ascii, or nerd")
		}
		if seconds := raw.UI.RefreshSeconds; seconds != nil {
			if *seconds != 0 && (*seconds < MinRefreshSeconds || *seconds > 86400) {
				return Config{}, fmt.Errorf("ui.refresh_seconds must be 0 (off) or between %d and 86400", MinRefreshSeconds)
			}
			cfg.UI.RefreshSeconds = int(*seconds)
		}
	}
	if raw.GitHub != nil {
		if raw.GitHub.Enabled != nil {
			cfg.GitHub.Enabled = *raw.GitHub.Enabled
		}
		for _, host := range raw.GitHub.Hosts {
			// Validate before lower-casing: ToLower maps some non-ASCII
			// letters, such as the Kelvin sign, to ASCII ones.
			name := strings.TrimSuffix(host, ".")
			if len(name) > 253 || !hostName.MatchString(name) {
				return Config{}, fmt.Errorf("github.hosts entry %q must be a host name such as github.example.com", host)
			}
			if name = strings.ToLower(name); !slices.Contains(cfg.GitHub.Hosts, name) {
				cfg.GitHub.Hosts = append(cfg.GitHub.Hosts, name)
			}
		}
	}
	if raw.DefaultWorkspace != nil {
		cfg.DefaultWorkspace = *raw.DefaultWorkspace
	}
	if raw.StatusWorkers != nil {
		if *raw.StatusWorkers <= 0 {
			return Config{}, errors.New("status_workers must be positive")
		}
		cfg.StatusWorkers = capWorkers(*raw.StatusWorkers)
	}
	if raw.ActionWorkers != nil {
		if *raw.ActionWorkers <= 0 {
			return Config{}, errors.New("action_workers must be positive")
		}
		cfg.ActionWorkers = capWorkers(*raw.ActionWorkers)
	}
	if raw.StatusTimeoutSeconds != nil {
		if err := validateTimeout("status_timeout_seconds", *raw.StatusTimeoutSeconds); err != nil {
			return Config{}, err
		}
		cfg.StatusTimeoutSeconds = int(*raw.StatusTimeoutSeconds)
	}
	if raw.ActionTimeoutSeconds != nil {
		if err := validateTimeout("action_timeout_seconds", *raw.ActionTimeoutSeconds); err != nil {
			return Config{}, err
		}
		cfg.ActionTimeoutSeconds = int(*raw.ActionTimeoutSeconds)
	}
	seen := make(map[string]struct{}, len(raw.Workspaces))
	for i, w := range raw.Workspaces {
		if strings.TrimSpace(w.Name) == "" {
			return Config{}, fmt.Errorf("workspace %d has an empty name", i+1)
		}
		if _, ok := seen[w.Name]; ok {
			return Config{}, fmt.Errorf("duplicate workspace name %q", w.Name)
		}
		seen[w.Name] = struct{}{}
		maxDepth := DefaultMaxDepth
		if w.MaxDepth != nil {
			if *w.MaxDepth < 0 {
				return Config{}, fmt.Errorf("workspace %q max_depth must not be negative", w.Name)
			}
			maxDepth = *w.MaxDepth
		}
		ignore := append([]string(nil), defaultIgnoreDirs...)
		if w.IgnoreDirs != nil {
			ignore = append([]string(nil), w.IgnoreDirs...)
		}
		paths := make([]string, 0, len(w.Paths))
		for _, p := range w.Paths {
			if strings.TrimSpace(p) == "" {
				return Config{}, fmt.Errorf("workspace %q contains an empty path", w.Name)
			}
			resolved, err := resolveConfigPath(p, cfg.configDir)
			if err != nil {
				return Config{}, fmt.Errorf("workspace %q path %q: %w", w.Name, p, err)
			}
			paths = append(paths, resolved)
		}
		cfg.Workspaces = append(cfg.Workspaces, Workspace{Name: w.Name, Paths: paths, MaxDepth: maxDepth, IgnoreDirs: ignore})
	}
	if cfg.DefaultWorkspace != "" {
		if _, ok := seen[cfg.DefaultWorkspace]; !ok {
			return Config{}, fmt.Errorf("default_workspace %q is not configured", cfg.DefaultWorkspace)
		}
	}
	return cfg, nil
}

// Resolve chooses a workspace and applies positional-root overrides. Relative
// command-line roots are interpreted from cwd. If no workspace or roots are
// configured, cwd is the safe fallback.
func (c Config) Resolve(workspace string, roots []string, cwd string) (Workspace, error) {
	name := workspace
	if name == "" {
		name = c.DefaultWorkspace
	}
	var selected Workspace
	if name != "" {
		found := false
		for _, w := range c.Workspaces {
			if w.Name == name {
				selected = cloneWorkspace(w)
				found = true
				break
			}
		}
		if !found {
			return Workspace{}, fmt.Errorf("unknown workspace %q", name)
		}
	} else {
		selected = Workspace{MaxDepth: DefaultMaxDepth, IgnoreDirs: append([]string(nil), defaultIgnoreDirs...)}
	}
	if len(roots) > 0 {
		selected.Paths = make([]string, 0, len(roots))
		for _, root := range roots {
			if root == "" {
				return Workspace{}, errors.New("root path must not be empty")
			}
			p, err := resolveCLIPath(root, cwd)
			if err != nil {
				return Workspace{}, fmt.Errorf("root %q: %w", root, err)
			}
			selected.Paths = append(selected.Paths, p)
		}
	}
	if len(selected.Paths) == 0 && name != "" {
		// Scanning wherever the user stands would silently ignore their choice.
		return Workspace{}, fmt.Errorf("workspace %q has no paths; add paths or pass ROOT arguments", name)
	}
	if len(selected.Paths) == 0 {
		if cwd == "" {
			var err error
			cwd, err = os.Getwd()
			if err != nil {
				return Workspace{}, fmt.Errorf("get current directory: %w", err)
			}
		}
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return Workspace{}, fmt.Errorf("resolve current directory: %w", err)
		}
		selected.Paths = []string{filepath.Clean(abs)}
	}
	return selected, nil
}

func defaults(configDir string) Config {
	return Config{UI: UI{Icons: "unicode", RefreshSeconds: DefaultRefreshSeconds}, GitHub: GitHub{Enabled: true}, StatusWorkers: DefaultStatusWorkers, ActionWorkers: DefaultActionWorkers,
		StatusTimeoutSeconds: DefaultStatusTimeoutSeconds, ActionTimeoutSeconds: DefaultActionTimeoutSeconds,
		configDir: configDir}
}

func capWorkers(n int64) int {
	if n > MaxWorkers {
		return MaxWorkers
	}
	return int(n)
}

func validateTimeout(name string, seconds int64) error {
	max := int64(math.MaxInt64 / int64(time.Second))
	if strconv.IntSize == 32 && max > math.MaxInt32 {
		max = math.MaxInt32
	}
	if seconds <= 0 {
		return fmt.Errorf("%s must be positive", name)
	}
	if seconds > max {
		return fmt.Errorf("%s exceeds the maximum representable duration", name)
	}
	return nil
}

func resolveConfigPath(path, base string) (string, error) {
	path, err := expandTilde(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Abs(path)
}

func resolveCLIPath(path, cwd string) (string, error) {
	path, err := expandTilde(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		if cwd == "" {
			cwd, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Abs(path)
}

func expandTilde(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func cloneWorkspace(w Workspace) Workspace {
	w.Paths = append([]string(nil), w.Paths...)
	w.IgnoreDirs = append([]string(nil), w.IgnoreDirs...)
	return w
}

type rawConfig struct {
	UI                   *rawUI         `toml:"ui"`
	GitHub               *rawGitHub     `toml:"github"`
	DefaultWorkspace     *string        `toml:"default_workspace"`
	StatusWorkers        *int64         `toml:"status_workers"`
	ActionWorkers        *int64         `toml:"action_workers"`
	StatusTimeoutSeconds *int64         `toml:"status_timeout_seconds"`
	ActionTimeoutSeconds *int64         `toml:"action_timeout_seconds"`
	Workspaces           []rawWorkspace `toml:"workspace"`
}

type rawUI struct {
	Icons          string `toml:"icons"`
	DefaultFocus   bool   `toml:"default_focus"`
	RefreshSeconds *int64 `toml:"refresh_seconds"`
}

type rawGitHub struct {
	Enabled *bool    `toml:"enabled"`
	Hosts   []string `toml:"hosts"`
}

type rawWorkspace struct {
	Name       string   `toml:"name"`
	Paths      []string `toml:"paths"`
	MaxDepth   *int     `toml:"max_depth"`
	IgnoreDirs []string `toml:"ignore_dirs"`
}
