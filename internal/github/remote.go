package github

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Repo names a repository on a GitHub host.
type Repo struct{ Host, Owner, Name string }

// String is the HOST/OWNER/REPO form gh accepts for --repo.
func (r Repo) String() string { return r.Host + "/" + r.Owner + "/" + r.Name }

// FullName is OWNER/REPO.
func (r Repo) FullName() string { return r.Owner + "/" + r.Name }

var repoPart = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ParseRemote recognizes a GitHub repository in a Git remote URL: https://,
// http://, ssh:// and git:// URLs, and scp-like [user@]host:owner/name. Only
// github.com (www.github.com and ssh.github.com included) and the given
// Enterprise hosts count. User information and ports are ignored, and the
// path must be exactly owner/name, with an optional .git suffix.
func ParseRemote(raw string, hosts []string) (Repo, bool) {
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Repo{}, false
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git":
		default:
			return Repo{}, false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// Git reads host:path as scp-like syntax only when no slash comes
		// before the first colon; anything else is a local path.
		colon := strings.Index(raw, ":")
		if colon <= 0 || strings.Contains(raw[:colon], "/") {
			return Repo{}, false
		}
		host, path = raw[:colon], raw[colon+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	switch host {
	case "www.github.com", "ssh.github.com":
		host = "github.com"
	}
	if host != "github.com" && !slices.Contains(hosts, host) {
		return Repo{}, false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !ok || strings.Contains(name, "/") {
		return Repo{}, false
	}
	for _, part := range []string{owner, name} {
		if !repoPart.MatchString(part) || part == "." || part == ".." {
			return Repo{}, false
		}
	}
	return Repo{Host: host, Owner: owner, Name: name}, true
}
