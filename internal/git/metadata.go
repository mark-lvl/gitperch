package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"repodash/internal/repository"
	"sort"
	"strings"
	"time"
)

// Metadata is a fresh local snapshot for planning/revalidation, never a remote
// lookup. Config stays internal to the application; it may contain credentials.
type Metadata struct {
	Status     repository.Status
	CommonDir  string
	ConfigHash string
	Config     map[string][]string
	Remotes    []string
}

func (m Metadata) Values(key string) []string { return m.Config[key] }
func (m Metadata) Value(key string) string {
	values := m.Values(key)
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func (r Runner) Metadata(ctx context.Context, path string) (Metadata, error) {
	m := Metadata{Status: r.Inspect(ctx, path), Config: map[string][]string{}}
	if m.Status.Error != "" {
		return m, fmt.Errorf("inspection failed: %s", m.Status.Error)
	}
	m.CommonDir = m.Status.CommonDir
	out, err := r.Run(ctx, path, "config", "--null", "--list")
	if err != nil {
		return m, err
	}
	m.ConfigHash = fmt.Sprintf("%x", sha256.Sum256(out.Stdout))
	for _, record := range bytes.Split(out.Stdout, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		key, value, ok := strings.Cut(string(record), "\n")
		if !ok {
			value = ""
		}
		m.Config[key] = append(m.Config[key], value)
	}
	out, err = r.Run(ctx, path, "remote")
	if err != nil {
		return m, err
	}
	for _, remote := range strings.Split(strings.TrimSuffix(string(out.Stdout), "\n"), "\n") {
		if remote == "" {
			continue
		}
		// Fetch passes "-c remote.<name>.pruneTags", which Git splits at "=".
		if strings.ContainsAny(remote, " \t\r\x00=") || strings.HasPrefix(remote, "-") {
			return m, fmt.Errorf("unsupported remote name")
		}
		m.Remotes = append(m.Remotes, remote)
	}
	sort.Strings(m.Remotes)
	return m, nil
}

// localMetadata also reports when HEAD last moved, from the reflog's mtime, so
// the workspace can show recent activity without another Git process.
func (r Runner) localMetadata(ctx context.Context, path string) (string, string, time.Time, error) {
	var activity time.Time
	gitPath := func(arg string) (string, error) {
		out, err := r.Run(ctx, path, "rev-parse", "--path-format=absolute", arg)
		if err != nil {
			return "", err
		}
		p := strings.TrimSuffix(string(out.Stdout), "\n")
		if p == "" || !filepath.IsAbs(p) {
			return "", fmt.Errorf("Git returned an invalid metadata path")
		}
		return filepath.Clean(p), nil
	}
	common, err := gitPath("--git-common-dir")
	if err != nil {
		return "", "", activity, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", "", activity, err
	}
	gitDir, err := gitPath("--git-dir")
	if err != nil {
		return "", "", activity, err
	}
	if info, err := os.Stat(filepath.Join(gitDir, "logs", "HEAD")); err == nil {
		activity = info.ModTime()
	}
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_LOG"} {
		if _, err := os.Stat(filepath.Join(gitDir, marker)); err == nil {
			return common, marker, activity, nil
		} else if !os.IsNotExist(err) {
			return "", "", activity, err
		}
	}
	return common, "", activity, nil
}

type FetchTarget struct {
	Remote string
	URL    string
}

func (r Runner) ResolveFetch(ctx context.Context, path string, m Metadata) (FetchTarget, error) {
	remote := m.Value("branch." + m.Status.Branch + ".remote")
	if remote == "" {
		if len(m.Remotes) != 1 {
			return FetchTarget{}, fmt.Errorf("no upstream remote and no sole unambiguous remote")
		}
		remote = m.Remotes[0]
	}
	valid := false
	for _, name := range m.Remotes {
		if remote == name {
			valid = true
		}
	}
	if !valid || remote == "." {
		return FetchTarget{}, fmt.Errorf("upstream remote is not a configured external remote")
	}
	mirror, err := r.configBool(ctx, path, m, "remote."+remote+".mirror")
	if err != nil {
		return FetchTarget{}, err
	}
	if mirror {
		return FetchTarget{}, fmt.Errorf("mirror remote is unsupported")
	}
	refspecs := m.Values("remote." + remote + ".fetch")
	if len(refspecs) == 0 {
		return FetchTarget{}, fmt.Errorf("remote has no conventional tracking refspec")
	}
	for _, spec := range refspecs {
		spec = strings.TrimPrefix(spec, "+")
		src, dst, ok := strings.Cut(spec, ":")
		if !ok || !strings.HasPrefix(src, "refs/heads/") || !strings.HasPrefix(dst, "refs/remotes/"+remote+"/") || strings.Count(src, "*") != strings.Count(dst, "*") || strings.Count(src, "*") > 1 || strings.ContainsAny(spec, " \t\n\r") {
			return FetchTarget{}, fmt.Errorf("unsupported fetch refspec; local branches and tags must not be updated")
		}
	}
	out, err := r.Run(ctx, path, "remote", "get-url", remote)
	if err != nil {
		return FetchTarget{}, err
	}
	url := strings.TrimSuffix(string(out.Stdout), "\n")
	if url == "" {
		return FetchTarget{}, fmt.Errorf("missing fetch URL")
	}
	return FetchTarget{Remote: remote, URL: url}, nil
}

func (r Runner) configBool(ctx context.Context, path string, m Metadata, key string) (bool, error) {
	if len(m.Values(key)) == 0 {
		return false, nil
	}
	out, err := r.Run(ctx, path, "config", "--type=bool", "--get", key)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out.Stdout)) == "true", nil
}

func (r Runner) Fetch(ctx context.Context, path string, target FetchTarget) error {
	_, err := r.Run(ctx, path, "-c", "fetch.pruneTags=false", "-c", "remote."+target.Remote+".pruneTags=false", "fetch", "--prune", "--no-prune-tags", "--no-tags", "--recurse-submodules=no", "--no-auto-maintenance", "--", target.Remote)
	return err
}
