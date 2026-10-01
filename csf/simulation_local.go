package csf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/containerd/errdefs"
	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"google.golang.org/protobuf/proto"
)

const localSimulationOwner = "candace-brain-simulator"
const localSimulationName = "csf-simulator-"
const localLogBytes = 1024 * 1024

// IDockerSimulations consumes the upstream Docker Engine SDK, not a second
// orchestrator process. The application owns the socket and client lifetime.
type IDockerSimulations interface {
	ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerLogs(ctx context.Context, id string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error)
}

type SimulationOption func(simulations *Simulations)

// LocalSimulations is an optional capability of the shared host. It never owns
// a listener or a goroutine; Simulations.Work drives its bounded reconciliation.
type LocalSimulations struct {
	docker IDockerSimulations
	config *pb.LocalSimulationConfig
}

func NewLocalSimulations(docker IDockerSimulations, config *pb.LocalSimulationConfig) (*LocalSimulations, error) {
	if docker == nil || config == nil {
		return nil, fmt.Errorf("local simulations require Docker and operator configuration")
	}
	if err := pb.ValidateLocalSimulationConfig(config); err != nil {
		return nil, err
	}
	if config.LogIndex != "" && !searchIndexName.MatchString(config.LogIndex) {
		return nil, fmt.Errorf("invalid simulation log index")
	}
	target, err := url.Parse(config.ProgressUrl)
	if err != nil || target.Host == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("local progress_url must be an HTTP URL without credentials")
	}
	if config.Network == "" || config.Network == "host" {
		return nil, fmt.Errorf("local simulations require an explicit container network")
	}
	seen := map[pb.Simulator]bool{}
	for _, profile := range config.Profiles {
		if profile == nil || seen[profile.Simulator] || (profile.Simulator != pb.Simulator_SIMULATOR_CARLA && profile.Simulator != pb.Simulator_SIMULATOR_ISAAC) {
			return nil, fmt.Errorf("one profile per known simulator required")
		}
		digest := strings.TrimPrefix(profile.Image, "sha256:")
		decoded, decodeErr := hex.DecodeString(digest)
		if decodeErr != nil || len(decoded) != sha256.Size || !strings.HasPrefix(profile.Image, "sha256:") {
			return nil, fmt.Errorf("local image must be an immutable sha256 image ID")
		}
		if profile.ArtifactVolume == "" || strings.ContainsAny(profile.ArtifactVolume, "/\\") || !filepath.IsAbs(profile.ArtifactDirectory) {
			return nil, fmt.Errorf("named artifact volume and absolute mounted directory required")
		}
		target, err := url.Parse(profile.ArtifactUrl)
		if err != nil || target.Host == "" || target.User != nil || (target.Scheme != "http" && target.Scheme != "https") || target.RawQuery != "" || target.Fragment != "" {
			return nil, fmt.Errorf("artifact_url must be an HTTP prefix")
		}
		seen[profile.Simulator] = true
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("local profiles required")
	}
	return &LocalSimulations{docker: docker, config: proto.CloneOf(config)}, nil
}

func WithLocalSimulations(local *LocalSimulations) SimulationOption {
	return func(simulations *Simulations) { simulations.local = local }
}

func (local *LocalSimulations) profile(simulator pb.Simulator) *pb.LocalSimulationProfile {
	for _, profile := range local.config.Profiles {
		if profile.Simulator == simulator {
			return profile
		}
	}
	return nil
}

