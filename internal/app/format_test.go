package app

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mark-lvl/gitperch/internal/repository"
	"strings"
	"testing"
)

type failingService struct{}

func (failingService) Inspect(_ context.Context, path string) repository.Status {
	if path == "bad" {
		return repository.Status{Error: "failed"}
	}
	return repository.Status{Branch: "main"}
}

func TestFailuresDoNotStopInspection(t *testing.T) {
	rows, err := Inspect(context.Background(), []repository.Repository{{Path: "bad"}, {Path: "good"}}, failingService{}, 8)
	if err != nil || len(rows) != 2 || rows[0].Status.Error == "" || rows[1].Status.Branch != "main" {
		t.Fatalf("%+v %v", rows, err)
	}
}

func TestReadOnlyReport(t *testing.T) {
	rows := []Row{{Repository: repository.Repository{Path: "/repo\nname", Name: "repo\x1b"}, Status: repository.Status{Branch: "main"}}}
	var out bytes.Buffer
	if err := WriteTable(&out, rows); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "synchronized") || strings.ContainsRune(s, '\x1b') || !strings.Contains(s, "?") || !strings.Contains(s, "local") {
		t.Fatal(s)
	}
	out.Reset()
	if err := WriteJSON(&out, rows, nil); err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || report.Repositories[0].Path != rows[0].Path || report.Warnings == nil {
		t.Fatalf("%+v", report)
	}
}

func TestTableShowsPullRequestMarkers(t *testing.T) {
	plain := []Row{{Repository: repository.Repository{Path: "/r", Name: "r"}, Status: repository.Status{Branch: "main", Upstream: "origin/main", ComparisonKnown: true}}}
	var out bytes.Buffer
	if err := WriteTable(&out, plain); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "GitHub") || strings.Contains(out.String(), "pr #") {
		t.Fatalf("table without GitHub data mentions it:\n%s", out.String())
	}
	rows := append(plain, Row{Repository: repository.Repository{Path: "/w", Name: "w"}, Status: repository.Status{Branch: "feat", Upstream: "origin/feat", ComparisonKnown: true},
		GitHub: &GitHubInfo{Error: "gh is not logged in to github.com — run gh auth login", PullRequests: []PullRequest{{Number: 42, State: PullRequestOpen, Draft: true, Checks: ChecksFailing, Review: ReviewChangesRequested}}}})
	out.Reset()
	if err := WriteTable(&out, rows); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pr #42 draft, checks failing, changes requested, github: gh is not logged in to github.com", "* Pull request data comes from GitHub through gh."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, out.String())
		}
	}
}
