// Copyright 2026 Candace Labs

// Package docker is the container capability: the Docker Engine reached over
// its API socket, granted to the code that runs containers.
//
// It wraps the upstream Docker Engine SDK client (github.com/moby/moby/client)
// rather than the docker CLI run through ipc/proc. The SDK is already the
// client CSF's local simulations consume, it is typed end to end (no parsing
// of CLI output), and it gives the owner of a container its whole lifecycle —
// create, wait, start, kill, logs, remove — as separate calls, which is what
// cleanup on cancellation needs. Talking to the Engine socket is a kernel I/O
// crossing, so the client is constructed here, under ipc, and nowhere else.
//
// A binary constructs one [ContainerHost] and passes it to whatever runs
// containers. Consumers that need only part of the Engine API declare their
// own narrow interface, which ContainerHost satisfies; [ContainerHost.RunSandboxed]
// is the bounded, isolated run-to-completion that sandboxed evaluation and the
// node executor are built on; [ContainerHost.EnsureService] keeps one
// long-lived named container up, such as the database CSF owns, and
// [ContainerHost.Exec] runs a command inside it.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/candacelabs/csf/io/kernel/sandbox"
	"github.com/candacelabs/csf/pkg/boundedbuffer"
	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"
)

const (
	// DefaultOutputBytes bounds each captured output stream of a sandboxed run.
	DefaultOutputBytes = 1 << 20
	// DefaultPidsLimit bounds the processes a sandboxed container may create.
	DefaultPidsLimit = 256
	// RemoveTimeout bounds the cleanup that removes a finished or canceled
	// sandbox, which runs even after the caller's context has ended.
	RemoveTimeout = 30 * time.Second

	sandboxNamePrefix   = "csf-sandbox-"
	networkNone         = "none"
	dropAllCapabilities = "ALL"
	noNewPrivileges     = "no-new-privileges"
	scratchDirectory    = "/tmp"
	scratchOptions      = "rw,noexec,nosuid,size=64m"
)

var (
	// ErrImageRequired is returned for a sandbox spec without an image.
	ErrImageRequired = errors.New("ipc/docker: sandbox image is required")
	// ErrInvalidOption is returned by NewContainerHost for a nil, empty or
	// conflicting option.
	ErrInvalidOption = errors.New("ipc/docker: invalid container host option")
)

// IContainerAPI is the part of the Docker Engine API client this capability
// uses and hands on. *client.Client satisfies it; a test substitutes a double.
type IContainerAPI interface {
	ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerKill(ctx context.Context, id string, options client.ContainerKillOptions) (client.ContainerKillResult, error)
	ContainerPause(ctx context.Context, id string, options client.ContainerPauseOptions) (client.ContainerPauseResult, error)
	ContainerUnpause(ctx context.Context, id string, options client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error)
	ContainerStats(ctx context.Context, id string, options client.ContainerStatsOptions) (client.ContainerStatsResult, error)
	ContainerWait(ctx context.Context, id string, options client.ContainerWaitOptions) client.ContainerWaitResult
	ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerLogs(ctx context.Context, id string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error)
	ImagePrune(ctx context.Context, options client.ImagePruneOptions) (client.ImagePruneResult, error)
	BuildCachePrune(ctx context.Context, options client.BuildCachePruneOptions) (client.BuildCachePruneResult, error)
	DiskUsage(ctx context.Context, options client.DiskUsageOptions) (client.DiskUsageResult, error)
	ImagePull(ctx context.Context, reference string, options client.ImagePullOptions) (client.ImagePullResponse, error)
	ExecCreate(ctx context.Context, containerID string, options client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)
	Close() error
}

// ContainerHost is the Docker Engine granted as a capability. Its Engine API
// methods are promoted from the client it wraps, so it satisfies a consumer's
// narrow interface directly.
type ContainerHost struct {
	IContainerAPI
	outputBytes int64
}

// ContainerHostOption configures a [ContainerHost].
type ContainerHostOption func(settings *containerHostSettings) error

