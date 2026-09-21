package copilotbridge

import (
	"encoding/json"
	"strconv"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/uuid"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const (
	// copilotAuthor is the transcript author every CLI-originated message is
	// persisted under; the operator's own turns carry their own author.
	copilotAuthor = "copilot"
	// assistantMessageContentField and sessionErrorMessageField are the JSON
	// field names the SDK payloads carry their text under.
	assistantMessageContentField = "content"
	sessionErrorMessageField     = "message"
)

// eventTranslator turns SDK session events into the adapter's BridgeEvents.
// The SDK delivers events on one goroutine per session, so the tool-name map
// has one owner and needs no lock.
type eventTranslator struct {
	events        chan<- copilotadapter.BridgeEvent
	sessionID     uuid.UUID
	resolutions   *resolutionRegistry
	toolNames     map[copilotadapter.ToolCallID]copilotadapter.ToolName
	toolTurns     map[copilotadapter.ToolCallID]uuid.UUID
	subagentTools map[copilotadapter.ToolCallID]struct{}
	agentTurns    map[string]uuid.UUID
	messageTurns  map[string]uuid.UUID
	pendingDeltas map[string][]copilotadapter.BridgeEvent
	eventIDs      sdkEventIDCursor
	usageIDs      sdkEventIDCursor
	turns         *turnCorrelator
	onAbort       func(identifier uuid.UUID)
}

func newEventTranslator(events chan<- copilotadapter.BridgeEvent, turns *turnCorrelator, abortObservers ...func(identifier uuid.UUID)) *eventTranslator {
	translator := &eventTranslator{
		events: events, turns: turns,
		toolNames: map[copilotadapter.ToolCallID]copilotadapter.ToolName{},
		toolTurns: map[copilotadapter.ToolCallID]uuid.UUID{}, subagentTools: map[copilotadapter.ToolCallID]struct{}{},
		agentTurns:    map[string]uuid.UUID{},
		messageTurns:  map[string]uuid.UUID{},
		pendingDeltas: map[string][]copilotadapter.BridgeEvent{},
	}
	if len(abortObservers) > 0 {
		translator.onAbort = abortObservers[0]
	}
	return translator
}

func (translator *eventTranslator) handlePermissions(sessionID uuid.UUID, resolutions *resolutionRegistry) {
	translator.sessionID = sessionID
	translator.resolutions = resolutions
}

func (translator *eventTranslator) handle(event copilot.SessionEvent) {
	controlAccepted := translator.eventIDs.accept(event)
	if isUsageEvent(event) {
		// Keep control-chain bookkeeping for every event, but admit a late
		// provider measurement through a bounded ID-only cursor when its old
		// parent is no longer the durable head.
		if !translator.usageIDs.accept(copilot.SessionEvent{ID: event.ID}) {
			return
		}
	} else if !controlAccepted {
		return
	}
	switch data := event.Data.(type) {
	case *rpc.PermissionRequestedData:
		translator.handlePermissionRequested(event, data)
		return
	case *rpc.PermissionCompletedData:
		translator.handlePermissionCompleted(event, data)
		return
	}
	if translator.handleStreamingMessage(event) {
		return
	}
	translated := translateEvents(event, translator.toolNames, translator.turns)
	if len(translated) == 0 {
		if _, idle := event.Data.(*rpc.SessionIdleData); idle && event.AgentID == nil {
			translator.clearCorrelationHistory()
		}
		return
	}
	for index := range translated {
		translated[index].ID = translatedEventID(event.ID, translated[index], index, len(translated))
		if translated[index].Kind == copilotadapter.BridgeEventUsage {
			translated[index].UsagePayload, _ = json.Marshal(event)
		}
		translator.correlate(event, &translated[index])
		if message, messageEvent := event.Data.(*rpc.AssistantMessageData); messageEvent && event.AgentID == nil {
			translator.flushMessageDeltas(message.MessageID, translated[index].TurnID)
			delete(translator.messageTurns, message.MessageID)
		}
		if translated[index].Kind == copilotadapter.BridgeEventTurnAborted && translated[index].TurnID != nil && translator.onAbort != nil {
			translator.onAbort(*translated[index].TurnID)
		}
		translator.events <- translated[index]
		if translated[index].Kind == copilotadapter.BridgeEventSubagentCompleted ||
			translated[index].Kind == copilotadapter.BridgeEventSubagentFailed {
			delete(translator.agentTurns, translated[index].AgentID)
		}
	}
	if _, idle := event.Data.(*rpc.SessionIdleData); idle {
		translator.clearCorrelationHistory()
	}
}

