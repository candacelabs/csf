// Copyright 2026 Candace Labs

package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// The labels that mark a container as a job's own.
const (
	ContainerOwnerLabel = "candace.owner"
	ContainerJobLabel   = "candace.job-id"
)

const (
	dockerLogDriver      = "json-file"
	dockerLogMaxSize     = "10m"
	dockerLogMaxFiles    = "3"
	dockerLogTail        = "20000"
	dockerLogSizeOption  = "max-size"
	dockerLogFilesOption = "max-file"
)

// IDockerEngine consumes the upstream Docker Engine SDK, not a second
// orchestrator process. The binary owns the socket and the client lifetime;
// when ipc/docker lands it grants this capability.
type IDockerEngine interface {
	ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerLogs(ctx context.Context, id string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error)
}

// IEvidenceStore retains bytes by content and returns their SHA-256.
type IEvidenceStore interface {
	Put(content []byte) (hash string, reference string, err error)
}

// ContainerTemplate fills the job-specific part of a container: command,
// environment, mounts and devices. The executor sets the name, image,
// network, labels, restart policy and log bounds itself.
type ContainerTemplate[Spec any] func(job Job[Spec]) (config container.Config, host container.HostConfig)

// LocalDockerSettings are the operator's local Docker choices.
type LocalDockerSettings struct {
	// Owner is the owner label value that marks this executor's containers.
	Owner string
	// NamePrefix prefixes the job identity in the durable container name.
	NamePrefix string
	// StopGrace is how long a cancelled or timed-out container may stop.
	StopGrace time.Duration
	// LogBytes bounds the retained container log.
	LogBytes int64
}

// LocalDockerExecutor is the host executor over the Docker Engine. Each job
// has one container named by its identity, so a lost create reply is
// recovered by inspecting the name instead of creating again.
type LocalDockerExecutor[Spec any] struct {
	docker   IDockerEngine
	evidence IEvidenceStore
	settings LocalDockerSettings
	template ContainerTemplate[Spec]
}

// NewLocalDockerExecutor returns the executor over a Docker client the
// binary owns and the store that retains logs and inspection evidence.
func NewLocalDockerExecutor[Spec any](docker IDockerEngine, evidence IEvidenceStore, settings LocalDockerSettings, template ContainerTemplate[Spec]) (*LocalDockerExecutor[Spec], error) {
	if docker == nil || evidence == nil || template == nil || settings.Owner == "" || settings.NamePrefix == "" || settings.StopGrace < time.Second || settings.LogBytes < 1 {
		return nil, fmt.Errorf("%w: local Docker needs a client, an evidence store, a template, an owner, a name prefix, a stop grace and a log bound", ErrInvalidExecutor)
	}
	return &LocalDockerExecutor[Spec]{docker: docker, evidence: evidence, settings: settings, template: template}, nil
}

// ContainerName is the durable name of a job's container.
func (executor *LocalDockerExecutor[Spec]) ContainerName(job Job[Spec]) string {
	return executor.settings.NamePrefix + job.ID
}

// Reconcile creates, starts, observes, stops and removes the job's own
// container, one step per call. Cleanup is confirmed only after the logs
// and the final inspection are retained and the container is removed.
func (executor *LocalDockerExecutor[Spec]) Reconcile(ctx context.Context, job Job[Spec]) (Execution, error) {
	execution := job.Execution()
	name := executor.ContainerName(job)
	observed, err := executor.docker.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		if done, problem := executor.absent(ctx, job, &execution); done {
			return execution, problem
		}
		observed, err = executor.docker.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	}
	if err != nil {
		return execution, err
	}
	info := observed.Container
	if err := executor.owns(job, info); err != nil {
		return execution, err
	}
	execution.ExternalID = info.ID
	if info.State == nil {
		return execution, fmt.Errorf("Docker returned no container state")
	}
	created, _ := time.Parse(time.RFC3339Nano, info.Created)
	timedOut := !created.IsZero() && info.State.Status == container.StateCreated && time.Since(created) > job.Timeout
	if info.State.Status == container.StateCreated && !job.CancellationRequested && !timedOut {
		if _, err := executor.docker.ContainerStart(ctx, info.ID, client.ContainerStartOptions{}); err != nil {
			return execution, err
		}
		execution.State = StateQueued
		return execution, nil
	}
	if info.State.Running {
		started, err := time.Parse(time.RFC3339Nano, info.State.StartedAt)
		if err != nil {
			return execution, fmt.Errorf("Docker start timestamp invalid: %w", err)
		}
		timedOut = time.Since(started) > job.Timeout
		if !job.CancellationRequested && !timedOut {
			if !execution.State.Terminal() {
				execution.State = StateRunning
			}
			return execution, nil
		}
		if observed, err = executor.stop(ctx, job, info.ID); err != nil {
			return execution, err
		}
		info = observed.Container
	}
	if info.State.Status != container.StateExited && info.State.Status != container.StateDead && info.State.Status != container.StateCreated {
		return execution, fmt.Errorf("container is still %s", info.State.Status)
	}
	return executor.retire(ctx, job, execution, observed, timedOut)
}

