package csf

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/jobs"
	"google.golang.org/protobuf/encoding/protojson"
)

const maxSimulationEvents = 100

// interpretBatchLog reads the worker's CSF_EVENT lines from a CloudWatch
// page. AWS owns job termination, so worker status lines are not applied.
func (simulations *Simulations) interpretBatchLog(ctx context.Context, job SimulationJob, lines []string) (jobs.Observation, error) {
	events := []*pb.ResearchEvent{}
	for _, line := range lines {
		if !strings.HasPrefix(line, simulationEventPrefix) {
			continue
		}
		if len(line) > maxAPIBytes {
			return jobs.Observation{}, fmt.Errorf("simulation event exceeds transport limit")
		}
		event := &pb.ResearchEvent{}
		if err := protojson.Unmarshal([]byte(strings.TrimPrefix(line, simulationEventPrefix)), event); err != nil {
			return jobs.Observation{}, err
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		return jobs.Observation{}, nil
	}
	return simulations.observe(job, events)
}

// observe validates worker events, retains them as evidence and converts
// them to a ledger observation. The ledger judges them against the job.
func (simulations *Simulations) observe(job SimulationJob, events []*pb.ResearchEvent) (jobs.Observation, error) {
	if len(events) == 0 || len(events) > maxSimulationEvents {
		return jobs.Observation{}, fmt.Errorf("1..%d events required", maxSimulationEvents)
	}
	observation := jobs.Observation{Entries: make([]jobs.ObservationEntry, 0, len(events))}
	for _, event := range events {
		entry, applies, err := simulationEntry(job, event)
		if err != nil {
			return jobs.Observation{}, err
		}
		if applies {
			observation.Entries = append(observation.Entries, entry)
		}
	}
	if len(observation.Entries) == 0 {
		return jobs.Observation{}, nil
	}
	encoded, err := protojson.Marshal(&pb.RecordSimulationEventsRequest{RunId: job.ID, Events: events})
	if err != nil {
		return jobs.Observation{}, err
	}
	observation.EvidenceHash, _, err = simulations.artifacts.Put(encoded)
	return observation, err
}

// simulationEntry converts one event. applies is false for a status from a
// worker whose lifecycle the executor owns.
func simulationEntry(job SimulationJob, event *pb.ResearchEvent) (entry jobs.ObservationEntry, applies bool, err error) {
	if event == nil {
		return entry, false, fmt.Errorf("nil simulation event")
	}
	if event.SchemaVersion != SchemaVersion || event.RecordedAt == "" {
		return entry, false, fmt.Errorf("unsupported or undated event")
	}
	stamp, err := time.Parse(time.RFC3339Nano, event.RecordedAt)
	if err != nil {
		return entry, false, err
	}
	switch payload := event.Payload.(type) {
	case *pb.ResearchEvent_Definition:
		if err := pb.ValidateMetricDefinition(payload.Definition); err != nil {
			return entry, false, err
		}
		return jobs.DefinitionEntry(jobs.MetricDefinition{Name: payload.Definition.Name, Unit: payload.Definition.Unit, Description: payload.Definition.Description}), true, nil
	case *pb.ResearchEvent_Measurement:
		metric := payload.Measurement
		if metric == nil || metric.RunId != job.ID {
			return entry, false, fmt.Errorf("measurement run or step differs from admission")
		}
		if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
			return entry, false, fmt.Errorf("non-finite measurement")
		}
		return jobs.MeasurementEntry(jobs.Measurement{Metric: metric.Metric, Step: metric.Step, Value: metric.Value, RecordedAt: stamp}), true, nil
	case *pb.ResearchEvent_Status:
		status := payload.Status
		if status == nil || status.RunId != job.ID || status.Phase == "" {
			return entry, false, fmt.Errorf("status belongs to another run")
		}
		// AWS owns job termination; its worker logs only own measurements.
		return jobs.PhaseEntry(jobs.PhaseReport{Phase: jobs.Phase(status.Phase), Message: status.Message}), job.Executor == simulationExecutorLocal, nil
	default:
		return entry, false, fmt.Errorf("event needs one typed payload")
	}
}