type containerHostSettings struct {
	host        string
	api         IContainerAPI
	outputBytes int64
}

// WithDockerHost names the Engine endpoint, such as unix:///var/run/docker.sock.
// The default is the SDK's platform default; the environment is never read.
func WithDockerHost(host string) ContainerHostOption {
	return func(settings *containerHostSettings) error {
		settings.host = host
		return nil
	}
}

// WithContainerAPI supplies an already-constructed Engine client, which the
// host then owns and closes.
func WithContainerAPI(api IContainerAPI) ContainerHostOption {
	return func(settings *containerHostSettings) error {
		if api == nil {
			return fmt.Errorf("%w: nil container API", ErrInvalidOption)
		}
		settings.api = api
		return nil
	}
}

// WithOutputBytes bounds each captured output stream of a sandboxed run.
func WithOutputBytes(limit int64) ContainerHostOption {
	return func(settings *containerHostSettings) error {
		if limit <= 0 {
			return fmt.Errorf("%w: output limit must be positive, got %d", ErrInvalidOption, limit)
		}
		settings.outputBytes = limit
		return nil
	}
}

// NewContainerHost connects the container capability. Construction does not
// contact the Engine; the first call does.
func NewContainerHost(options ...ContainerHostOption) (*ContainerHost, error) {
	settings := containerHostSettings{outputBytes: DefaultOutputBytes}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(&settings); err != nil {
			return nil, err
		}
	}
	if settings.api != nil && settings.host != "" {
		return nil, fmt.Errorf("%w: WithDockerHost and WithContainerAPI are exclusive", ErrInvalidOption)
	}
	if settings.api == nil {
		clientOptions := []client.Opt{client.WithAPIVersionNegotiation()}
		if settings.host != "" {
			clientOptions = append(clientOptions, client.WithHost(settings.host))
		}
		engine, err := client.New(clientOptions...)
		if err != nil {
			return nil, fmt.Errorf("ipc/docker: connect: %w", err)
		}
		settings.api = engine
	}
	return &ContainerHost{IContainerAPI: settings.api, outputBytes: settings.outputBytes}, nil
}

// Mount is one host path visible inside a sandbox.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// SandboxSpec is one isolated run to completion. Unset limits fall back to
// the sandbox defaults: no network, every capability dropped, no privilege
// escalation, a read-only root filesystem with a small /tmp, an init process,
// and [DefaultPidsLimit].
type SandboxSpec struct {
	Image            string
	Command          []string
	Environment      []string
	WorkingDirectory string
	User             string
	Mounts           []Mount
	Labels           map[string]string
	// Network is a Docker network mode; empty means no network at all.
	Network string
	// MemoryBytes and NanoCPUs are resource limits; zero means unlimited.
	MemoryBytes int64
	NanoCPUs    int64
	// PidsLimit bounds process creation; zero means [DefaultPidsLimit].
	PidsLimit int64
	// WritableRoot leaves the root filesystem writable.
	WritableRoot bool
}