func (translator *eventTranslator) handlePermissionRequested(event copilot.SessionEvent, data *rpc.PermissionRequestedData) {
	if translator.resolutions == nil || translator.sessionID == uuid.Nil || event.ID == "" || data.RequestID == "" ||
		(data.ResolvedByHook != nil && *data.ResolvedByHook) {
		return
	}
	translator.publishPermission(event, data, translator.permissionTurn(event, data))
}

func (translator *eventTranslator) publishPermission(event copilot.SessionEvent, data *rpc.PermissionRequestedData, turnID *uuid.UUID) {
	identifier := uuid.NewSHA1(translator.sessionID, []byte("copilot.permission.event\x00"+event.ID))
	opened, err := translator.resolutions.open(identifier, data.RequestID)
	if err != nil || !opened {
		return
	}
	description, _ := json.Marshal(data.PermissionRequest)
	occurredAt := event.Timestamp
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	translator.events <- copilotadapter.BridgeEvent{
		ID: event.ID, Kind: copilotadapter.BridgeEventRequestOpened, OccurredAt: occurredAt,
		RequestID: &identifier, RequestKind: copilotadapter.BridgeRequestPermission,
		TurnID: turnID, ToolName: permissionToolName(data.PermissionRequest), Text: string(description),
	}
}

func (translator *eventTranslator) permissionTurn(event copilot.SessionEvent, data *rpc.PermissionRequestedData) *uuid.UUID {
	if toolCallID := permissionToolCallID(data.PermissionRequest); toolCallID != "" {
		if identifier, found := translator.toolTurns[toolCallID]; found {
			return &identifier
		}
		if event.AgentID != nil {
			if identifier, found := translator.agentTurns[*event.AgentID]; found {
				return &identifier
			}
		}
		return nil
	}
	if event.AgentID != nil {
		identifier, found := translator.agentTurns[*event.AgentID]
		if !found {
			return nil
		}
		return &identifier
	}
	return activeTurn(translator.turns)
}

func (translator *eventTranslator) handlePermissionCompleted(event copilot.SessionEvent, data *rpc.PermissionCompletedData) {
	if translator.resolutions == nil || event.ID == "" || data.RequestID == "" || data.Result == nil {
		return
	}
	decision, recognized := permissionResultDecision(data.Result)
	if !recognized {
		return
	}
	identifier, found := translator.resolutions.complete(data.RequestID, decision)
	if !found {
		return
	}
	occurredAt := event.Timestamp
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	translator.events <- copilotadapter.BridgeEvent{
		ID: event.ID, Kind: copilotadapter.BridgeEventRequestCompleted, OccurredAt: occurredAt,
		RequestID: &identifier, ResolutionDecision: decision,
	}
}

func permissionResultDecision(result rpc.PermissionResult) (string, bool) {
	switch result.Kind() {
	case rpc.PermissionResultKindApproved, rpc.PermissionResultKindApprovedForLocation, rpc.PermissionResultKindApprovedForSession:
		return "approve", true
	case rpc.PermissionResultKindCancelled,
		rpc.PermissionResultKindDeniedByContentExclusionPolicy,
		rpc.PermissionResultKindDeniedByPermissionRequestHook,
		rpc.PermissionResultKindDeniedByRules,
		rpc.PermissionResultKindDeniedInteractivelyByUser,
		rpc.PermissionResultKindDeniedNoApprovalRuleAndCouldNotRequestFromUser:
		return "deny", true
	default:
		return "", false
	}
}

const sdkEventIDWindowSize = 256