// absent handles a job whose container does not exist. done is false when
// the container was just created and must be inspected again.
func (executor *LocalDockerExecutor[Spec]) absent(ctx context.Context, job Job[Spec], execution *Execution) (done bool, problem error) {
	switch {
	case job.LogStream != "" && job.State.Terminal():
		// The removal acknowledgement was lost after retention; it is gone.
		execution.CleanupConfirmed = true
		return true, nil
	case job.State != StatePending && job.State != StateSubmitting:
		execution.State, execution.Reason, execution.CleanupConfirmed = StateFailed, "owned container disappeared before terminal inspection", true
		return true, nil
	case job.State == StateSubmitting && time.Since(job.CreatedAt) > job.Timeout:
		execution.State, execution.Reason, execution.CleanupConfirmed = StateFailed, "container creation did not complete within the operator deadline", true
		return true, nil
	case job.CancellationRequested:
		execution.State, execution.CleanupConfirmed = StateCancelled, true
		return true, nil
	}
	execution.State, execution.ExternalID = StateSubmitting, executor.ContainerName(job)
	if _, err := executor.docker.ContainerCreate(ctx, executor.createOptions(job)); err != nil {
		if errdefs.IsNotFound(err) || errdefs.IsInvalidArgument(err) {
			execution.State, execution.Reason, execution.CleanupConfirmed = StateFailed, err.Error(), true
		}
		// The name stays durable: the next pass inspects before any retry.
		return true, err
	}
	return false, nil
}

// stop stops a running container and requires inspection evidence that it
// stopped; a stop acknowledgement alone is not cleanup evidence.
func (executor *LocalDockerExecutor[Spec]) stop(ctx context.Context, job Job[Spec], id string) (client.ContainerInspectResult, error) {
	grace := int(executor.settings.StopGrace.Seconds())
	if _, err := executor.docker.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &grace}); err != nil {
		return client.ContainerInspectResult{}, err
	}
	confirmed, err := executor.docker.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return confirmed, err
	}
	if err := executor.owns(job, confirmed.Container); err != nil {
		return confirmed, err
	}
	if confirmed.Container.State == nil || confirmed.Container.State.Running {
		return confirmed, fmt.Errorf("container has not stopped")
	}
	return confirmed, nil
}

// retire retains the logs and final inspection, settles the terminal state
// and removes only this job's container. Named volumes survive removal.
func (executor *LocalDockerExecutor[Spec]) retire(ctx context.Context, job Job[Spec], execution Execution, observed client.ContainerInspectResult, timedOut bool) (Execution, error) {
	info := observed.Container
	logs, err := executor.read(ctx, info.ID)
	if err != nil {
		return execution, fmt.Errorf("retain container logs before cleanup: %w", err)
	}
	hash, _, err := executor.evidence.Put(logs)
	if err != nil {
		return execution, err
	}
	execution.LogStream = hash
	if _, _, err := executor.evidence.Put(observed.Raw); err != nil {
		return execution, err
	}
	switch {
	case timedOut:
		execution.State, execution.Reason = StateFailed, "local container exceeded the operator wall-time limit"
	case job.CancellationRequested && execution.State != StateSucceeded && execution.State != StateFailed:
		execution.State, execution.Reason = StateCancelled, "owned local container stopped and removed"
	case info.State.ExitCode != 0:
		execution.State, execution.Reason = StateFailed, fmt.Sprintf("local container exited with code %d", info.State.ExitCode)
	case execution.State == StateFailed:
		// Preserve the worker's specific failure.
	case execution.State != StateSucceeded || job.CompletedUnits != job.TotalUnits:
		execution.State, execution.Reason = StateFailed, "container exited without a complete successful worker report"
	}
	if _, err := executor.docker.ContainerRemove(ctx, info.ID, client.ContainerRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
		return execution, err
	}
	execution.CleanupConfirmed = true
	return execution, nil
}

// Logs reads a live job container's logs after checking it is the job's own.
func (executor *LocalDockerExecutor[Spec]) Logs(ctx context.Context, job Job[Spec]) ([]byte, error) {
	observed, err := executor.docker.ContainerInspect(ctx, job.ExternalID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}
	if err := executor.owns(job, observed.Container); err != nil {
		return nil, err
	}
	return executor.read(ctx, job.ExternalID)
}

func (executor *LocalDockerExecutor[Spec]) owns(job Job[Spec], info container.InspectResponse) error {
	if info.Config == nil || info.Config.Labels[ContainerOwnerLabel] != executor.settings.Owner || info.Config.Labels[ContainerJobLabel] != job.ID || info.Image != job.Image {
		return ErrForeignResource
	}
	return nil
}

func (executor *LocalDockerExecutor[Spec]) createOptions(job Job[Spec]) client.ContainerCreateOptions {
	config, host := executor.template(job)
	config.Image = job.Image
	labels := map[string]string{}
	maps.Copy(labels, config.Labels)
	labels[ContainerOwnerLabel], labels[ContainerJobLabel] = executor.settings.Owner, job.ID
	config.Labels = labels
	host.NetworkMode = container.NetworkMode(job.Target)
	host.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	host.LogConfig = container.LogConfig{Type: dockerLogDriver, Config: map[string]string{dockerLogSizeOption: dockerLogMaxSize, dockerLogFilesOption: dockerLogMaxFiles}}
	return client.ContainerCreateOptions{Name: executor.ContainerName(job), Config: &config, HostConfig: &host}
}

func (executor *LocalDockerExecutor[Spec]) read(ctx context.Context, id string) ([]byte, error) {
	stream, err := executor.docker.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: dockerLogTail})
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	// SDK demultiplexing preserves output text; Docker also bounds retained
	// log files. The reader limit bounds an unexpectedly large response.
	var output bytes.Buffer
	_, err = stdcopy.StdCopy(&output, &output, io.LimitReader(stream, executor.settings.LogBytes))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return output.Bytes(), nil
}
