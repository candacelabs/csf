// Copyright 2026 Candace Labs

package opsview_test

import (
	"context"
	"slices"
	"sync"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/services/harness/session"
)

// hostEngine is a Docker Engine double: a table of containers the gomock
// double reads and changes. The lock is a leaf: the host operations make
// their changes concurrently, and each call touches one row and nothing else.
type hostEngine struct {
	lock  sync.Mutex
	items []container.Summary
	// stuck names containers whose state changes the Engine accepts and
	// ignores, which is what the acceptance check is there to catch.
	stuck map[string]bool
	// calls records every state change made, as "action name".
	calls []string
	cpu   uint64
}

// newHostEngine grants a double over items, every call of it allowed.
func newHostEngine(controller *gomock.Controller, items ...container.Summary) (*MockIHostContainers, *hostEngine) {
	engine := &hostEngine{items: items, stuck: map[string]bool{}}
	double := NewMockIHostContainers(controller)
	double.EXPECT().ContainerList(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		engine.lock.Lock()
		defer engine.lock.Unlock()
		return client.ContainerListResult{Items: slices.Clone(engine.items)}, nil
	}).AnyTimes()
	change := func(action string, state container.ContainerState) func(ctx context.Context, name string) {
		return func(_ context.Context, name string) {
			engine.lock.Lock()
			defer engine.lock.Unlock()
			engine.calls = append(engine.calls, action+" "+name)
			if engine.stuck[name] {
				return
			}
			for index := range engine.items {
				if engine.items[index].Names[0] == "/"+name {
					engine.items[index].State = state
				}
			}
		}
	}
	start, stop, pause, unpause := change("start", container.StateRunning), change("stop", container.StateExited), change("pause", container.StatePaused), change("unpause", container.StateRunning)
	double.EXPECT().ContainerStart(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, name string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
		start(ctx, name)
		return client.ContainerStartResult{}, nil
	}).AnyTimes()
	double.EXPECT().ContainerStop(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, name string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
		stop(ctx, name)
		return client.ContainerStopResult{}, nil
	}).AnyTimes()
	double.EXPECT().ContainerPause(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, name string, _ client.ContainerPauseOptions) (client.ContainerPauseResult, error) {
		pause(ctx, name)
		return client.ContainerPauseResult{}, nil
	}).AnyTimes()
	double.EXPECT().ContainerUnpause(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, name string, _ client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error) {
		unpause(ctx, name)
		return client.ContainerUnpauseResult{}, nil
	}).AnyTimes()
	double.EXPECT().SampleContainer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string) (docker.ContainerSample, error) {
		engine.lock.Lock()
		defer engine.lock.Unlock()
		engine.cpu += 1_000_000_000
		return docker.ContainerSample{CPUNanoseconds: engine.cpu / 4, SystemNanoseconds: engine.cpu, OnlineCPUs: 4, MemoryBytes: 256 << 20}, nil
	}).AnyTimes()
	return double, engine
}

// state is one container's state in the table.
func (engine *hostEngine) state(name string) string {
	engine.lock.Lock()
	defer engine.lock.Unlock()
	for _, item := range engine.items {
		if item.Names[0] == "/"+name {
			return string(item.State)
		}
	}
	return ""
}

// changes is every state change the Engine was asked for.
func (engine *hostEngine) changes() []string {
	engine.lock.Lock()
	defer engine.lock.Unlock()
	return slices.Clone(engine.calls)
}

// hostContainer is one container summary: a name, a state, and optionally a
// session's assignment label or a bind mount.
func hostContainer(name string, state container.ContainerState, assignment string, mountSource string) container.Summary {
	summary := container.Summary{ID: "id-" + name, Names: []string{"/" + name}, Image: "image/" + name, State: state, Status: string(state), Labels: map[string]string{}}
	if assignment != "" {
		summary.Labels[session.LabelAssignment] = assignment
	}
	if mountSource != "" {
		summary.Mounts = []container.MountPoint{{Source: mountSource, Destination: "/workspace"}}
	}
	return summary
}
