package opencode

import (
	"strings"
	"time"

	"github.com/google/uuid"
	opencodesdk "github.com/sst/opencode-sdk-go"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// projectedEvent is one normalized event together with the bookkeeping that
// marks it delivered. record is applied to the authoritative projection state
// only after the host has accepted the event (or after the runtime has decided
// the event is historical and must never be published), which is what makes a
// rejected publication retryable rather than lost.
type projectedEvent struct {
	event *candaceosv1.HarnessEvent
	// parentMessageID is the provider user message this event descends from.
	// It is the key the run fence is evaluated against.
	parentMessageID string
	record          func(projected *projectionState)
}

func (event projectedEvent) recordInto(projected *projectionState) {
	if event.record != nil {
		event.record(projected)
	}
}

// projectMessages turns one transcript snapshot into the events that have not
// been projected yet, and reports which parent message IDs now have a completed
// assistant reply. It mutates projected as if every returned event were
// delivered; callers that publish incrementally keep a separate authoritative
// copy and apply each event's record on success.
//
// runs maps a provider message ID to the run that produced it. A message with
// no known run is provider- or operator-authored history: it is marked seen so
// it is never republished, but no event is produced for it.
func projectMessages(
	projected *projectionState,
	runs map[string]string,
	messages []providerMessage,
) ([]projectedEvent, map[string]struct{}) {
	events := make([]projectedEvent, 0)
	completed := make(map[string]struct{})
	for _, entry := range messages {
		switch info := entry.Info.AsUnion().(type) {
		case opencodesdk.UserMessage:
			events = append(events, projectUser(projected, runs, entry, info)...)
		case opencodesdk.AssistantMessage:
			events = append(events, projectAssistant(projected, runs, completed, entry, info)...)
		}
	}
	return events, completed
}

func projectUser(
	projected *projectionState,
	runs map[string]string,
	entry providerMessage,
	info opencodesdk.UserMessage,
) []projectedEvent {
	if _, seen := projected.seenUsers[info.ID]; seen {
		return nil
	}
	runID := runs[info.ID]
	if runID == "" {
		projected.seenUsers[info.ID] = struct{}{}
		return nil
	}
	content := partsText(entry.Parts)
	if content == "" {
		return nil
	}
	projected.seenUsers[info.ID] = struct{}{}
	messageID := info.ID
	return []projectedEvent{{
		parentMessageID: info.ID,
		record: func(projected *projectionState) {
			projected.seenUsers[messageID] = struct{}{}
		},
		event: &candaceosv1.HarnessEvent{
			Id:    "opencode-user-" + info.ID,
			RunId: runID,
			At:    timestamppb.New(timestampAt(int64(info.Time.Created))),
			Payload: &candaceosv1.HarnessEvent_UserMessage{
				UserMessage: &candaceosv1.HarnessUserMessage{Content: content},
			},
		},
	}}
}

func projectAssistant(
	projected *projectionState,
	runs map[string]string,
	completed map[string]struct{},
	entry providerMessage,
	info opencodesdk.AssistantMessage,
) []projectedEvent {
	finished := info.Time.Completed != 0 || info.Error.Name != ""
	at := timestampAt(int64(info.Time.Created))
	if finished {
		completed[info.ParentID] = struct{}{}
		if info.Time.Completed != 0 {
			at = timestampAt(int64(info.Time.Completed))
		}
	}
	runID := runs[info.ParentID]
	if runID == "" {
		return nil
	}
	// An abort the operator requested is not a turn failure. Suppression is
	// scoped to the provider's own aborted-message error so a genuine failure
	// during a steered turn still reaches the host.
	_, requested := projected.intentionalAborts[info.ParentID]
	intentionalAbort := requested && info.Error.Name == opencodesdk.AssistantMessageErrorNameMessageAbortedError

	events := projectAssistantText(projected, entry, info, runID, at, finished)
	for _, part := range entry.Parts {
		if tool, isTool := part.AsUnion().(opencodesdk.ToolPart); isTool {
			events = append(events, projectTool(projected, tool, info.ParentID, runID, at, intentionalAbort)...)
		}
	}
	return append(events, projectAssistantError(projected, info, runID, at, intentionalAbort)...)
}

// projectAssistantText emits either the final assistant message or the delta
// that extends what has already been streamed. A transcript that is not a
// prefix extension of the streamed text is left alone: deltas are transient and
// must never contradict what the host already showed.
func projectAssistantText(
	projected *projectionState,
	entry providerMessage,
	info opencodesdk.AssistantMessage,
	runID string,
	at time.Time,
	finished bool,
) []projectedEvent {
	content := partsText(entry.Parts)
	assistantID := info.ID
	if finished {
		if _, published := projected.finalAssistants[assistantID]; published || content == "" {
			return nil
		}
		projected.finalAssistants[assistantID] = struct{}{}
		projected.assistantText[assistantID] = content
		return []projectedEvent{{
			parentMessageID: info.ParentID,
			record: func(projected *projectionState) {
				projected.finalAssistants[assistantID] = struct{}{}
				projected.assistantText[assistantID] = content
			},
			event: &candaceosv1.HarnessEvent{
				Id:    "opencode-assistant-" + assistantID,
				RunId: runID,
				At:    timestamppb.New(at),
				Payload: &candaceosv1.HarnessEvent_AssistantMessage{
					AssistantMessage: &candaceosv1.HarnessAssistantMessage{
						MessageId: assistantID, Content: content,
					},
				},
			},
		}}
	}
	previous := projected.assistantText[assistantID]
	if content == previous || !strings.HasPrefix(content, previous) {
		return nil
	}
	delta := strings.TrimPrefix(content, previous)
	projected.assistantText[assistantID] = content
	if delta == "" {
		return nil
	}
	return []projectedEvent{{
		parentMessageID: info.ParentID,
		record: func(projected *projectionState) {
			projected.assistantText[assistantID] = content
		},
		event: &candaceosv1.HarnessEvent{
			Id:    "opencode-delta-" + uuid.NewString(),
			RunId: runID,
			At:    timestamppb.New(at),
			Payload: &candaceosv1.HarnessEvent_AssistantDelta{
				AssistantDelta: &candaceosv1.HarnessAssistantDelta{
					MessageId: assistantID, Content: delta,
				},
			},
		},
	}}
}

func projectAssistantError(
	projected *projectionState,
	info opencodesdk.AssistantMessage,
	runID string,
	at time.Time,
	intentionalAbort bool,
) []projectedEvent {
	if info.Error.Name == "" {
		return nil
	}
	assistantID := info.ID
	if _, published := projected.seenErrors[assistantID]; published {
		return nil
	}
	projected.seenErrors[assistantID] = struct{}{}
	if intentionalAbort {
		// The marker has served its purpose; clearing it keeps a later,
		// genuine abort error from being suppressed too.
		delete(projected.intentionalAborts, info.ParentID)
		return nil
	}
	return []projectedEvent{{
		parentMessageID: info.ParentID,
		record: func(projected *projectionState) {
			projected.seenErrors[assistantID] = struct{}{}
		},
		event: &candaceosv1.HarnessEvent{
			Id:    "opencode-error-" + assistantID,
			RunId: runID,
			At:    timestamppb.New(at),
			Payload: &candaceosv1.HarnessEvent_Error{
				Error: &candaceosv1.HarnessError{Message: normalizedAssistantError(info.Error)},
			},
		},
	}}
}