// SandboxResult is what a finished sandbox left behind.
type SandboxResult struct {
	ContainerID     string
	ExitCode        int64
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

// ExitError reports a sandbox whose command exited unsuccessfully.
type ExitError struct {
	ContainerID string
	Code        int64
}

// Error names the container and its status.
func (exitError *ExitError) Error() string {
	return fmt.Sprintf("ipc/docker: sandbox %s exited with status %d", exitError.ContainerID, exitError.Code)
}

// RunSandboxed creates the container, starts it, waits for it to exit and
// collects its output, then removes it. The container is removed on every
// path — success, failure, and cancellation, which kills it first — so a
// sandbox never outlives the call. A nonzero exit returns the result together
// with an [*ExitError].
func (host *ContainerHost) RunSandboxed(ctx context.Context, spec SandboxSpec) (result SandboxResult, err error) {
	if spec.Image == "" {
		return SandboxResult{}, ErrImageRequired
	}
	created, err := host.ContainerCreate(ctx, sandboxCreateOptions(spec))
	if err != nil {
		return SandboxResult{}, fmt.Errorf("ipc/docker: create sandbox: %w", err)
	}
	result.ContainerID = created.ID
	defer func() { err = errors.Join(err, host.remove(ctx, created.ID)) }()

	// Wait is registered before Start so an exit cannot be missed.
	waiting := host.ContainerWait(ctx, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNextExit})
	if _, err := host.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return result, fmt.Errorf("ipc/docker: start sandbox %s: %w", created.ID, err)
	}
	select {
	case status := <-waiting.Result:
		if status.Error != nil {
			return result, fmt.Errorf("ipc/docker: wait for sandbox %s: %s", created.ID, status.Error.Message)
		}
		result.ExitCode = status.StatusCode
	case waitErr := <-waiting.Error:
		if ctx.Err() != nil {
			return result, fmt.Errorf("ipc/docker: sandbox %s: %w", created.ID, context.Cause(ctx))
		}
		return result, fmt.Errorf("ipc/docker: wait for sandbox %s: %w", created.ID, waitErr)
	}
	if err := host.collect(ctx, created.ID, &result); err != nil {
		return result, err
	}
	if result.ExitCode != 0 {
		return result, &ExitError{ContainerID: created.ID, Code: result.ExitCode}
	}
	return result, nil
}

// StartedContainer is a container [ContainerHost.StartDetached] left running.
type StartedContainer struct {
	ID string
	// Pid is the host PID of the container's init process.
	Pid int
}

// StartDetached creates and starts a long-lived container under the sandbox
// defaults and returns once it runs; the caller owns it and ends it with
// [ContainerHost.RemoveContainer]. A container that fails to start is removed.
func (host *ContainerHost) StartDetached(ctx context.Context, spec SandboxSpec) (StartedContainer, error) {
	if spec.Image == "" {
		return StartedContainer{}, ErrImageRequired
	}
	created, err := host.ContainerCreate(ctx, sandboxCreateOptions(spec))
	if err != nil {
		return StartedContainer{}, fmt.Errorf("ipc/docker: create container: %w", err)
	}
	if _, err := host.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return StartedContainer{}, errors.Join(fmt.Errorf("ipc/docker: start container %s: %w", created.ID, err), host.remove(ctx, created.ID))
	}
	inspected, err := host.ContainerInspect(ctx, created.ID, client.ContainerInspectOptions{})
	if err != nil {
		return StartedContainer{}, errors.Join(fmt.Errorf("ipc/docker: inspect container %s: %w", created.ID, err), host.remove(ctx, created.ID))
	}
	started := StartedContainer{ID: created.ID}
	if inspected.Container.State != nil {
		started.Pid = inspected.Container.State.Pid
	}
	return started, nil
}

// ContainerUsage reads the running container's cpu and memory receipt from
// its cgroup, through its init process.
func (host *ContainerHost) ContainerUsage(started StartedContainer) (sandbox.Usage, error) {
	return sandbox.ProcessCgroupUsage(started.Pid)
}

// ContainerSample is one reading of a running container's usage: cumulative
// cpu time beside the host's, so two readings give a cpu share, and the
// memory it holds now.
type ContainerSample struct {
	// CPUNanoseconds is the container's cumulative cpu time.
	CPUNanoseconds uint64
	// SystemNanoseconds is the host's cumulative cpu time across every cpu.
	SystemNanoseconds uint64
	// OnlineCPUs is how many cpus the host had online at the reading.
	OnlineCPUs uint32
	// MemoryBytes is the memory the container's cgroup charges.
	MemoryBytes uint64
}

