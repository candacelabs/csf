package csf

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const simulationTraceService = "candace.csf.simulator"
const simulationTraceLimit = 1024 * 1024
const simulationTraceVersion = "csf.simulation.v2"
const simulationTraceVersionAttribute = "langfuse.observation.metadata.projection_version"
const simulationTraceSupersedesAttribute = "langfuse.observation.metadata.supersedes_trace_url"

type ISimulationTraces interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	UploadTraces(ctx context.Context, spans []*tracepb.ResourceSpans) error
}

func WithSimulationTraces(client ISimulationTraces) SimulationOption {
	return func(simulations *Simulations) { simulations.traces = client }
}

// Export is a projection of retained native artifacts. The delivery ledger
// prevents resending immutable Langfuse spans after an ambiguous HTTP outcome.
func (simulations *Simulations) exportSimulationTrace(ctx context.Context) error {
	if simulations.traces == nil || simulations.local == nil || simulations.local.config.TraceBaseUrl == "" {
		return nil
	}
	row, err := simulations.store.queries.NextSimulationTrace(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	spans, identity, problem := simulations.local.trace(row)
	if problem == nil {
		deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
		problem = simulations.deliverSimulationTrace(deadline, row, spans, identity)
		cancel()
	}
	if problem == nil {
		return nil
	}
	return simulations.store.queries.SetSimulationTrace(ctx, db.SetSimulationTraceParams{RunID: row.RunID, TraceExportError: problem.Error()})
}

func (simulations *Simulations) deliverSimulationTrace(ctx context.Context, row db.BrainspineSimulation, spans *tracepb.ResourceSpans, identity string) error {
	destination := strings.TrimRight(simulations.local.config.TraceBaseUrl, "/")
	url := destination + "/" + identity
	if row.TraceUrl == url {
		return nil
	}
	_, err := simulations.store.queries.ClaimSimulationTraceDelivery(ctx, db.ClaimSimulationTraceDeliveryParams{Destination: destination, RunID: row.RunID, TraceID: identity})
	if errors.Is(err, pgx.ErrNoRows) {
		delivery, readErr := simulations.store.queries.GetSimulationTraceDelivery(ctx, db.GetSimulationTraceDeliveryParams{Destination: destination, TraceID: identity})
		if readErr != nil {
			return readErr
		}
		if delivery.State != db.BrainspineTraceDeliveryStateSucceeded {
			return fmt.Errorf("trace delivery %s is %s; inspect the sink before recovery, automatic resend is disabled", identity, delivery.State)
		}
	} else if err != nil {
		return err
	} else if err := simulations.sendSimulationTrace(ctx, destination, row.TraceUrl, spans, identity); err != nil {
		return err
	}
	return simulations.store.queries.SetSimulationTrace(ctx, db.SetSimulationTraceParams{RunID: row.RunID, TraceUrl: url})
}

func (simulations *Simulations) sendSimulationTrace(ctx context.Context, destination string, previousURL string, spans *tracepb.ResourceSpans, identity string) error {
	if previousURL != "" {
		parent := spans.ScopeSpans[0].Spans[0]
		parent.Attributes = append(parent.Attributes, simulationTraceString(simulationTraceSupersedesAttribute, previousURL))
	}
	problem := simulations.traces.UploadTraces(ctx, []*tracepb.ResourceSpans{spans})
	result := db.FinishSimulationTraceDeliveryParams{Destination: destination, TraceID: identity, State: db.BrainspineTraceDeliveryStateSucceeded}
	if problem != nil {
		result.State, result.Error = db.BrainspineTraceDeliveryStateAmbiguous, problem.Error()
	}
	deadline, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := simulations.store.queries.FinishSimulationTraceDelivery(deadline, result)
	if failure := errors.Join(problem, err); failure != nil {
		return fmt.Errorf("trace delivery unconfirmed; automatic resend is disabled: %w", failure)
	}
	return nil
}

func (local *LocalSimulations) trace(row db.BrainspineSimulation) (*tracepb.ResourceSpans, string, error) {
	source, err := local.traceSource(row)
	if err != nil {
		return nil, "", err
	}
	return local.projectTrace(source)
}

// traceSource retains the exact vendor text used to derive stable trace IDs.
// Camera byte hashes are verified here; replay retains their observed references.
func (local *LocalSimulations) traceSource(row db.BrainspineSimulation) (*pb.SimulationTraceSource, error) {
	profile := local.profile(pb.Simulator(row.Simulator))
	if profile == nil || !simulationID.MatchString(row.RunID) {
		return nil, fmt.Errorf("simulator artifact profile unavailable")
	}
	root, err := os.OpenRoot(filepath.Join(profile.ArtifactDirectory, row.RunID))
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
	run := simulationViews.Run(row)
	// Projection receipts are outputs, not inputs to the reconstructed trace.
	run.TraceUrl, run.TraceExportError = "", ""
	run.LogDocumentId, run.LogProjectionError, run.LogIndexedAt = "", "", ""
	run.ArtifactUri = strings.TrimRight(profile.ArtifactUrl, "/") + "/" + row.RunID + "/"
	manifest, err := readSimulationFile(root, "manifest.json")
	if os.IsNotExist(err) && row.State != db.BrainspineSimulationStateSucceeded {
		manifest, err = protojson.Marshal(run)
	}
	if err != nil {
		return nil, err
	}
	source := &pb.SimulationTraceSource{Run: run, EventsJsonl: string(events), TrajectoryJsonl: string(trajectory), ManifestJson: string(manifest), Artifacts: local.artifactViews(row.RunID, pb.Simulator(row.Simulator))}
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

func newSimulationTraceRoot(source *pb.SimulationTraceSource, publicTraces bool) (*tracepb.Span, error) {
	if source == nil || source.Run == nil || !simulationID.MatchString(source.Run.RunId) {
		return nil, fmt.Errorf("trace source has no valid simulation identity")
	}
	run := source.Run
	trajectory, manifest := []byte(source.TrajectoryJsonl), []byte(source.ManifestJson)
	// The source is arbitrary vendor JSON, represented by protobuf's native JSON
	// value. The transport remains official OTLP protobuf, never a hand-made DTO.
	if err := protojson.Unmarshal(manifest, &structpb.Struct{}); err != nil {
		return nil, err
	}
	traceHash := sha256.Sum256(trajectory)
	identity := sha256.Sum256([]byte(simulationTraceVersion + "\x00" + run.RunId + "\x00" + hex.EncodeToString(traceHash[:])))
	parentID := sha256.Sum256(identity[:])
	parent := &tracepb.Span{TraceId: identity[:16], SpanId: parentID[:8], Name: run.RunId, Kind: tracepb.Span_SPAN_KIND_INTERNAL, Attributes: []*commonpb.KeyValue{
		simulationTraceString("langfuse.trace.name", run.RunId), simulationTraceString("langfuse.observation.type", "span"),
		simulationTraceString("langfuse.observation.output", string(manifest)), simulationTraceString("langfuse.observation.metadata.state", run.State.String()),
		simulationTraceString("langfuse.observation.metadata.source_sha256", hex.EncodeToString(traceHash[:])),
		simulationTraceString("langfuse.observation.metadata.artifact_url", run.ArtifactUri),
		simulationTraceString(simulationTraceVersionAttribute, simulationTraceVersion),
		simulationTraceString("langfuse.observation.metadata.clock", "span times are observed event UTC; simulation_seconds is a separate simulator clock"),
		{Key: "langfuse.trace.public", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: publicTraces}}},
	}}
	if run.State != pb.SimulationState_SIMULATION_STATE_SUCCEEDED {
		parent.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: run.Reason}
	}
	return parent, nil
}

