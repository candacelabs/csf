// Copyright 2026 Candace Labs

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	cronservice "github.com/candacelabs/csf/services/cron"
)

// The Docker classes the docker trigger removes, and nothing else.
const (
	// RunnerContainerPrefix names the CI runner containers whose exited
	// leftovers are removed.
	RunnerContainerPrefix = "candace-docker-runner"

	filterName     = "name"
	filterStatus   = "status"
	filterDangling = "dangling"
	filterTrue     = "true"
	nameSeparator  = "/"
)

// Docker removes the exited CI runner containers, the dangling images and
// the unused build cache. It never removes a volume (the capability it is
// granted has no volume operation), a running container or an image a
// container uses.
func (housekeeper *Housekeeper) Docker(ctx context.Context, occurrence cronservice.Occurrence) error {
	return housekeeper.reclaimDocker(ctx, passOf(occurrence))
}

func (housekeeper *Housekeeper) reclaimDocker(ctx context.Context, at pass) error {
	return errors.Join(
		housekeeper.removeRunnerContainers(ctx, at),
		housekeeper.pruneDanglingImages(ctx, at),
		housekeeper.pruneBuildCache(ctx, at),
	)
}

func (housekeeper *Housekeeper) removeRunnerContainers(ctx context.Context, at pass) error {
	listed, err := housekeeper.containers.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Size:    true,
		Filters: make(client.Filters).Add(filterName, RunnerContainerPrefix).Add(filterStatus, string(container.StateExited)),
	})
	if err != nil {
		return fmt.Errorf("housekeeping: list runner containers: %w", err)
	}
	var failures []error
	for _, summary := range listed.Items {
		// The Engine's name filter matches anywhere in the name and its
		// status filter is trusted no further than this check.
		if summary.State != container.StateExited || !runnerName(summary.Names) {
			continue
		}
		id := summary.ID
		failures = append(failures, housekeeper.delete(ctx, at,
			Record{Kind: KindRunnerContainer, What: strings.TrimPrefix(summary.Names[0], nameSeparator), Bytes: uint64(max(summary.SizeRw, 0)), Detail: id},
			func(ctx context.Context) (uint64, error) {
				_, err := housekeeper.containers.ContainerRemove(ctx, id, client.ContainerRemoveOptions{})
				return uint64(max(summary.SizeRw, 0)), err
			}))
	}
	return errors.Join(failures...)
}

func runnerName(names []string) bool {
	for _, name := range names {
		if strings.HasPrefix(strings.TrimPrefix(name, nameSeparator), RunnerContainerPrefix) {
			return true
		}
	}
	return false
}

func (housekeeper *Housekeeper) pruneDanglingImages(ctx context.Context, at pass) error {
	dangling := make(client.Filters).Add(filterDangling, filterTrue)
	listed, err := housekeeper.containers.ImageList(ctx, client.ImageListOptions{Filters: dangling})
	if err != nil {
		return fmt.Errorf("housekeeping: list dangling images: %w", err)
	}
	if len(listed.Items) == 0 {
		return nil
	}
	var size int64
	for _, summary := range listed.Items {
		size += summary.Size
	}
	return housekeeper.delete(ctx, at,
		Record{Kind: KindDanglingImages, What: fmt.Sprintf("%d dangling images", len(listed.Items)), Bytes: uint64(max(size, 0))},
		func(ctx context.Context) (uint64, error) {
			pruned, err := housekeeper.containers.ImagePrune(ctx, client.ImagePruneOptions{Filters: dangling})
			return pruned.Report.SpaceReclaimed, err
		})
}

func (housekeeper *Housekeeper) pruneBuildCache(ctx context.Context, at pass) error {
	usage, err := housekeeper.containers.DiskUsage(ctx, client.DiskUsageOptions{BuildCache: true})
	if err != nil {
		return fmt.Errorf("housekeeping: measure the build cache: %w", err)
	}
	cache := usage.BuildCache
	if cache.TotalCount == 0 {
		return nil
	}
	return housekeeper.delete(ctx, at,
		Record{Kind: KindBuildCache, What: fmt.Sprintf("build cache (%d records, %d bytes in all)", cache.TotalCount, cache.TotalSize), Bytes: uint64(max(cache.Reclaimable, 0))},
		func(ctx context.Context) (uint64, error) {
			pruned, err := housekeeper.containers.BuildCachePrune(ctx, client.BuildCachePruneOptions{All: true})
			return pruned.Report.SpaceReclaimed, err
		})
}
