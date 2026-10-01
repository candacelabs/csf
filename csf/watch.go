package csf

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	maxWatchFiles  = 32
	maxWatchBytes  = 1 << 20
	ChangeCreated  = "created"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"
)

// These are in-process snapshots, not wire DTOs. Each check sees the exact
// bytes whose digest caused notification, including explicit missing files.
type SourceFile struct {
	Path    string
	Content []byte
	Missing bool
	Problem string
}
type Change struct{ Path, Kind string }
type CheckRequest struct {
	Changes []Change
	Files   []SourceFile
}
type WatchResult struct {
	Changes []Change
	Err     error
}
type SourceCheck func(ctx context.Context, request CheckRequest) error

type SourceWatch struct {
	root     string
	paths    []string
	interval time.Duration
	check    SourceCheck
}

// NewSourceWatch accepts only explicit relative files, bounded in count and size.
// Checks must honor ctx; a callback owns no additional watcher goroutine.
func NewSourceWatch(root string, paths []string, interval time.Duration, check SourceCheck) (*SourceWatch, error) {
	if len(paths) == 0 || len(paths) > maxWatchFiles || interval <= 0 || check == nil {
		return nil, fmt.Errorf("invalid bounded source watch configuration")
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsLocal(path) || seen[path] {
			return nil, fmt.Errorf("invalid or repeated watch path %q", path)
		}
		seen[path] = true
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("watch root must be a directory")
	}
	return &SourceWatch{root: root, paths: append([]string(nil), paths...), interval: interval, check: check}, nil
}

// Start captures the baseline before returning. One goroutine owns observation,
// checks and coalescing. A slow consumer applies backpressure, cancellably.
func (watch *SourceWatch) Start(ctx context.Context) (<-chan WatchResult, error) {
	root, err := os.OpenRoot(watch.root)
	if err != nil {
		return nil, err
	}
	previous := watch.scan(ctx, root)
	results := make(chan WatchResult, 1)
	go func() {
		defer close(results)
		defer func() { _ = root.Close() }()
		ticker := time.NewTicker(watch.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			next := watch.scan(ctx, root)
			if ctx.Err() != nil {
				return
			}
			changes := changedFiles(previous, next)
			if len(changes) == 0 {
				continue
			}
			previous = next
			result := WatchResult{Changes: changes, Err: watch.check(ctx, CheckRequest{Changes: changes, Files: next})}
			select {
			case <-ctx.Done():
				return
			case results <- result:
			}
		}
	}()
	return results, nil
}

func (watch *SourceWatch) scan(ctx context.Context, root *os.Root) []SourceFile {
	files := make([]SourceFile, 0, len(watch.paths))
	for _, path := range watch.paths {
		if ctx.Err() != nil {
			return files
		}
		source := SourceFile{Path: path}
		source.Content, source.Missing, source.Problem = readWatchFile(root, path)
		files = append(files, source)
	}
	return files
}

func readWatchFile(root *os.Root, path string) ([]byte, bool, string) {
	info, err := root.Stat(path)
	if os.IsNotExist(err) {
		return nil, true, ""
	}
	if err != nil {
		return nil, false, err.Error()
	}
	if !info.Mode().IsRegular() || info.Size() > maxWatchBytes {
		return nil, false, "watch file is not regular or exceeds byte limit"
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, false, err.Error()
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxWatchBytes+1))
	if err != nil {
		return nil, false, err.Error()
	}
	if len(content) > maxWatchBytes {
		return nil, false, "watch file exceeds byte limit"
	}
	return content, false, ""
}

func changedFiles(previous, next []SourceFile) []Change {
	changes := make([]Change, 0)
	for index, source := range next {
		old := previous[index]
		if old.Missing == source.Missing && old.Problem == source.Problem && sha256.Sum256(old.Content) == sha256.Sum256(source.Content) {
			continue
		}
		kind := ChangeModified
		if source.Missing {
			kind = ChangeDeleted
		} else if old.Missing {
			kind = ChangeCreated
		}
		changes = append(changes, Change{Path: source.Path, Kind: kind})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}