// SampleContainer takes one usage reading of a running container, at once
// rather than after the Engine's one-second prior sample.
func (host *ContainerHost) SampleContainer(ctx context.Context, id string) (ContainerSample, error) {
	stats, err := host.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return ContainerSample{}, fmt.Errorf("ipc/docker: sample container %s: %w", id, err)
	}
	defer func() { _ = stats.Body.Close() }()
	var response container.StatsResponse
	if err := json.NewDecoder(stats.Body).Decode(&response); err != nil {
		return ContainerSample{}, fmt.Errorf("ipc/docker: decode container %s sample: %w", id, err)
	}
	return ContainerSample{
		CPUNanoseconds:    response.CPUStats.CPUUsage.TotalUsage,
		SystemNanoseconds: response.CPUStats.SystemUsage,
		OnlineCPUs:        response.CPUStats.OnlineCPUs,
		MemoryBytes:       response.MemoryStats.Usage,
	}, nil
}

// RemoveContainer force-removes the container, killing every process in it.
// It outlives the caller's context, bounded by [RemoveTimeout].
func (host *ContainerHost) RemoveContainer(ctx context.Context, id string) error {
	return host.remove(ctx, id)
}

// collect reads the finished container's two output streams, each bounded.
func (host *ContainerHost) collect(ctx context.Context, id string, result *SandboxResult) error {
	retention := &boundedbufferv1.Retention{MaxBytes: host.outputBytes}
	stdout, err := boundedbuffer.New(retention)
	if err != nil {
		return fmt.Errorf("ipc/docker: output buffer: %w", err)
	}
	stderr, err := boundedbuffer.New(retention)
	if err != nil {
		return fmt.Errorf("ipc/docker: output buffer: %w", err)
	}
	logs, err := host.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return fmt.Errorf("ipc/docker: read sandbox %s output: %w", id, err)
	}
	defer func() { _ = logs.Close() }()
	if _, err := stdcopy.StdCopy(stdout, stderr, logs); err != nil {
		return fmt.Errorf("ipc/docker: demultiplex sandbox %s output: %w", id, err)
	}
	result.Stdout, result.StdoutTruncated = append([]byte(nil), stdout.Bytes()...), stdout.Truncated()
	result.Stderr, result.StderrTruncated = append([]byte(nil), stderr.Bytes()...), stderr.Truncated()
	return nil
}

// remove force-removes the sandbox, killing it if it still runs. It outlives
// the caller's context, bounded by [RemoveTimeout].
func (host *ContainerHost) remove(ctx context.Context, id string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), RemoveTimeout)
	defer cancel()
	if _, err := host.ContainerRemove(cleanup, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
		return fmt.Errorf("ipc/docker: remove sandbox %s: %w", id, err)
	}
	return nil
}

// sandboxCreateOptions applies the sandbox defaults to spec.
func sandboxCreateOptions(spec SandboxSpec) client.ContainerCreateOptions {
	network := spec.Network
	if network == "" {
		network = networkNone
	}
	pids := spec.PidsLimit
	if pids == 0 {
		pids = DefaultPidsLimit
	}
	mounts := make([]mount.Mount, 0, len(spec.Mounts))
	for _, bind := range spec.Mounts {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: bind.Source, Target: bind.Target, ReadOnly: bind.ReadOnly})
	}
	initProcess := true
	hostConfig := &container.HostConfig{
		NetworkMode:    container.NetworkMode(network),
		CapDrop:        []string{dropAllCapabilities},
		SecurityOpt:    []string{noNewPrivileges},
		ReadonlyRootfs: !spec.WritableRoot,
		Init:           &initProcess,
		Mounts:         mounts,
		Resources:      container.Resources{Memory: spec.MemoryBytes, NanoCPUs: spec.NanoCPUs, PidsLimit: &pids},
	}
	if !spec.WritableRoot {
		hostConfig.Tmpfs = map[string]string{scratchDirectory: scratchOptions}
	}
	return client.ContainerCreateOptions{
		Name: sandboxNamePrefix + uuid.NewString(),
		Config: &container.Config{
			Image: spec.Image, Cmd: spec.Command, Env: spec.Environment,
			WorkingDir: spec.WorkingDirectory, User: spec.User, Labels: spec.Labels,
		},
		HostConfig: hostConfig,
	}
}
