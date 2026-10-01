package csf

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/encoding/protojson"
)

const simulationCancelReason = "CSF operator cancellation"

func (simulations *Simulations) poll(ctx context.Context, row db.BrainspineSimulation) error {
	result, err := simulations.batch.DescribeJobs(ctx, &batch.DescribeJobsInput{Jobs: []string{row.JobID}})
	if err != nil {
		return err
	}
	if result == nil || len(result.Jobs) != 1 {
		return fmt.Errorf("AWS did not return the recorded job")
	}
	job := result.Jobs[0]
	if aws.ToString(job.JobId) != row.JobID || aws.ToString(job.JobQueue) != row.JobQueue || aws.ToString(job.JobDefinition) != row.JobDefinition || job.Tags["csf-run-id"] != row.RunID {
		return fmt.Errorf("AWS job ownership differs from the admitted job")
	}
	state := db.BrainspineSimulationStateQueued
	terminal := false
	switch job.Status {
	case batchtypes.JobStatusSubmitted, batchtypes.JobStatusPending, batchtypes.JobStatusRunnable:
	case batchtypes.JobStatusStarting, batchtypes.JobStatusRunning:
		state = db.BrainspineSimulationStateRunning
	case batchtypes.JobStatusSucceeded:
		state = db.BrainspineSimulationStateSucceeded
		terminal = true
	case batchtypes.JobStatusFailed:
		state = db.BrainspineSimulationStateFailed
		terminal = true
		if row.CancellationRequested && aws.ToString(job.StatusReason) == simulationCancelReason {
			state = db.BrainspineSimulationStateCancelled
		}
	default:
		return fmt.Errorf("unknown AWS job status %q", job.Status)
	}
	if row.CancellationRequested && !terminal {
		if _, err := simulations.batch.TerminateJob(ctx, &batch.TerminateJobInput{JobId: aws.String(row.JobID), Reason: aws.String(simulationCancelReason)}); err != nil {
			return err
		}
		state = db.BrainspineSimulationStateCancelling
	}
	stream := ""
	if job.Container != nil {
		stream = aws.ToString(job.Container.LogStreamName)
	}
	if err := simulations.store.queries.UpdateSimulationProvider(ctx, db.UpdateSimulationProviderParams{RunID: row.RunID, State: state, Reason: aws.ToString(job.StatusReason), LogStream: stream, CleanupConfirmed: terminal}); err != nil {
		return err
	}
	if stream == "" {
		return nil
	}
	input := &cloudwatchlogs.GetLogEventsInput{LogGroupName: aws.String(simulations.config.LogGroup), LogStreamName: aws.String(stream), StartFromHead: aws.Bool(true), Limit: aws.Int32(100)}
	if row.LogToken != "" {
		input.NextToken = aws.String(row.LogToken)
	}
	logs, err := simulations.logs.GetLogEvents(ctx, input)
	if err != nil {
		return err
	}
	if logs == nil {
		return fmt.Errorf("empty CloudWatch response")
	}
	events := []*pb.ResearchEvent{}
	for _, log := range logs.Events {
		line := aws.ToString(log.Message)
		if !strings.HasPrefix(line, simulationEventPrefix) {
			continue
		}
		if len(line) > maxAPIBytes {
			return fmt.Errorf("simulation event exceeds transport limit")
		}
		event := &pb.ResearchEvent{}
		if err := protojson.Unmarshal([]byte(strings.TrimPrefix(line, simulationEventPrefix)), event); err != nil {
			return err
		}
		events = append(events, event)
	}
	if len(events) > 0 {
		if err := simulations.record(ctx, row.RunID, events); err != nil {
			return err
		}
	}
	return simulations.store.queries.UpdateSimulationLogToken(ctx, db.UpdateSimulationLogTokenParams{RunID: row.RunID, LogToken: aws.ToString(logs.NextForwardToken)})
}

func simulationTerminal(state db.BrainspineSimulationState) bool {
	return state == db.BrainspineSimulationStateSucceeded || state == db.BrainspineSimulationStateFailed || state == db.BrainspineSimulationStateCancelled
}

