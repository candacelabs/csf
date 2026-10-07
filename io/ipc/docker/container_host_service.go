// Copyright 2026 Candace Labs

package docker

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/candacelabs/csf/pkg/boundedbuffer"
	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"
)

// ErrNoContainer reports a named container the Engine does not have.
var ErrNoContainer = errors.New("ipc/docker: no such container")

// ModelServerLabel marks a container that serves a model on request and is
// idle otherwise, such as a reranker: holding the GPU is not using it, so a
// GPU consumer that yields to others does not yield to it.
const ModelServerLabel = "dev.csf.model-server"

// The device request that grants a container every GPU.
const (
	gpuDriver     = "nvidia"
	gpuCapability = "gpu"
	allGPUs       = -1
)

// ServiceSpec is one long-lived named container: created once, kept across
// restarts of the caller, and restarted by the Engine unless stopped. Its
// data lives in named volumes, which removing the container never removes.
type ServiceSpec struct {
	Name        string
	Image       string
	Environment []string
	Labels      map[string]string
	// Volumes maps a named volume to the path it is mounted at; the Engine
	// creates a missing volume.
	Volumes map[string]string
	// Mounts are host paths visible inside the container. A missing source is
	// an error rather than a root-owned directory the Engine creates.
	Mounts []Mount
	// Port is the container port ("5432/tcp") published on HostAddress at
	// HostPort, which [ServiceState.HostPort] reports. A fixed HostPort keeps
	// the same port across restarts; zero lets the Engine choose one on every
	// start.
	Port        string
	HostAddress netip.Addr
	HostPort    uint16
	// ExtraHostAddresses publish Port at the same HostPort on each further
	// address, such as a tailnet address beside the loopback one; they need a
	// fixed HostPort, so every address serves the one recorded port.
	ExtraHostAddresses []netip.Addr
	// Command replaces the image's default arguments; empty keeps them.
	Command []string
	// HealthCheck is the command the Engine runs every HealthInterval;
	// [ServiceState.Health] reports its verdict.
	HealthCheck    []string
	HealthInterval time.Duration
	// GPU grants the container the host's GPUs through the nvidia driver.
	GPU bool
}

// ServiceState is what the Engine reports of a named container.
type ServiceState struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Running bool   `json:"running"`
	// Health is the health check's verdict: starting, healthy or unhealthy.
	Health   string `json:"health"`
	HostPort uint16 `json:"host_port"`
}

// ExecSpec is one command run inside a running container.
type ExecSpec struct {
	Command []string
	// User is the user[:group] the command runs as; empty means the
	// container's.
	User string
}

// ExecResult is what a finished command left behind, each output bounded by
// the host's output limit.
type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

const (
	healthCommandShell = "CMD"
	inspectFailed      = "ipc/docker: inspect %s: %w"
)

// EnsureService brings the named container up: it pulls the image when the
// Engine lacks it, creates the container when none has the name, and starts
// it when it is not running. An existing container is never recreated, so
// its configuration is the one it was created with.
func (host *ContainerHost) EnsureService(ctx context.Context, spec ServiceSpec) (ServiceState, error) {
	state, err := host.InspectService(ctx, spec.Name)
	if errors.Is(err, ErrNoContainer) {
		if err := host.create(ctx, spec); err != nil {
			return ServiceState{}, err
		}
		state, err = host.InspectService(ctx, spec.Name)
	}
	if err != nil {
		return ServiceState{}, err
	}
	if state.Running {
		return state, nil
	}
	if _, err := host.ContainerStart(ctx, state.ID, client.ContainerStartOptions{}); err != nil {
		return ServiceState{}, fmt.Errorf("ipc/docker: start %s: %w", spec.Name, err)
	}
	return host.InspectService(ctx, spec.Name)
}

// InspectService reports the named container, or [ErrNoContainer].
func (host *ContainerHost) InspectService(ctx context.Context, name string) (ServiceState, error) {
	inspected, err := host.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return ServiceState{}, fmt.Errorf("%w: %s", ErrNoContainer, name)
	}
	if err != nil {
		return ServiceState{}, fmt.Errorf(inspectFailed, name, err)
	}
	return serviceState(inspected.Container), nil
}

// SignalService sends signal, such as "HUP", to the named container's main
// process: how a long-lived service is told to reread its configuration.
func (host *ContainerHost) SignalService(ctx context.Context, name string, signal string) error {
	if _, err := host.ContainerKill(ctx, name, client.ContainerKillOptions{Signal: signal}); err != nil {
		return fmt.Errorf("ipc/docker: signal %s %s: %w", name, signal, err)
	}
	return nil
}

// StopService stops the named container and leaves it, and its volumes, in
// place for a later [ContainerHost.EnsureService].
func (host *ContainerHost) StopService(ctx context.Context, name string) error {
	if _, err := host.InspectService(ctx, name); err != nil {
		return err
	}
	if _, err := host.ContainerStop(ctx, name, client.ContainerStopOptions{}); err != nil {
		return fmt.Errorf("ipc/docker: stop %s: %w", name, err)
	}
	return nil
}