// sdkEventIDCursor bounds the per-session replay filter. The persisted SDK
// events form one linked chain, so its head rejects a replay of any age.
// Ephemeral events disappear from replayed history but may be either a sibling
// of the next durable event or its immediate parent. A bounded alias records
// their durable anchor without advancing the durable head, supporting both
// shapes without letting a transient branch weaken replay rejection. Durable
// bridge-event receipts remain the cross-process replay authority.
type sdkEventIDCursor struct {
	head             string
	linked           bool
	recent           map[string]struct{}
	ephemeralAnchors map[string]string
	order            [sdkEventIDWindowSize]string
	next             int
	recentSize       int
}

func (cursor *sdkEventIDCursor) accept(event copilot.SessionEvent) bool {
	if event.ID == "" {
		return true
	}
	if event.ID == cursor.head {
		return false
	}
	if _, found := cursor.recent[event.ID]; found {
		return false
	}
	if event.Ephemeral != nil && *event.Ephemeral {
		return cursor.rememberEphemeral(event)
	}
	if event.ParentID != nil {
		if cursor.head != "" && *event.ParentID != cursor.head {
			anchor, found := cursor.ephemeralAnchors[*event.ParentID]
			if !found || anchor != cursor.head {
				return false
			}
		}
		cursor.linked = true
	} else if cursor.linked && cursor.head != "" {
		// ParentID is null only on the first durable event. Once a linked event
		// has been observed, another durable root is necessarily stale.
		return false
	}
	cursor.head = event.ID
	cursor.remember(event.ID)
	return true
}

func isUsageEvent(event copilot.SessionEvent) bool {
	switch event.Data.(type) {
	case *rpc.AssistantUsageData, *rpc.SessionUsageCheckpointData:
		return true
	default:
		return false
	}
}

func (cursor *sdkEventIDCursor) rememberEphemeral(event copilot.SessionEvent) bool {
	anchor, anchored := cursor.ephemeralAnchor(event.ParentID)
	if !anchored {
		return false
	}
	cursor.remember(event.ID)
	if cursor.ephemeralAnchors == nil {
		cursor.ephemeralAnchors = make(map[string]string, sdkEventIDWindowSize)
	}
	cursor.ephemeralAnchors[event.ID] = anchor
	return true
}

func (cursor *sdkEventIDCursor) ephemeralAnchor(parentID *string) (string, bool) {
	if parentID == nil {
		return "", cursor.head == ""
	}
	if *parentID == cursor.head {
		return cursor.head, true
	}
	anchor, found := cursor.ephemeralAnchors[*parentID]
	return anchor, found && anchor == cursor.head
}

func (cursor *sdkEventIDCursor) remember(identifier string) {
	if cursor.recent == nil {
		cursor.recent = make(map[string]struct{}, sdkEventIDWindowSize)
	}
	if cursor.recentSize < sdkEventIDWindowSize {
		cursor.order[cursor.recentSize] = identifier
		cursor.recentSize++
		cursor.recent[identifier] = struct{}{}
		return
	}
	evicted := cursor.order[cursor.next]
	delete(cursor.recent, evicted)
	delete(cursor.ephemeralAnchors, evicted)
	cursor.order[cursor.next] = identifier
	cursor.next = (cursor.next + 1) % sdkEventIDWindowSize
	cursor.recent[identifier] = struct{}{}
}

func translatedEventID(sourceID string, event copilotadapter.BridgeEvent, index int, count int) string {
	if count == 1 || sourceID == "" {
		return sourceID
	}
	turnID := ""
	if event.TurnID != nil {
		turnID = event.TurnID.String()
	}
	seed := sourceID + "\x00" + string(event.Kind) + "\x00" + turnID + "\x00" + strconv.Itoa(index)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}

