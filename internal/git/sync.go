package git

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

type UpstreamTarget struct {
	Fetch               FetchTarget
	Branch, TrackingRef string
}
type PushTarget struct{ Remote, URL, SourceRef, DestinationRef, HeadOID string }

func (r Runner) ResolveUpstream(ctx context.Context, path string, m Metadata) (UpstreamTarget, error) {
	s := m.Status
	if s.Detached || s.Unborn || s.Branch == "" {
		return UpstreamTarget{}, fmt.Errorf("detached or unborn branch")
	}
	key := "branch." + s.Branch
	remotes, merges := m.Values(key+".remote"), m.Values(key+".merge")
	if len(remotes) != 1 || len(merges) != 1 || remotes[0] == "" || !strings.HasPrefix(merges[0], "refs/heads/") || len(merges[0]) <= len("refs/heads/") {
		return UpstreamTarget{}, fmt.Errorf("one conventional configured upstream is required")
	}
	fetch, err := r.ResolveFetch(ctx, path, m)
	if err != nil {
		return UpstreamTarget{}, err
	}
	var tracking []string
	for _, spec := range m.Values("remote." + fetch.Remote + ".fetch") {
		src, dst, _ := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
		if !strings.Contains(src, "*") {
			if src == merges[0] {
				tracking = append(tracking, dst)
			}
			continue
		}
		prefix, suffix, _ := strings.Cut(src, "*")
		if strings.HasPrefix(merges[0], prefix) && strings.HasSuffix(merges[0], suffix) && len(merges[0]) >= len(prefix)+len(suffix) {
			middle := merges[0][len(prefix) : len(merges[0])-len(suffix)]
			tracking = append(tracking, strings.Replace(dst, "*", middle, 1))
		}
	}
	slices.Sort(tracking)
	if tracking = slices.Compact(tracking); len(tracking) != 1 { // refspecs may repeat a mapping
		return UpstreamTarget{}, fmt.Errorf("upstream has no unambiguous tracking mapping")
	}
	if _, err := r.Run(ctx, path, "check-ref-format", merges[0]); err != nil {
		return UpstreamTarget{}, err
	}
	if _, err := r.Run(ctx, path, "check-ref-format", tracking[0]); err != nil {
		return UpstreamTarget{}, err
	}
	return UpstreamTarget{Fetch: fetch, Branch: merges[0], TrackingRef: tracking[0]}, nil
}

