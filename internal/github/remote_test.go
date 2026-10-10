package github

import "testing"

func TestParseRemote(t *testing.T) {
	enterprise := []string{"ghe.example.com"}
	for raw, want := range map[string]Repo{
		"https://github.com/acme/widgets.git":           {"github.com", "acme", "widgets"},
		"https://github.com/acme/widgets":               {"github.com", "acme", "widgets"},
		"https://github.com/acme/widgets/":              {"github.com", "acme", "widgets"},
		"https://token@github.com/acme/widgets.git":     {"github.com", "acme", "widgets"},
		"http://www.github.com/acme/widgets":            {"github.com", "acme", "widgets"},
		"ssh://git@github.com/acme/widgets.git":         {"github.com", "acme", "widgets"},
		"ssh://git@ssh.github.com:443/acme/widgets.git": {"github.com", "acme", "widgets"},
		"git://github.com/acme/widgets.git":             {"github.com", "acme", "widgets"},
		"git@github.com:acme/widgets.git":               {"github.com", "acme", "widgets"},
		"git@GitHub.com:Acme/my.repo_v2-x":              {"github.com", "Acme", "my.repo_v2-x"},
		"https://ghe.example.com/team/service.git":      {"ghe.example.com", "team", "service"},
		"git@ghe.example.com:team/service.git":          {"ghe.example.com", "team", "service"},
		"https://ghe.example.com:8443/team/service.git": {"ghe.example.com", "team", "service"},
	} {
		got, ok := ParseRemote(raw, enterprise)
		if !ok || got != want {
			t.Errorf("%q: %+v %v, want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"",
		"/srv/git/widgets.git",
		"../remote.git",
		"./a:b/c",
		"file:///srv/git/acme/widgets.git",
		"https://gitlab.com/acme/widgets.git",
		"git@github-work:acme/widgets.git", // SSH host alias: not resolved
		"https://github.com/acme",
		"https://github.com/acme/widgets/tree/main",
		"https://github.com/../widgets",
		"https://github.com/acme/wid gets",
		"git@github.com:acme/",
		"ftp://github.com/acme/widgets",
	} {
		if got, ok := ParseRemote(raw, enterprise); ok {
			t.Errorf("%q accepted as %+v", raw, got)
		}
	}
	if _, ok := ParseRemote("https://ghe.example.com/team/service.git", nil); ok {
		t.Error("an unconfigured Enterprise host was accepted")
	}
	r := Repo{"github.com", "acme", "widgets"}
	if r.String() != "github.com/acme/widgets" || r.FullName() != "acme/widgets" {
		t.Fatalf("names: %q %q", r.String(), r.FullName())
	}
}
