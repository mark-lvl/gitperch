package repository

import (
	"path/filepath"
	"testing"
)

func TestNewNormalizesPathAndUsesBasename(t *testing.T) {
	got, err := New(filepath.Join(".", "alpha", "..", "project"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.Path) || got.Path != filepath.Clean(got.Path) {
		t.Fatalf("path is not normalized absolute: %q", got.Path)
	}
	if got.Name != "project" {
		t.Fatalf("name = %q, want project", got.Name)
	}
}
