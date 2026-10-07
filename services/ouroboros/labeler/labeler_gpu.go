// Copyright 2026 Candace Labs

package labeler

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/proc"
)

// What marks a container as a GPU consumer, and how the GPU is measured.
const (
	runtimeNvidia   = "nvidia"
	driverNvidia    = "nvidia"
	capabilityGPU   = "gpu"
	nameSeparator   = "/"
	filterStatus    = "status"
	programNvidia   = "nvidia-smi"
	queryFlag       = "--query-gpu=utilization.gpu,memory.used"
	formatFlag      = "--format=csv,noheader,nounits"
	sampleFields    = 2
	mebibyte        = 1 << 20
	sampleSeparator = ","
)

// IContainers is the part of the container capability the labeler uses: to
// see which containers run and whether each one holds the GPU. It changes
// nothing.
type IContainers interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
}

// gpuConsumers names the running containers, other than the model server,
// that were created with the GPU: the nvidia runtime, or a device request
// for the nvidia driver or the gpu capability. A container's creation is
// immutable, so the answer for an identifier is cached for the run.
func (labeler *Labeler) gpuConsumers(ctx context.Context) ([]string, error) {
	listed, err := labeler.containers.ContainerList(ctx, client.ContainerListOptions{
		Filters: make(client.Filters).Add(filterStatus, string(container.StateRunning)),
	})
	if err != nil {
		return nil, fmt.Errorf("labeler: list running containers: %w", err)
	}
	var consumers []string
	for _, summary := range listed.Items {
		name := containerName(summary.Names)
		// Model servers, its own and those labelled as such (a reranker),
		// hold the GPU idle between requests: they are not consumers.
		if name == labeler.modelServer || summary.Labels[docker.ModelServerLabel] != "" {
			continue
		}
		holds, known := labeler.gpuByContainer[summary.ID]
		if !known {
			inspected, err := labeler.containers.ContainerInspect(ctx, summary.ID, client.ContainerInspectOptions{})
			if err != nil {
				return nil, fmt.Errorf("labeler: inspect container %s: %w", name, err)
			}
			holds = holdsGPU(inspected.Container.HostConfig)
			labeler.gpuByContainer[summary.ID] = holds
		}
		if holds {
			consumers = append(consumers, name)
		}
	}
	slices.Sort(consumers)
	return consumers, nil
}

// holdsGPU reads a container's creation for the GPU.
func holdsGPU(hostConfig *container.HostConfig) bool {
	if hostConfig == nil {
		return false
	}
	if hostConfig.Runtime == runtimeNvidia {
		return true
	}
	for _, request := range hostConfig.DeviceRequests {
		if request.Driver == driverNvidia || request.Count != 0 || len(request.DeviceIDs) > 0 {
			return true
		}
		for _, capabilities := range request.Capabilities {
			if slices.Contains(capabilities, capabilityGPU) {
				return true
			}
		}
	}
	return false
}

// containerName is the container's first name without the Engine's leading
// slash.
func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], nameSeparator)
}

// gpuSample is one reading of the GPU: utilization in percent and memory in
// use in bytes.
type gpuSample struct {
	utilization float64
	memoryBytes int64
}

// sampleGPU reads the GPU through nvidia-smi. A host without it, or a
// failing query, reads as unmeasured: the run records -1 rather than a
// number nobody measured.
func (labeler *Labeler) sampleGPU(ctx context.Context) (gpuSample, bool) {
	result, err := labeler.launcher.Run(ctx, proc.Command{Executable: programNvidia, Arguments: []string{queryFlag, formatFlag}})
	if err != nil {
		labeler.logger.Debug("labeler: GPU unmeasured", "error", err)
		return gpuSample{}, false
	}
	fields := strings.Split(strings.TrimSpace(string(result.Stdout)), sampleSeparator)
	if len(fields) != sampleFields {
		return gpuSample{}, false
	}
	utilization, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	if err != nil {
		return gpuSample{}, false
	}
	memory, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
	if err != nil {
		return gpuSample{}, false
	}
	return gpuSample{utilization: utilization, memoryBytes: memory * mebibyte}, true
}

// keepAliveFor derives the keep-alive sent with the next request from two
// measurements: the gaps between the starts of the batches so far, and the
// model's load time. The model stays loaded for twice the median gap, so
// the next batch of the same run finds it loaded while a run that ended
// lets it go within a few gaps; the floor of twice the load time keeps a
// reload (which costs the load) from happening more often than it pays. No
// gap and no load measured yet leaves the server's default in force.
func keepAliveFor(gaps []time.Duration, load time.Duration) time.Duration {
	return max(2*load, 2*median(gaps))
}

// median is the middle of the sorted durations, or zero of none.
func median(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}