func projectSimulationEvents(events string, parent *tracepb.Span) (map[uint64]*tracepb.Span, error) {
	children := map[uint64]*tracepb.Span{}
	scanner := bufio.NewScanner(strings.NewReader(events))
	scanner.Buffer(make([]byte, 4096), maxAPIBytes)
	for scanner.Scan() {
		event := &pb.ResearchEvent{}
		if err := protojson.Unmarshal(scanner.Bytes(), event); err != nil {
			return nil, err
		}
		stamp, err := time.Parse(time.RFC3339Nano, event.RecordedAt)
		if err != nil || stamp.UnixNano() <= 0 {
			return nil, fmt.Errorf("native event has invalid wall clock")
		}
		nano := uint64(stamp.UnixNano())
		if parent.StartTimeUnixNano == 0 || nano < parent.StartTimeUnixNano {
			parent.StartTimeUnixNano = nano
		}
		if nano > parent.EndTimeUnixNano {
			parent.EndTimeUnixNano = nano
		}
		metric := event.GetMeasurement()
		if metric == nil || metric.Step == 0 {
			continue
		}
		child := children[metric.Step]
		if child == nil {
			spanID := sha256.Sum256([]byte(hex.EncodeToString(parent.TraceId) + "\x00step\x00" + strconv.FormatUint(metric.Step, 10)))
			child = &tracepb.Span{TraceId: parent.TraceId, SpanId: spanID[:8], ParentSpanId: parent.SpanId, Name: fmt.Sprintf("Simulation step %d", metric.Step), Kind: tracepb.Span_SPAN_KIND_INTERNAL, StartTimeUnixNano: nano, EndTimeUnixNano: nano,
				Attributes: []*commonpb.KeyValue{simulationTraceString("langfuse.observation.type", "span"), {Key: "langfuse.observation.metadata." + simulationProgressMetric, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(metric.Step)}}}}}
			children[metric.Step] = child
		}
		if nano < child.StartTimeUnixNano {
			child.StartTimeUnixNano = nano
		}
		if nano > child.EndTimeUnixNano {
			child.EndTimeUnixNano = nano
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if parent.StartTimeUnixNano == 0 {
		return nil, fmt.Errorf("trace has no observed events")
	}
	return children, nil
}