func (simulations *Simulations) record(ctx context.Context, runID string, events []*pb.ResearchEvent) error {
	if len(events) == 0 || len(events) > 100 {
		return fmt.Errorf("1..100 events required")
	}
	envelope := &pb.RecordSimulationEventsRequest{RunId: runID, Events: events}
	encoded, err := protojson.Marshal(envelope)
	if err != nil {
		return err
	}
	hash, _, err := simulations.artifacts.Put(encoded)
	if err != nil {
		return err
	}
	tx, err := simulations.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := simulations.store.queries.WithTx(tx)
	row, err := queries.LockSimulation(ctx, runID)
	if err != nil {
		return err
	}
	definitions, err := queries.ListSimulationDefinitions(ctx, runID)
	if err != nil {
		return err
	}
	registry := map[string]*pb.MetricDefinition{}
	for _, definition := range definitions {
		registry[definition.Name] = &pb.MetricDefinition{Name: definition.Name, Unit: definition.Unit, Description: definition.Description}
	}
	for _, event := range events {
		if event == nil {
			return fmt.Errorf("nil simulation event")
		}
		if err := validateEvent(event, registry); err != nil {
			return err
		}
		stamp, err := time.Parse(time.RFC3339Nano, event.RecordedAt)
		if err != nil {
			return err
		}
		switch payload := event.Payload.(type) {
		case *pb.ResearchEvent_Definition:
			count, err := queries.InsertSimulationDefinition(ctx, db.InsertSimulationDefinitionParams{RunID: runID, Name: payload.Definition.Name, Unit: payload.Definition.Unit, Description: payload.Definition.Description})
			if err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("metric definition changed")
			}
		case *pb.ResearchEvent_Measurement:
			metric := payload.Measurement
			if metric.RunId != runID || metric.Step > uint64(row.Steps) {
				return fmt.Errorf("measurement run or step differs from admission")
			}
			count, err := queries.InsertSimulationMeasurement(ctx, db.InsertSimulationMeasurementParams{RunID: runID, Metric: metric.Metric, Step: int64(metric.Step), Value: metric.Value, RecordedAt: pgtype.Timestamptz{Time: stamp, Valid: true}, EvidenceHash: hash})
			if err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("conflicting metric replay")
			}
			if metric.Metric == simulationProgressMetric {
				if metric.Value != math.Trunc(metric.Value) || metric.Value < 0 || metric.Value > float64(row.Steps) || uint64(metric.Value) != metric.Step {
					return fmt.Errorf("completed steps must equal the event step within admission bounds")
				}
				if int32(metric.Value) > row.CompletedSteps {
					row.CompletedSteps = int32(metric.Value)
				}
			}
		case *pb.ResearchEvent_Status:
			status := payload.Status
			if status.RunId != runID {
				return fmt.Errorf("status belongs to another run")
			}
			if row.Executor != int32(pb.SimulationExecutor_SIMULATION_EXECUTOR_LOCAL) {
				continue
			} // AWS owns job termination, worker logs only own measurements.
			switch status.Phase {
			case "started":
				if row.State == db.BrainspineSimulationStatePending {
					row.State = db.BrainspineSimulationStateRunning
				}
			case "completed":
				if !simulationTerminal(row.State) {
					if row.CompletedSteps != row.Steps {
						return fmt.Errorf("completion requires every admitted step")
					}
					row.State = db.BrainspineSimulationStateSucceeded
					row.Reason = status.Message
				}
			case "failed":
				// A retained worker failure (including artifact delivery or cleanup after
				// physics completion) invalidates an earlier success report.
				row.State = db.BrainspineSimulationStateFailed
				row.Reason = status.Message
			case "cancelled":
				if row.CancellationRequested {
					row.State = db.BrainspineSimulationStateCancelled
					row.Reason = status.Message
				}
			default:
				return fmt.Errorf("unsupported simulation phase %q", status.Phase)
			}
		}
	}
	if err := queries.RecordSimulationProgress(ctx, db.RecordSimulationProgressParams{RunID: runID, CompletedSteps: row.CompletedSteps, State: row.State, Reason: row.Reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