// handleStreamingMessage correlates deltas through the stable SDK message id.
// A delta has no turn id, so it is buffered until either message_start captures
// the then-active turn or the completed assistant.message supplies an exact id.
func (translator *eventTranslator) handleStreamingMessage(event copilot.SessionEvent) bool {
	if event.AgentID != nil {
		return false
	}
	switch data := event.Data.(type) {
	case *rpc.AssistantMessageStartData:
		if identifier := activeTurn(translator.turns); identifier != nil {
			translator.messageTurns[data.MessageID] = *identifier
			// A delta may arrive before message_start. Correlation becomes exact
			// here, so release those original events now, ahead of every later
			// delta and without requiring a completed-message event.
			translator.flushMessageDeltas(data.MessageID, identifier)
		}
		return true
	case *rpc.AssistantMessageDeltaData:
		occurredAt := event.Timestamp
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		delta := copilotadapter.BridgeEvent{
			ID: event.ID, Kind: copilotadapter.BridgeEventAssistantDelta, OccurredAt: occurredAt,
			Text: data.DeltaContent, Author: copilotAuthor,
		}
		if identifier, found := translator.messageTurns[data.MessageID]; found {
			delta.TurnID = &identifier
			translator.events <- delta
		} else {
			translator.pendingDeltas[data.MessageID] = append(translator.pendingDeltas[data.MessageID], delta)
		}
		return true
	}
	return false
}

func (translator *eventTranslator) flushMessageDeltas(messageID string, turnID *uuid.UUID) {
	for _, delta := range translator.pendingDeltas[messageID] {
		delta.TurnID = turnID
		translator.events <- delta
	}
	delete(translator.pendingDeltas, messageID)
}

func (translator *eventTranslator) correlate(event copilot.SessionEvent, translated *copilotadapter.BridgeEvent) {
	switch data := event.Data.(type) {
	case *rpc.AssistantUsageData:
		// Only explicit provider identities may link usage to a turn. An active
		// turn is insufficient: billing events can arrive after the next turn starts.
		translated.TurnID = translator.turnForToolPointer(data.ParentToolCallID, nil)
		if translated.TurnID == nil && event.AgentID != nil {
			if id, found := translator.agentTurns[*event.AgentID]; found {
				translated.TurnID = &id
			}
		}
	case *rpc.AssistantMessageData:
		if event.AgentID != nil && data.ParentToolCallID != nil {
			translated.TurnID = translator.turnForTool(*data.ParentToolCallID, translated.TurnID)
		}
		if event.AgentID == nil {
			if identifier, found := translator.messageTurns[data.MessageID]; found {
				translated.TurnID = &identifier
			} else if translated.TurnID != nil {
				translator.messageTurns[data.MessageID] = *translated.TurnID
			}
		}
	case *rpc.ToolExecutionStartData:
		translated.TurnID = translator.turnForToolPointer(data.ParentToolCallID, translated.TurnID)
		if translated.TurnID != nil {
			toolCallID := copilotadapter.ToolCallID(data.ToolCallID)
			if _, found := translator.toolTurns[toolCallID]; !found {
				translator.toolTurns[toolCallID] = *translated.TurnID
			}
		}
	case *rpc.ToolExecutionCompleteData:
		translated.TurnID = translator.turnForTool(data.ToolCallID, translated.TurnID)
		translated.TurnID = translator.turnForToolPointer(data.ParentToolCallID, translated.TurnID)
		toolCallID := copilotadapter.ToolCallID(data.ToolCallID)
		if _, subagent := translator.subagentTools[toolCallID]; !subagent {
			delete(translator.toolTurns, toolCallID)
		}
	case *rpc.SubagentStartedData:
		translated.TurnID = translator.turnForTool(data.ToolCallID, translated.TurnID)
		translator.subagentTools[copilotadapter.ToolCallID(data.ToolCallID)] = struct{}{}
		if translated.TurnID != nil && translated.AgentID != "" {
			if _, found := translator.agentTurns[translated.AgentID]; !found {
				translator.agentTurns[translated.AgentID] = *translated.TurnID
			}
		}
	case *rpc.SubagentCompletedData:
		translated.TurnID = translator.turnForTool(data.ToolCallID, translated.TurnID)
		delete(translator.toolTurns, copilotadapter.ToolCallID(data.ToolCallID))
		delete(translator.subagentTools, copilotadapter.ToolCallID(data.ToolCallID))
	case *rpc.SubagentFailedData:
		translated.TurnID = translator.turnForTool(data.ToolCallID, translated.TurnID)
		delete(translator.toolTurns, copilotadapter.ToolCallID(data.ToolCallID))
		delete(translator.subagentTools, copilotadapter.ToolCallID(data.ToolCallID))
	case *rpc.AssistantIntentData:
		if identifier, found := translator.agentTurns[translated.AgentID]; found {
			translated.TurnID = &identifier
		}
	}
}