func projectSimulationTrajectory(trajectory string, maxSteps uint32, children map[uint64]*tracepb.Span) ([]*tracepb.Span, error) {
	var spans []*tracepb.Span
	scanner := bufio.NewScanner(strings.NewReader(trajectory))
	scanner.Buffer(make([]byte, 4096), maxAPIBytes)
	step := uint64(0)
	for scanner.Scan() {
		step++
		if step > uint64(maxSteps) {
			return nil, fmt.Errorf("trajectory exceeds admitted step count")
		}
		source := &structpb.Struct{}
		if err := protojson.Unmarshal(scanner.Bytes(), source); err != nil {
			return nil, err
		}
		child := children[step]
		if child == nil {
			return nil, fmt.Errorf("native step %d has no observed wall-clock measurements", step)
		}
		child.Attributes = append(child.Attributes, simulationTraceString("langfuse.observation.output", string(scanner.Bytes())))
		spans = append(spans, child)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return spans, nil
}

func projectSimulationFrames(source *pb.SimulationTraceSource, children map[uint64]*tracepb.Span) error {
	for _, frame := range source.GetFrames().GetFrames() {
		child := children[uint64(frame.Step)]
		if child == nil || !simulationArtifactName.MatchString(frame.Path) || !strings.HasSuffix(frame.Path, ".png") {
			return fmt.Errorf("camera frame does not belong to an observed simulation step")
		}
		var artifact *pb.SimulationArtifact
		for _, candidate := range source.Artifacts {
			if candidate.Path == frame.Path {
				artifact = candidate
				break
			}
		}
		if artifact == nil || len(frame.Sha256) != sha256.Size*2 || artifact.Sha256 != frame.Sha256 {
			return fmt.Errorf("camera frame hash mismatch")
		}
		child.Attributes = append(child.Attributes,
			simulationTraceString("langfuse.observation.metadata.camera_url", artifact.Url),
			simulationTraceString("langfuse.observation.metadata.camera_sha256", frame.Sha256))
	}
	return nil
}

func readSimulationFile(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxAPIBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxAPIBytes {
		return nil, fmt.Errorf("simulation artifact exceeds projection limit")
	}
	return content, nil
}

func simulationTraceString(key string, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}
