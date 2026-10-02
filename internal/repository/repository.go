// Package repository contains the stable identity shared by repository
// discovery and later inspection layers.
package repository

import "path/filepath"

// Repository identifies a worktree by its normalized absolute path. Name is
// only a display label and is not guaranteed to be unique.
type Repository struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// New constructs a repository identity from path. It makes the path absolute
// and cleans it, but deliberately does not resolve symlinks: distinct linked
// worktrees must retain their own paths.
func New(path string) (Repository, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Repository{}, err
	}
	abs = filepath.Clean(abs)
	return Repository{Path: abs, Name: filepath.Base(abs)}, nil
}
