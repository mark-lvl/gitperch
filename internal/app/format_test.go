package app

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/markkaghazgarian/gitperch/internal/repository"
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