func (r Runner) ResolvePush(ctx context.Context, path string, m Metadata, upstream UpstreamTarget) (PushTarget, error) {
	remote := upstream.Fetch.Remote
	pushRemote := m.Value("branch." + m.Status.Branch + ".pushremote")
	if pushRemote == "" {
		pushRemote = m.Value("remote.pushdefault")
	}
	if pushRemote == "" {
		pushRemote = remote
	}
	if pushRemote != remote {
		return PushTarget{}, fmt.Errorf("push remote differs from upstream; triangular workflows are unsupported")
	}
	mirror, err := r.configBool(ctx, path, m, "remote."+remote+".mirror")
	if err != nil {
		return PushTarget{}, err
	}
	if mirror {
		return PushTarget{}, fmt.Errorf("mirror pushes are unsupported")
	}
	source := "refs/heads/" + m.Status.Branch
	specs := m.Values("remote." + remote + ".push")
	if len(specs) > 0 {
		destinations := map[string]bool{}
		for _, spec := range specs {
			if strings.HasPrefix(spec, "+") {
				return PushTarget{}, fmt.Errorf("force-configured push refspec is unsupported")
			}
			src, dst, colon := strings.Cut(spec, ":")
			if !colon {
				dst = src
			}
			if src == "" || dst == "" || strings.ContainsAny(spec, " \t\n\r") {
				return PushTarget{}, fmt.Errorf("unsupported push refspec")
			}
			if src == "HEAD" {
				if !colon { // a bare HEAD pushes the current branch to its own name
					dst = source
				}
				src = source
			} else if !strings.HasPrefix(src, "refs/") {
				src = "refs/heads/" + src
			}
			if !strings.HasPrefix(dst, "refs/") {
				dst = "refs/heads/" + dst
			}
			if !strings.HasPrefix(src, "refs/heads/") || !strings.HasPrefix(dst, "refs/heads/") || strings.Count(src, "*") != strings.Count(dst, "*") || strings.Count(src, "*") > 1 {
				return PushTarget{}, fmt.Errorf("unsupported push refspec mapping")
			}
			if strings.Contains(src, "*") {
				prefix, suffix, _ := strings.Cut(src, "*")
				if strings.HasPrefix(source, prefix) && strings.HasSuffix(source, suffix) && len(source) >= len(prefix)+len(suffix) {
					middle := source[len(prefix) : len(source)-len(suffix)]
					destinations[strings.Replace(dst, "*", middle, 1)] = true
				}
			} else if src == source {
				destinations[dst] = true
			}
		}
		if len(destinations) != 1 || !destinations[upstream.Branch] {
			return PushTarget{}, fmt.Errorf("push destination mapping differs from upstream or is ambiguous")
		}
	} else {
		mode := m.Value("push.default")
		if mode == "" {
			mode = "simple"
		}
		switch mode {
		case "upstream":
		case "simple", "current":
			if source != upstream.Branch {
				return PushTarget{}, fmt.Errorf("push destination differs from upstream")
			}
		default:
			return PushTarget{}, fmt.Errorf("push.default=%s is unsupported or ambiguous", SafeText(mode))
		}
	}
	out, err := r.Run(ctx, path, "remote", "get-url", "--push", "--all", remote)
	if err != nil {
		return PushTarget{}, err
	}
	urls := strings.Split(strings.TrimSuffix(string(out.Stdout), "\n"), "\n")
	if len(urls) != 1 || urls[0] == "" {
		return PushTarget{}, fmt.Errorf("multiple or missing push URLs are unsupported")
	}
	return PushTarget{Remote: remote, URL: urls[0], SourceRef: source, DestinationRef: upstream.Branch, HeadOID: m.Status.HeadOID}, nil
}

func (r Runner) UpstreamCommit(ctx context.Context, path string, target UpstreamTarget) (string, error) {
	out, err := r.Run(ctx, path, "rev-parse", "--verify", target.TrackingRef+"^{commit}")
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out.Stdout))
	if !objectID(oid) {
		return "", fmt.Errorf("invalid upstream commit")
	}
	return oid, nil
}

func (r Runner) Push(ctx context.Context, path string, target PushTarget) error {
	if !objectID(target.HeadOID) || !strings.HasPrefix(target.DestinationRef, "refs/heads/") || target.Remote == "" || strings.HasPrefix(target.Remote, "-") {
		return fmt.Errorf("invalid reviewed push target")
	}
	// Pin the reviewed branch's object ID, so an external local commit made
	// after revalidation cannot accidentally join this push.
	out, err := r.Run(ctx, path, "-c", "push.followTags=false", "-c", "push.recurseSubmodules=no", "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--", target.Remote, target.HeadOID+":"+target.DestinationRef)
	if err != nil {
		if reasons := pushRejections(out.Stdout); reasons != "" {
			return fmt.Errorf("%s: %w", reasons, err)
		}
	}
	return err
}

// pushRejections summarizes rejected refs from porcelain push output, which
// Git writes to stdout ("!\t<src>:<dst>\t[rejected] (fetch first)"), while
// stderr carries only a generic failure and optional advice.
func pushRejections(stdout []byte) string {
	var reasons []string
	for _, line := range strings.Split(string(stdout), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || fields[0] != "!" {
			continue
		}
		ref := fields[1][strings.LastIndex(fields[1], ":")+1:]
		reasons = append(reasons, SafeText(fields[2]+" for "+ref))
	}
	return strings.Join(reasons, "; ")
}

func (r Runner) FastForward(ctx context.Context, path, commit string) error {
	if !objectID(commit) {
		return fmt.Errorf("invalid reviewed integration commit")
	}
	_, err := r.Run(ctx, path, "-c", "merge.autoStash=false", "merge", "--ff-only", "--no-autostash", "--no-edit", commit)
	return err
}