func (translator *eventTranslator) turnForToolPointer(toolCallID *string, fallback *uuid.UUID) *uuid.UUID {
	if toolCallID == nil {
		return fallback
	}
	return translator.turnForTool(*toolCallID, fallback)
}

func (translator *eventTranslator) turnForTool(toolCallID string, fallback *uuid.UUID) *uuid.UUID {
	identifier, found := translator.toolTurns[copilotadapter.ToolCallID(toolCallID)]
	if !found {
		return fallback
	}
	return &identifier
}

func (translator *eventTranslator) clearCorrelationHistory() {
	translator.toolNames = map[copilotadapter.ToolCallID]copilotadapter.ToolName{}
	translator.toolTurns = map[copilotadapter.ToolCallID]uuid.UUID{}
	translator.subagentTools = map[copilotadapter.ToolCallID]struct{}{}
	translator.agentTurns = map[string]uuid.UUID{}
	translator.messageTurns = map[string]uuid.UUID{}
	translator.pendingDeltas = map[string][]copilotadapter.BridgeEvent{}
}

// translateEvent is the pure mapping the unit specs cover. toolNames carries
// tool call ids to names so a result can name the tool that produced it; the
// call id itself is carried on the event so the pairing never depends on the
// name being unique among the calls in flight.
func translateEvent(event copilot.SessionEvent, toolNames map[copilotadapter.ToolCallID]copilotadapter.ToolName, turns *turnCorrelator) (copilotadapter.BridgeEvent, bool) {
	events := translateEvents(event, toolNames, turns)
	if len(events) != 1 {
		return copilotadapter.BridgeEvent{}, false
	}
	return events[0], true
}

