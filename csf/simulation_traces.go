package csf

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

const simulationTraceService = "candace.csf.simulator"
const simulationTraceLimit = 1024 * 1024
const simulationTraceVersion = "csf.simulation.v2"
const simulationTraceVersionAttribute = "langfuse.observation.metadata.projection_version"
const simulationTraceSupersedesAttribute = "langfuse.observation.metadata.supersedes_trace_url"
const simulationTraceDeadline = 10 * time.Second
const simulationDeliveryRecordDeadline = 5 * time.Second

type ISimulationTraces interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	UploadTraces(ctx context.Context, spans []*tracepb.ResourceSpans) error
}

func WithSimulationTraces(client ISimulationTraces) SimulationOption {
	return func(simulations *Simulations) { simulations.traces = client }
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