// Exec runs one command inside the running container and waits for it.
func (host *ContainerHost) Exec(ctx context.Context, name string, spec ExecSpec) (ExecResult, error) {
	created, err := host.ExecCreate(ctx, name, client.ExecCreateOptions{
		User: spec.User, Cmd: spec.Command, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return ExecResult{}, fmt.Errorf("ipc/docker: exec in %s: %w", name, err)
	}
	attached, err := host.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("ipc/docker: attach to exec in %s: %w", name, err)
	}
	defer attached.Close()
	retention := &boundedbufferv1.Retention{MaxBytes: host.outputBytes}
	stdout, err := boundedbuffer.New(retention)
	if err != nil {
		return ExecResult{}, fmt.Errorf("ipc/docker: output buffer: %w", err)
	}
	stderr, err := boundedbuffer.New(retention)
	if err != nil {
		return ExecResult{}, fmt.Errorf("ipc/docker: output buffer: %w", err)
	}
	if _, err := stdcopy.StdCopy(stdout, stderr, attached.Reader); err != nil {
		return ExecResult{}, fmt.Errorf("ipc/docker: read exec output in %s: %w", name, err)
	}
	inspected, err := host.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return ExecResult{}, fmt.Errorf("ipc/docker: inspect exec in %s: %w", name, err)
	}
	return ExecResult{ExitCode: inspected.ExitCode,
		Stdout: append([]byte(nil), stdout.Bytes()...), Stderr: append([]byte(nil), stderr.Bytes()...)}, nil
}

// create creates the container, pulling the image first when the Engine
// reports it missing.
func (host *ContainerHost) create(ctx context.Context, spec ServiceSpec) error {
	options, err := serviceCreateOptions(spec)
	if err != nil {
		return err
	}
	_, err = host.ContainerCreate(ctx, options)
	if cerrdefs.IsNotFound(err) {
		if err := host.pull(ctx, spec.Image); err != nil {
			return err
		}
		_, err = host.ContainerCreate(ctx, options)
	}
	if err != nil {
		return fmt.Errorf("ipc/docker: create %s: %w", spec.Name, err)
	}
	return nil
}

// serviceCreateOptions is spec as the Engine's create request: the port
// published on the host address only, restarted unless stopped.
func serviceCreateOptions(spec ServiceSpec) (client.ContainerCreateOptions, error) {
	port, err := network.ParsePort(spec.Port)
	if err != nil {
		return client.ContainerCreateOptions{}, fmt.Errorf("%w: port %q: %w", ErrInvalidOption, spec.Port, err)
	}
	mounts := make([]mount.Mount, 0, len(spec.Volumes)+len(spec.Mounts))
	for volume, target := range spec.Volumes {
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: volume, Target: target})
	}
	for _, bind := range spec.Mounts {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: bind.Source, Target: bind.Target, ReadOnly: bind.ReadOnly})
	}
	binding := network.PortBinding{HostIP: spec.HostAddress}
	if spec.HostPort != 0 {
		binding.HostPort = strconv.Itoa(int(spec.HostPort))
	}
	bindings := []network.PortBinding{binding}
	if len(spec.ExtraHostAddresses) > 0 && spec.HostPort == 0 {
		return client.ContainerCreateOptions{}, fmt.Errorf("%w: extra host addresses for %s need a fixed host port", ErrInvalidOption, spec.Name)
	}
	for _, address := range spec.ExtraHostAddresses {
		bindings = append(bindings, network.PortBinding{HostIP: address, HostPort: binding.HostPort})
	}
	var devices []container.DeviceRequest
	if spec.GPU {
		devices = []container.DeviceRequest{{Driver: gpuDriver, Count: allGPUs, Capabilities: [][]string{{gpuCapability}}}}
	}
	return client.ContainerCreateOptions{
		Name: spec.Name,
		Config: &container.Config{
			Image: spec.Image, Env: spec.Environment, Labels: spec.Labels, Cmd: spec.Command,
			ExposedPorts: network.PortSet{port: {}},
			Healthcheck: &container.HealthConfig{
				Test: append([]string{healthCommandShell}, spec.HealthCheck...), Interval: spec.HealthInterval,
			},
		},
		HostConfig: &container.HostConfig{
			Mounts:        mounts,
			PortBindings:  network.PortMap{port: bindings},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Resources:     container.Resources{DeviceRequests: devices},
		},
	}, nil
}

// pull fetches image and waits until the Engine has it.
func (host *ContainerHost) pull(ctx context.Context, image string) error {
	progress, err := host.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("ipc/docker: pull %s: %w", image, err)
	}
	defer func() { _ = progress.Close() }()
	if err := progress.Wait(ctx); err != nil {
		return fmt.Errorf("ipc/docker: pull %s: %w", image, err)
	}
	return nil
}

// serviceState reads the state, health and published port of a container.
func serviceState(inspected container.InspectResponse) ServiceState {
	state := ServiceState{ID: inspected.ID}
	if inspected.State != nil {
		state.Status, state.Running = string(inspected.State.Status), inspected.State.Running
		if inspected.State.Health != nil {
			state.Health = string(inspected.State.Health.Status)
		}
	}
	if inspected.NetworkSettings == nil {
		return state
	}
	for _, bindings := range inspected.NetworkSettings.Ports {
		for _, binding := range bindings {
			if port, err := strconv.ParseUint(binding.HostPort, 10, 16); err == nil {
				state.HostPort = uint16(port)
				return state
			}
		}
	}
	return state
}