func translateEvents(event copilot.SessionEvent, toolNames map[copilotadapter.ToolCallID]copilotadapter.ToolName, turns *turnCorrelator) []copilotadapter.BridgeEvent {
	occurredAt := event.Timestamp
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	agentID := ""
	if event.AgentID != nil {
		agentID = *event.AgentID
	}
	switch data := event.Data.(type) {
	case *rpc.AssistantUsageData:
		usage := usageViews.ModelCall(*data)
		usage.Kind = api.ModelCall
		if data.CopilotUsage != nil {
			usage.NanoAiu = &data.CopilotUsage.TotalNanoAiu
		}
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventUsage, OccurredAt: occurredAt, Usage: &usage}}
	case *rpc.SessionUsageCheckpointData:
		usage := usageViews.Checkpoint(*data)
		usage.Kind = api.SessionCheckpoint
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventUsage, OccurredAt: occurredAt, Usage: &usage}}

	case *rpc.AssistantIntentData:
		if agentID == "" {
			return nil
		}
		return []copilotadapter.BridgeEvent{{
			Kind: copilotadapter.BridgeEventSubagentProgress, OccurredAt: occurredAt,
			AgentID: agentID, Text: data.Intent, TurnID: activeTurn(turns),
		}}
	case *rpc.SubagentStartedData:
		return []copilotadapter.BridgeEvent{{
			Kind: copilotadapter.BridgeEventSubagentStarted, OccurredAt: occurredAt,
			AgentID:     agentIdentifier(agentID, data.AgentName, data.ToolCallID),
			DisplayName: data.AgentDisplayName, Text: data.AgentDescription, TurnID: activeTurn(turns),
		}}
	case *rpc.SubagentCompletedData:
		return []copilotadapter.BridgeEvent{{
			Kind: copilotadapter.BridgeEventSubagentCompleted, OccurredAt: occurredAt,
			AgentID:     agentIdentifier(agentID, data.AgentName, data.ToolCallID),
			DisplayName: data.AgentDisplayName, TurnID: activeTurn(turns),
		}}
	case *rpc.SubagentFailedData:
		return []copilotadapter.BridgeEvent{{
			Kind: copilotadapter.BridgeEventSubagentFailed, OccurredAt: occurredAt,
			AgentID:     agentIdentifier(agentID, data.AgentName, data.ToolCallID),
			DisplayName: data.AgentDisplayName, Text: data.Error, TurnID: activeTurn(turns),
		}}
	case *rpc.AssistantTurnStartData:
		if agentID != "" || turns == nil {
			return nil
		}
		return projectTurnTransition(turns.start(stringPointerValue(data.InteractionID), data.TurnID), occurredAt)
	case *rpc.AssistantTurnEndData:
		if agentID == "" && turns != nil {
			turns.end(data.TurnID)
		}
		return nil
	case *rpc.UserMessageData:
		if agentID != "" || turns == nil || !adapterSubmittedUserMessage(data) {
			return nil
		}
		return projectTurnTransition(turns.observeUserMessage(
			stringPointerValue(data.InteractionID), stringPointerValue(data.Delivery),
		), occurredAt)
	case *rpc.AssistantMessageDeltaData:
		if agentID != "" {
			return nil
		}
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventAssistantDelta, OccurredAt: occurredAt, Text: data.DeltaContent, Author: copilotAuthor, TurnID: activeTurn(turns)}}
	case *rpc.AssistantMessageData:
		if agentID != "" {
			return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventSubagentMessage, OccurredAt: occurredAt, AgentID: agentID, Text: textField(data, assistantMessageContentField), Author: copilotAuthor, TurnID: turnForSDK(turns, data.InteractionID, data.TurnID)}}
		}
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventAssistantMessage, OccurredAt: occurredAt, Text: textField(data, assistantMessageContentField), Author: copilotAuthor, TurnID: turnForSDK(turns, data.InteractionID, data.TurnID)}}
	case *rpc.ToolExecutionStartData:
		toolNames[copilotadapter.ToolCallID(data.ToolCallID)] = copilotadapter.ToolName(data.ToolName)
		arguments, _ := json.Marshal(data.Arguments)
		if agentID != "" {
			return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventSubagentToolCall, OccurredAt: occurredAt, AgentID: agentID, ToolName: copilotadapter.ToolName(data.ToolName), ToolCallID: copilotadapter.ToolCallID(data.ToolCallID), Text: string(arguments), Author: copilotAuthor, TurnID: turnForSDK(turns, nil, data.TurnID)}}
		}
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventToolCall, OccurredAt: occurredAt, ToolName: copilotadapter.ToolName(data.ToolName), ToolCallID: copilotadapter.ToolCallID(data.ToolCallID), Text: string(arguments), Author: copilotAuthor, TurnID: turnForSDK(turns, nil, data.TurnID)}}
	case *rpc.ToolExecutionCompleteData:
		name := toolNames[copilotadapter.ToolCallID(data.ToolCallID)]
		delete(toolNames, copilotadapter.ToolCallID(data.ToolCallID))
		result, _ := json.Marshal(data.Result)
		if agentID != "" {
			return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventSubagentToolResult, OccurredAt: occurredAt, AgentID: agentID, ToolName: name, ToolCallID: copilotadapter.ToolCallID(data.ToolCallID), Text: string(result), Author: copilotAuthor, TurnID: turnForSDK(turns, data.InteractionID, data.TurnID)}}
		}
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventToolResult, OccurredAt: occurredAt, ToolName: name, ToolCallID: copilotadapter.ToolCallID(data.ToolCallID), Text: string(result), Author: copilotAuthor, TurnID: turnForSDK(turns, data.InteractionID, data.TurnID)}}
	case *rpc.SessionIdleData:
		if agentID != "" || turns == nil {
			return nil
		}
		return projectTurnTransition(turns.idle(), occurredAt)
	case *rpc.AbortData:
		if agentID != "" || turns == nil {
			return nil
		}
		identifier, sessionBusy, found := turns.terminateAborted()
		if !found {
			return nil
		}
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventTurnAborted, OccurredAt: occurredAt, TurnID: &identifier, SessionBusy: sessionBusy}}
	case *rpc.SessionErrorData:
		// EligibleForAutoSwitch only predicts a follow-up request when the SDK
		// session opts into OnAutoModeSwitchRequest. The adapter has no durable
		// auto-switch request contract, so the error remains terminal here.
		// A subagent error has its own canonical subagent.failed lifecycle event;
		// it must not consume the root turn that happens to be active beside it.
		if agentID != "" || turns == nil {
			return nil
		}
		identifier, sessionBusy, found := turns.terminateActive()
		if !found {
			return nil
		}
		return []copilotadapter.BridgeEvent{{Kind: copilotadapter.BridgeEventTurnFailed, OccurredAt: occurredAt, TurnID: &identifier, SessionBusy: sessionBusy, Text: textField(data, sessionErrorMessageField)}}
	case *rpc.SessionShutdownData:
		var results []copilotadapter.BridgeEvent
		if data.TotalPremiumRequests != nil || data.TotalNanoAiu != nil {
			usage := usageViews.Shutdown(*data)
			usage.Kind = api.SessionCheckpoint
			results = append(results, copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventUsage, OccurredAt: occurredAt, Usage: &usage})
		}
		if data.ShutdownType == rpc.ShutdownTypeError {
			reason := ""
			if data.ErrorReason != nil {
				reason = *data.ErrorReason
			}
			results = append(results, copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventFailed, OccurredAt: occurredAt, Text: reason})
		}
		return results
	}
	return nil
}

