// Copyright 2026 Candace Labs

package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/pkg/atomicfile"
)

// snapshot copies the working tree into a fresh sibling directory, so a
// refused change can be rolled back. It copies regular files and directories,
// not symlinks: a bootstrap repository is a tree of regular files.
func (l *Loop) snapshot(repo string) (string, error) {
	target := filepath.Join(filepath.Dir(repo), fmt.Sprintf(".%s.snapshot", filepath.Base(repo)))
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	// Create the snapshot root before walking, so a repository whose first
	// member is a top-level file — not a directory — has somewhere to write.
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	root := filepath.Clean(repo)
	walk := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return atomicfile.WriteFile(destination, content, info.Mode().Perm())
	}
	if err := filepath.Walk(root, walk); err != nil {
		return "", err
	}
	return target, nil
}

// restore replaces the working tree with the snapshot, a move that consumes
// the snapshot directory.
func (l *Loop) restore(repo, snapshot string) error {
	if err := os.RemoveAll(repo); err != nil {
		return err
	}
	return os.Rename(snapshot, repo)
}

// removeSnapshot discards a snapshot whose change was kept.
func (l *Loop) removeSnapshot(snapshot string) error { return os.RemoveAll(snapshot) }
