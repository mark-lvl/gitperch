package git

import (
	"context"
	"testing"
)

func resolvePush(t *testing.T, repo string) (PushTarget, error) {
	t.Helper()
	r := Runner{}
	m, err := r.Metadata(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := r.ResolveUpstream(context.Background(), repo, m)
	if err != nil {
		t.Fatal(err)
	}
	return r.ResolvePush(context.Background(), repo, m, upstream)
}

// A bare HEAD push refspec pushes the current branch to the same name.
func TestResolvePushAcceptsHEADRefspec(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "config", "remote.origin.push", "HEAD")
	target, err := resolvePush(t, repo)
	if err != nil || target.SourceRef != "refs/heads/main" || target.DestinationRef != "refs/heads/main" {
		t.Fatalf("push HEAD: %+v %v", target, err)
	}
	gitCmd(t, repo, "config", "remote.origin.push", "HEAD:refs/heads/other")
	if target, err := resolvePush(t, repo); err == nil {
		t.Fatalf("HEAD to another branch accepted: %+v", target)
	}
}

// Fetch refspecs that agree on the tracking ref are not ambiguous; refspecs
// that map the branch to different tracking refs are.
func TestResolveUpstreamDeduplicatesTrackingRefs(t *testing.T) {
	repo, _ := repoWithRemote(t)
	gitCmd(t, repo, "config", "--add", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
	r := Runner{}
	m, err := r.Metadata(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := r.ResolveUpstream(context.Background(), repo, m)
	if err != nil || upstream.TrackingRef != "refs/remotes/origin/main" {
		t.Fatalf("duplicate mapping: %+v %v", upstream, err)
	}
	gitCmd(t, repo, "config", "--add", "remote.origin.fetch", "+refs/heads/main:refs/remotes/mirror/main")
	if m, err = r.Metadata(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	if upstream, err := r.ResolveUpstream(context.Background(), repo, m); err == nil {
		t.Fatalf("distinct mappings accepted: %+v", upstream)
	}
}