func projectTurnTransition(transition turnTransition, occurredAt time.Time) []copilotadapter.BridgeEvent {
	events := make([]copilotadapter.BridgeEvent, 0, len(transition.completed)+len(transition.started))
	for _, identifier := range transition.completed {
		turnID := identifier
		events = append(events, copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventTurnCompleted, OccurredAt: occurredAt,
			TurnID: &turnID, SessionBusy: transition.sessionBusy,
		})
	}
	for _, identifier := range transition.started {
		turnID := identifier
		events = append(events, copilotadapter.BridgeEvent{
			Kind: copilotadapter.BridgeEventTurnStarted, OccurredAt: occurredAt, TurnID: &turnID,
		})
	}
	return events
}

func adapterSubmittedUserMessage(data *rpc.UserMessageData) bool {
	if data.IsAutopilotContinuation != nil && *data.IsAutopilotContinuation {
		return false
	}
	return data.Source == nil || *data.Source == ""
}

func stringPointerValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func agentIdentifier(fromEvent string, name string, toolCallID string) string {
	if fromEvent != "" {
		return fromEvent
	}
	if toolCallID != "" {
		return toolCallID
	}
	return name
}

func activeTurn(turns *turnCorrelator) *uuid.UUID {
	if turns == nil {
		return nil
	}
	identifier, found := turns.active()
	if !found {
		return nil
	}
	return &identifier
}

func turnForSDK(turns *turnCorrelator, interactionID *string, sdkTurnID *string) *uuid.UUID {
	if turns == nil {
		return nil
	}
	if sdkTurnID == nil {
		return activeTurn(turns)
	}
	identifier, found := turns.lookup(stringPointerValue(interactionID), *sdkTurnID)
	if !found {
		return nil
	}
	return &identifier
}

// textField reads one string field off an SDK payload by its JSON name, so
// the mapping does not pin the SDK's Go field names.
func textField(data any, name string) string {
	body, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	fields := map[string]any{}
	if err := json.Unmarshal(body, &fields); err != nil {
		return ""
	}
	value, _ := fields[name].(string)
	return value
}

// IsolatedBuiltInToolNames is the one set of tool names the SDK exports
// (copilot.BuiltInToolsIsolated: the built-ins that act only inside the
// session, ask_user among them), re-exported typed and derived at init rather
// than retyped here, so it moves with the SDK version.
var IsolatedBuiltInToolNames = func() []copilotadapter.ToolName {
	names := make([]copilotadapter.ToolName, 0, len(copilot.BuiltInToolsIsolated))
	for _, name := range copilot.BuiltInToolsIsolated {
		names = append(names, copilotadapter.ToolName(name))
	}
	return names
}()