// A transaction-scoped lock serializes host recovery even during overlapping
// deploys. Persistent names make create retries idempotent at the Docker boundary.
func (simulations *Simulations) tickLocal(ctx context.Context) error {
	deadline, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	tx, err := simulations.store.pool.Begin(deadline)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := simulations.store.queries.WithTx(tx)
	acquired, err := queries.LockLocalSimulationWorker(deadline)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	row, err := queries.NextLocalSimulation(deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	problem := simulations.reconcileLocal(deadline, &row)
	if err := queries.UpdateLocalSimulation(deadline, db.UpdateLocalSimulationParams{RunID: row.RunID, State: row.State, JobID: row.JobID, Reason: row.Reason, CleanupConfirmed: row.CleanupConfirmed, LogStream: row.LogStream}); err != nil {
		return err
	}
	if problem != nil {
		if err := queries.SetSimulationInspectionError(deadline, db.SetSimulationInspectionErrorParams{RunID: row.RunID, InspectionError: problem.Error()}); err != nil {
			return err
		}
	}
	return tx.Commit(deadline)
}

func (simulations *Simulations) reconcileLocal(ctx context.Context, row *db.BrainspineSimulation) error {
	local := simulations.local
	name := localSimulationName + row.RunID
	observed, err := local.docker.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if errdefs.IsNotFound(err) {
		if row.LogStream != "" && simulationTerminal(row.State) {
			row.CleanupConfirmed = true
			return nil
		}
		if row.State != db.BrainspineSimulationStatePending && row.State != db.BrainspineSimulationStateSubmitting {
			row.State, row.Reason, row.CleanupConfirmed = db.BrainspineSimulationStateFailed, "owned container disappeared before terminal inspection", true
			return nil
		}
		if row.State == db.BrainspineSimulationStateSubmitting && time.Since(row.CreatedAt.Time) > time.Duration(row.TimeoutSeconds)*time.Second {
			row.State, row.Reason, row.CleanupConfirmed = db.BrainspineSimulationStateFailed, "container creation did not complete within the operator deadline", true
			return nil
		}
		if row.CancellationRequested {
			row.State, row.CleanupConfirmed = db.BrainspineSimulationStateCancelled, true
			return nil
		}
		row.State, row.JobID = db.BrainspineSimulationStateSubmitting, name
		_, err = local.docker.ContainerCreate(ctx, local.createOptions(*row))
		if err != nil {
			if errdefs.IsNotFound(err) || errdefs.IsInvalidArgument(err) {
				row.State, row.Reason, row.CleanupConfirmed = db.BrainspineSimulationStateFailed, err.Error(), true
			}
			return err
		} // Name remains durable; inspect before any retry.
		observed, err = local.docker.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	}
	if err != nil {
		return err
	}
	info := observed.Container
	if err := ownsLocalContainer(*row, info); err != nil {
		return err
	}
	row.JobID = info.ID
	if info.State == nil {
		return fmt.Errorf("Docker returned no container state")
	}
	created, _ := time.Parse(time.RFC3339Nano, info.Created)
	timedOut := !created.IsZero() && info.State.Status == container.StateCreated && time.Since(created) > time.Duration(row.TimeoutSeconds)*time.Second
	if info.State.Status == container.StateCreated && !row.CancellationRequested && !timedOut {
		_, err = local.docker.ContainerStart(ctx, info.ID, client.ContainerStartOptions{})
		if err != nil {
			return err
		}
		row.State = db.BrainspineSimulationStateQueued
		return nil
	}
	if info.State.Running {
		started, err := time.Parse(time.RFC3339Nano, info.State.StartedAt)
		if err != nil {
			return fmt.Errorf("Docker start timestamp invalid: %w", err)
		}
		timedOut = time.Since(started) > time.Duration(row.TimeoutSeconds)*time.Second
		if !row.CancellationRequested && !timedOut {
			if !simulationTerminal(row.State) {
				row.State = db.BrainspineSimulationStateRunning
			}
			return nil
		}
		grace := 10
		if _, err := local.docker.ContainerStop(ctx, info.ID, client.ContainerStopOptions{Timeout: &grace}); err != nil {
			return err
		}
		// A stop acknowledgement alone is insufficient cleanup evidence.
		confirmed, err := local.docker.ContainerInspect(ctx, info.ID, client.ContainerInspectOptions{})
		if err != nil {
			return err
		}
		observed = confirmed
		info = confirmed.Container
		if err := ownsLocalContainer(*row, info); err != nil {
			return err
		}
		if info.State == nil || info.State.Running {
			return fmt.Errorf("container has not stopped")
		}
	}
	if info.State.Status != container.StateExited && info.State.Status != container.StateDead && info.State.Status != container.StateCreated {
		return fmt.Errorf("container is still %s", info.State.Status)
	}
	logs, err := local.logs(ctx, info.ID)
	if err != nil {
		return fmt.Errorf("retain container logs before cleanup: %w", err)
	}
	hash, _, err := simulations.artifacts.Put(logs)
	if err != nil {
		return err
	}
	row.LogStream = hash
	if _, _, err := simulations.artifacts.Put(observed.Raw); err != nil {
		return err
	}
	// Retain artifacts and logs before removing only this owned container. Named
	// volumes survive removal and are still readable through the Workbench host.
	switch {
	case timedOut:
		row.State, row.Reason = db.BrainspineSimulationStateFailed, "local container exceeded the operator wall-time limit"
	case row.CancellationRequested && row.State != db.BrainspineSimulationStateSucceeded && row.State != db.BrainspineSimulationStateFailed:
		row.State, row.Reason = db.BrainspineSimulationStateCancelled, "owned local container stopped and removed"
	case info.State.ExitCode != 0:
		row.State, row.Reason = db.BrainspineSimulationStateFailed, fmt.Sprintf("local container exited with code %d", info.State.ExitCode)
	case row.State == db.BrainspineSimulationStateFailed:
		// Preserve the worker's specific failure.
	case row.State != db.BrainspineSimulationStateSucceeded || row.CompletedSteps != row.Steps:
		row.State, row.Reason = db.BrainspineSimulationStateFailed, "container exited without a complete successful worker report"
	}
	if _, err := local.docker.ContainerRemove(ctx, info.ID, client.ContainerRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	row.CleanupConfirmed = true
	return nil
}

func ownsLocalContainer(row db.BrainspineSimulation, info container.InspectResponse) error {
	if info.Config == nil || info.Config.Labels["candace.owner"] != localSimulationOwner || info.Config.Labels["candace.run-id"] != row.RunID || info.Image != row.JobDefinition {
		return fmt.Errorf("container ownership or admitted image mismatch")
	}
	return nil
}

func (local *LocalSimulations) createOptions(row db.BrainspineSimulation) client.ContainerCreateOptions {
	environment := []string{"CSF_RUN_ID=" + row.RunID, "CSF_STEPS=" + strconv.Itoa(int(row.Steps)), "CSF_OUTPUT=/artifacts/" + row.RunID}
	if row.Simulator == int32(pb.Simulator_SIMULATOR_ISAAC) {
		environment = append(environment, "ACCEPT_EULA=Y")
	}
	command := []string{"--progress-url", local.config.ProgressUrl, "--max-wall-seconds", strconv.Itoa(int(row.TimeoutSeconds)), "--capture-every", strconv.Itoa(int(row.CaptureEvery))}
	if row.Simulator == int32(pb.Simulator_SIMULATOR_CARLA) {
		command = append(command, "--timeout-seconds", "10")
	}
	return client.ContainerCreateOptions{Name: localSimulationName + row.RunID,
		Config: &container.Config{Image: row.JobDefinition, Env: environment, Cmd: command, Labels: map[string]string{"candace.owner": localSimulationOwner, "candace.run-id": row.RunID}},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(row.JobQueue), RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			Resources: container.Resources{DeviceRequests: []container.DeviceRequest{{Driver: "nvidia", Count: -1, Capabilities: [][]string{{"gpu"}}}}},
			Mounts:    []mount.Mount{{Type: mount.TypeVolume, Source: row.ArtifactVolume, Target: "/artifacts"}},
			LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
	}
}

func (local *LocalSimulations) logs(ctx context.Context, id string) ([]byte, error) {
	stream, err := local.docker.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: "20000"})
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	// SDK demultiplexing preserves output text; Docker also bounds retained log
	// files. The reader limit bounds even an unexpectedly large provider response.
	var output bytes.Buffer
	_, err = stdcopy.StdCopy(&output, &output, io.LimitReader(stream, localLogBytes))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return output.Bytes(), nil
}

