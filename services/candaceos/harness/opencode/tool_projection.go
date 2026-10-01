package opencode

import (
	"time"

	opencodesdk "github.com/sst/opencode-sdk-go"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// toolObservation is the union-flattened view of one tool part. The generated
// SDK models tool state as a union; this is the only place that shape is
// destructured.
type toolObservation struct {
	status   opencodesdk.ToolPartStateStatus
	input    any
	output   string
	failure  string
	metadata map[string]any
	started  int64
	ended    int64
}

func (observation toolObservation) terminal() bool {
	return observation.status == opencodesdk.ToolPartStateStatusCompleted ||
		observation.status == opencodesdk.ToolPartStateStatusError
}

func observeTool(part opencodesdk.ToolPart) (toolObservation, bool) {
	switch state := part.State.AsUnion().(type) {
	case opencodesdk.ToolStatePending:
		return toolObservation{status: opencodesdk.ToolPartStateStatusPending}, true
	case opencodesdk.ToolStateRunning:
		return toolObservation{
			status: opencodesdk.ToolPartStateStatusRunning, input: state.Input,
			metadata: state.Metadata, started: int64(state.Time.Start),
		}, true
	case opencodesdk.ToolStateCompleted:
		return toolObservation{
			status: opencodesdk.ToolPartStateStatusCompleted, input: state.Input, output: state.Output,
			metadata: state.Metadata, started: int64(state.Time.Start), ended: int64(state.Time.End),
		}, true
	case opencodesdk.ToolStateError:
		return toolObservation{
			status: opencodesdk.ToolPartStateStatusError, input: state.Input, failure: state.Error,
			metadata: state.Metadata, started: int64(state.Time.Start), ended: int64(state.Time.End),
		}, true
	default:
		return toolObservation{}, false
	}
}

// projectTool emits at most one start and one completion per tool call. A call
// first observed in a terminal state still gets its start, so the host never
// sees a completion for a tool it was not told about.
func projectTool(
	projected *projectionState,
	part opencodesdk.ToolPart,
	parentMessageID, runID string,
	fallback time.Time,
	intentionalAbort bool,
) []projectedEvent {
	observation, known := observeTool(part)
	if !known {
		return nil
	}
	key := part.CallID
	if key == "" {
		key = part.ID
	}
	previous := projected.toolStatus[key]
	events := make([]projectedEvent, 0, 2)
	if previous == "" {
		projected.toolStatus[key] = opencodesdk.ToolPartStateStatusRunning
		events = append(events, projectedEvent{
			parentMessageID: parentMessageID,
			record: func(projected *projectionState) {
				projected.toolStatus[key] = opencodesdk.ToolPartStateStatusRunning
			},
			event: &candaceosv1.HarnessEvent{
				Id:    "opencode-tool-start-" + part.ID,
				RunId: runID,
				At:    timestamppb.New(timestampOr(observation.started, fallback)),
				Payload: &candaceosv1.HarnessEvent_ToolStarted{
					ToolStarted: &candaceosv1.HarnessToolStarted{
						ToolCallId: key, ToolName: part.Tool, Arguments: structValue(observation.input),
					},
				},
			},
		})
	}
	if !observation.terminal() || previous == observation.status {
		return events
	}
	status := observation.status
	projected.toolStatus[key] = status
	completion := &candaceosv1.HarnessToolCompleted{ToolCallId: key, ToolName: part.Tool}
	recordToolOutcome(completion, observation, intentionalAbort)
	return append(events, projectedEvent{
		parentMessageID: parentMessageID,
		record: func(projected *projectionState) {
			projected.toolStatus[key] = status
		},
		event: &candaceosv1.HarnessEvent{
			Id:      "opencode-tool-complete-" + part.ID,
			RunId:   runID,
			At:      timestamppb.New(timestampOr(observation.ended, fallback)),
			Payload: &candaceosv1.HarnessEvent_ToolCompleted{ToolCompleted: completion},
		},
	})
}

// recordToolOutcome classifies a finished tool call onto completion. A tool the
// operator interrupted is reported as a succeeded call carrying an interrupted
// marker rather than a failure, so steering does not look like a broken tool.
func recordToolOutcome(
	completion *candaceosv1.HarnessToolCompleted,
	observation toolObservation,
	intentionalAbort bool,
) {
	if observation.status != opencodesdk.ToolPartStateStatusError {
		completion.Outcome = &candaceosv1.HarnessToolCompleted_Succeeded{
			Succeeded: &candaceosv1.HarnessToolSucceeded{
				Result: structpb.NewStringValue(boundedText(observation.output)),
			},
		}
		return
	}
	if interrupted, _ := observation.metadata["interrupted"].(bool); intentionalAbort && interrupted {
		completion.Outcome = interruptedToolOutcome(observation.metadata)
		return
	}
	failure := boundedText(observation.failure)
	if failure == "" {
		failure = "OpenCode tool failed"
	}
	completion.Outcome = &candaceosv1.HarnessToolCompleted_Failed{
		Failed: &candaceosv1.HarnessToolFailed{Message: failure},
	}
}

func interruptedToolOutcome(metadata map[string]any) *candaceosv1.HarnessToolCompleted_Succeeded {
	notice, _ := metadata["output"].(string)
	if notice == "" {
		notice = "Interrupted by operator"
	}
	result, _ := structpb.NewStruct(map[string]any{
		"output": boundedText(notice), "interrupted": true,
	})
	return &candaceosv1.HarnessToolCompleted_Succeeded{
		Succeeded: &candaceosv1.HarnessToolSucceeded{Result: structpb.NewStructValue(result)},
	}
}
