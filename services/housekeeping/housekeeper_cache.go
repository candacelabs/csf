// Copyright 2026 Candace Labs

package housekeeping

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path/filepath"
	"slices"
	"time"

	"github.com/candacelabs/csf/io/ipc/proc"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/harness"
)

// removeBatch bounds the entries one removal names.
const removeBatch = 256

// cacheEntry is one file of the shared Bazel disk cache.
type cacheEntry struct {
	name     string
	size     uint64
	modified time.Time
}

// SharedCache trims the shared Bazel disk cache to its derived size,
// oldest entries first.
func (housekeeper *Housekeeper) SharedCache(ctx context.Context, occurrence cronservice.Occurrence) error {
	return housekeeper.trimSharedCache(ctx, passOf(occurrence))
}

// trimSharedCache derives the cache's size from the disk: the cache may
// hold at most half of the space above the disk floor, counting the space
// it holds now, so it never pushes free disk below the floor and leaves
// as much again for everything else.
func (housekeeper *Housekeeper) trimSharedCache(ctx context.Context, at pass) error {
	entries, total, err := housekeeper.readSharedCache()
	if err != nil || total == 0 {
		return err
	}
	sessions, err := housekeeper.census(ctx)
	if err != nil {
		return err
	}
	floor := housekeeper.deriveFloor(ctx, sessions)
	free, err := housekeeper.freeBytes(ctx)
	if err != nil {
		return err
	}
	var target uint64
	if free+total > floor.bytes {
		target = (free + total - floor.bytes) / 2
	}
	if total <= target {
		return nil
	}
	slices.SortStableFunc(entries, func(left, right cacheEntry) int { return left.modified.Compare(right.modified) })
	var failures []error
	for len(entries) > 0 && total > target {
		var batch []cacheEntry
		var size uint64
		for len(entries) > 0 && len(batch) < removeBatch && total-size > target {
			batch, size = append(batch, entries[0]), size+entries[0].size
			entries = entries[1:]
		}
		total -= size
		failures = append(failures, housekeeper.removeCacheEntries(ctx, at, batch, size, target))
	}
	return errors.Join(failures...)
}

func (housekeeper *Housekeeper) removeCacheEntries(ctx context.Context, at pass, batch []cacheEntry, size uint64, target uint64) error {
	paths := make([]string, 0, len(batch)+2)
	paths = append(paths, "-f", "--")
	for _, entry := range batch {
		paths = append(paths, filepath.Join(housekeeper.stateDirectory, entry.name))
	}
	return housekeeper.delete(ctx, at, Record{
		Kind:   KindSharedCache,
		What:   fmt.Sprintf("%d entries of %s, modified %s to %s", len(batch), harness.BazelDiskCacheDirectory, batch[0].modified.UTC().Format(time.RFC3339), batch[len(batch)-1].modified.UTC().Format(time.RFC3339)),
		Bytes:  size,
		Detail: fmt.Sprintf("trimming to %d bytes: half the free space above the disk floor", target),
	}, func(ctx context.Context) (uint64, error) {
		_, err := housekeeper.launcher.Run(ctx, proc.Command{Executable: programRemove, Arguments: paths})
		return size, err
	})
}

// readSharedCache lists every regular file of the shared cache.
func (housekeeper *Housekeeper) readSharedCache() ([]cacheEntry, uint64, error) {
	var entries []cacheEntry
	var total uint64
	err := stdfs.WalkDir(housekeeper.state, harness.BazelDiskCacheDirectory, func(name string, entry stdfs.DirEntry, err error) error {
		if err != nil {
			if name == harness.BazelDiskCacheDirectory && errors.Is(err, stdfs.ErrNotExist) {
				return stdfs.SkipAll
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		size := uint64(max(info.Size(), 0))
		entries = append(entries, cacheEntry{name: name, size: size, modified: info.ModTime()})
		total += size
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("housekeeping: read the shared cache: %w", err)
	}
	slices.SortStableFunc(entries, func(left, right cacheEntry) int { return cmp.Compare(left.name, right.name) })
	return entries, total, nil
}