func (service *Service) ReadSimulationLogs(ctx context.Context, request *pb.ReadSimulationLogsRequest) (*pb.ReadSimulationLogsResponse, error) {
	if service.simulations == nil || request == nil {
		return nil, fmt.Errorf("simulation logs unavailable")
	}
	if err := pb.ValidateReadSimulationLogsRequest(request); err != nil {
		return nil, err
	}
	simulations := service.simulations
	row, err := simulations.store.queries.GetSimulation(ctx, request.RunId)
	if err != nil {
		return nil, err
	}
	if !row.Managed {
		return nil, fmt.Errorf("logs operation requires a host-managed local run")
	}
	var content []byte
	if row.LogStream != "" {
		content, err = simulations.artifacts.Get(row.LogStream)
	} else if simulations.local != nil && row.JobID != "" {
		observed, inspectErr := simulations.local.docker.ContainerInspect(ctx, row.JobID, client.ContainerInspectOptions{})
		if inspectErr != nil {
			return nil, inspectErr
		}
		if err := ownsLocalContainer(row, observed.Container); err != nil {
			return nil, err
		}
		content, err = simulations.local.logs(ctx, row.JobID)
	} else {
		return nil, fmt.Errorf("container logs not available yet")
	}
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(content)
	result := &pb.ReadSimulationLogsResponse{Sha256: hex.EncodeToString(hash[:]), Truncated: len(content) > int(request.MaxBytes)}
	if result.Truncated {
		content = content[len(content)-int(request.MaxBytes):]
	}
	result.Content = strings.ToValidUTF8(string(content), "�")
	return result, nil
}
