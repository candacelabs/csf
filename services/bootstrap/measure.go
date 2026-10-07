// Copyright 2026 Candace Labs

package bootstrap

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/csf/prod"
)

// gitMetadata is the directory a checkout reaches its repository through; the
// file walk skips it so only tracked files are classified.
const gitMetadata = ".git"

// Measure reads a repository into a prod.Reading by the marker convention the
// merge quality gate and this loop share: a top-level directory's count for a
// chief row is the marker file named after the row's id inside it, and the
// files are every regular file under the repository, by slash path. The
// per-directory checker that computes the counts from the tree is not landed,
// so a directory with no markers reads clean — the loop and the gate agree
// because they read the convention through this one function.
func Measure(ctx context.Context, repo string) (prod.Reading, error) {
	if err := ctx.Err(); err != nil {
		return prod.Reading{}, err
	}
	chief, err := readChiefCounts(repo)
	if err != nil {
		return prod.Reading{}, err
	}
	files, err := readTrackedFiles(repo)
	if err != nil {
		return prod.Reading{}, err
	}
	return prod.Reading{Chief: chief, Files: files}, nil
}

// readChiefCounts reads each top-level directory's count for a chief row: the
// marker file named after the row's id inside the directory.
func readChiefCounts(repo string) (map[string]map[string]int, error) {
	entries, err := os.ReadDir(repo)
	if err != nil {
		return nil, err
	}
	chief := map[string]map[string]int{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		counts := map[string]int{}
		for _, row := range prod.ChiefRows {
			if _, err := os.Stat(filepath.Join(repo, entry.Name(), row.ID)); err == nil {
				counts[row.ID] = 1
			}
		}
		chief[entry.Name()] = counts
	}
	return chief, nil
}

// readTrackedFiles walks the repository and returns every regular file, by
// slash path, skipping the VCS metadata directory a checkout carries.
func readTrackedFiles(repo string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(repo, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == gitMetadata {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
