package csf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/jobs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const localSimulationOwner = "candace-brain-simulator"
const localSimulationName = "csf-simulator-"
const localLogBytes = 1024 * 1024
const localStopGrace = 10 * time.Second
const localArtifactMount = "/artifacts"
const localGPUDriver = "nvidia"
const localGPUCapability = "gpu"
const localIsaacEULA = "ACCEPT_EULA=Y"
const localCARLAConnectSeconds = "10"

type SimulationOption func(simulations *Simulations)

// LocalSimulations is an optional capability of the shared host: the
// operator's local simulator profiles and the Docker client the binary
// owns. It never owns a listener or a goroutine; the job ledger's local
// Docker executor drives each container from Simulations.Work.
type LocalSimulations struct {
	docker jobs.IDockerEngine
	config *pb.LocalSimulationConfig
}

func NewLocalSimulations(docker jobs.IDockerEngine, config *pb.LocalSimulationConfig) (*LocalSimulations, error) {
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
		if profile == nil || seen[profile.Simulator] || simulationKinds[profile.Simulator] == "" {
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

// container is the simulator-specific part of a job's container; the
// executor adds the durable name, image, network, labels and log bounds.
func (local *LocalSimulations) container(job SimulationJob) (container.Config, container.HostConfig) {
	simulator := simulatorOf(job.Kind)
	steps := strconv.FormatInt(job.TotalUnits, 10)
	environment := []string{simulationRunEnvironment + "=" + job.ID, simulationStepsEnvironment + "=" + steps, "CSF_OUTPUT=" + localArtifactMount + "/" + job.ID}
	if simulator == pb.Simulator_SIMULATOR_ISAAC {
		environment = append(environment, localIsaacEULA)
	}
	command := []string{"--progress-url", local.config.ProgressUrl, "--max-wall-seconds", strconv.Itoa(int(job.Timeout.Seconds())), "--capture-every", strconv.FormatUint(uint64(job.Spec.CaptureEvery), 10)}
	if simulator == pb.Simulator_SIMULATOR_CARLA {
		command = append(command, "--timeout-seconds", localCARLAConnectSeconds)
	}
	return container.Config{Env: environment, Cmd: command},
		container.HostConfig{
			Resources: container.Resources{DeviceRequests: []container.DeviceRequest{{Driver: localGPUDriver, Count: -1, Capabilities: [][]string{{localGPUCapability}}}}},
			Mounts:    []mount.Mount{{Type: mount.TypeVolume, Source: job.Spec.ArtifactVolume, Target: localArtifactMount}},
		}
}

func (service *Service) ReadSimulationLogs(ctx context.Context, request *pb.ReadSimulationLogsRequest) (*pb.ReadSimulationLogsResponse, error) {
	if service.simulations == nil || request == nil {
		return nil, fmt.Errorf("simulation logs unavailable")
	}
	if err := pb.ValidateReadSimulationLogsRequest(request); err != nil {
		return nil, err
	}
	simulations := service.simulations
	job, err := simulations.ledger.Get(ctx, request.RunId)
	if err != nil {
		return nil, err
	}
	if !job.Managed || job.Executor != simulationExecutorLocal {
		return nil, fmt.Errorf("logs operation requires a host-managed local run")
	}
	var content []byte
	switch {
	case job.LogStream != "":
		content, err = simulations.artifacts.Get(job.LogStream)
	case simulations.docker != nil && job.ExternalID != "":
		content, err = simulations.docker.Logs(ctx, job)
	default:
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

func (local *LocalSimulations) trace(job SimulationJob) (*tracepb.ResourceSpans, string, error) {
	source, err := local.traceSource(job)
	if err != nil {
		return nil, "", err
	}
	return local.projectTrace(source)
}

// traceSource retains the exact vendor text used to derive stable trace IDs.
// Camera byte hashes are verified here; replay retains their observed references.
func (local *LocalSimulations) traceSource(job SimulationJob) (*pb.SimulationTraceSource, error) {
	profile := local.profile(simulatorOf(job.Kind))
	if profile == nil || !simulationID.MatchString(job.ID) {
		return nil, fmt.Errorf("simulator artifact profile unavailable")
	}
	root, err := os.OpenRoot(filepath.Join(profile.ArtifactDirectory, job.ID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	events, err := readSimulationFile(root, "events.jsonl")
	if err != nil {
		return nil, err
	}
	trajectory, err := readSimulationFile(root, "trace.jsonl")
	if err != nil {
		return nil, err
	}
	run := simulationRun(job)
	// Projection receipts are outputs, not inputs to the reconstructed trace.
	run.TraceUrl, run.TraceExportError = "", ""
	run.LogDocumentId, run.LogProjectionError, run.LogIndexedAt = "", "", ""
	run.ArtifactUri = strings.TrimRight(profile.ArtifactUrl, "/") + "/" + job.ID + "/"
	manifest, err := readSimulationFile(root, "manifest.json")
	if os.IsNotExist(err) && job.State != jobs.StateSucceeded {
		manifest, err = protojson.Marshal(run)
	}
	if err != nil {
		return nil, err
	}
	source := &pb.SimulationTraceSource{Run: run, EventsJsonl: string(events), TrajectoryJsonl: string(trajectory), ManifestJson: string(manifest), Artifacts: local.artifactViews(job.ID, simulatorOf(job.Kind))}
	frames, err := readSimulationFile(root, "frames.json")
	if err == nil {
		source.Frames = &pb.SimulationFrames{}
		if err := protojson.Unmarshal(frames, source.Frames); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return source, nil
}

// projectTrace is shared by local artifact export and OpenSearch reconstruction.
// It does not read a filesystem or substitute simulation time for wall time.
func (local *LocalSimulations) projectTrace(source *pb.SimulationTraceSource) (*tracepb.ResourceSpans, string, error) {
	parent, err := newSimulationTraceRoot(source, local.config.PublicTraces)
	if err != nil {
		return nil, "", err
	}
	children, err := projectSimulationEvents(source.EventsJsonl, parent)
	if err != nil {
		return nil, "", err
	}
	steps, err := projectSimulationTrajectory(source.TrajectoryJsonl, source.Run.Steps, children)
	if err != nil {
		return nil, "", err
	}
	if err := projectSimulationFrames(source, children); err != nil {
		return nil, "", err
	}
	spans := append([]*tracepb.Span{parent}, steps...)
	payload := &tracepb.ResourceSpans{Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{simulationTraceString("service.name", simulationTraceService)}}, ScopeSpans: []*tracepb.ScopeSpans{{Scope: &commonpb.InstrumentationScope{Name: simulationTraceService}, Spans: spans}}}
	if proto.Size(payload) > simulationTraceLimit {
		return nil, "", fmt.Errorf("native trace exceeds OTLP projection limit; raw artifacts remain available")
	}
	return payload, hex.EncodeToString(parent.TraceId), nil
}
