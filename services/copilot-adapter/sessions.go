package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

type liveSession struct {
	handle BridgeSession
	cancel context.CancelFunc
}

var errInvalidBridgeEvent = errors.New("copilot-adapter: invalid bridge event")
var errStartingBridgeSession = errors.New("copilot-adapter: bridge session is still starting")

const (
	turnDeliveryAccepted = "accepted"
	turnDeliveryUnknown  = "unknown"
	turnDeliveryPending  = "pending"
)

// sessionRegistry owns live CLI handles on one goroutine. Shutdown is an
// owner-consumed signal; callers never close the command channel, so a lookup
// racing Close cannot send on a closed channel.
type sessionRegistry struct {
	commands      chan func(live map[uuid.UUID]liveSession)
	stopRequested chan struct{}
	stopped       chan struct{}
	closeTimeout  time.Duration
	stopOnce      sync.Once
}

func newSessionRegistry(closeTimeout time.Duration) *sessionRegistry {
	registry := &sessionRegistry{
		commands:      make(chan func(live map[uuid.UUID]liveSession)),
		stopRequested: make(chan struct{}),
		stopped:       make(chan struct{}),
		closeTimeout:  closeTimeout,
	}
	go registry.loop()
	return registry
}

func (registry *sessionRegistry) loop() {
	defer close(registry.stopped)
	live := map[uuid.UUID]liveSession{}
	for {
		select {
		case <-registry.stopRequested:
			closeContext, cancelClose := context.WithTimeout(context.Background(), registry.closeTimeout)
			defer cancelClose()
			for identifier, session := range live {
				if session.cancel != nil {
					session.cancel()
				}
				if session.handle.Close != nil {
					_ = session.handle.Close(closeContext)
				}
				delete(live, identifier)
			}
			return
		case command := <-registry.commands:
			command(live)
		}
	}
}

func (registry *sessionRegistry) run(command func(live map[uuid.UUID]liveSession)) bool {
	done := make(chan struct{})
	wrapper := func(live map[uuid.UUID]liveSession) {
		command(live)
		close(done)
	}
	select {
	case <-registry.stopRequested:
		return false
	case registry.commands <- wrapper:
	}
	select {
	case <-done:
		return true
	case <-registry.stopped:
		return false
	}
}

func (registry *sessionRegistry) register(identifier uuid.UUID, session liveSession) bool {
	return registry.run(func(live map[uuid.UUID]liveSession) { live[identifier] = session })
}

func (registry *sessionRegistry) lookup(identifier uuid.UUID) (BridgeSession, bool) {
	var handle BridgeSession
	found := false
	registry.run(func(live map[uuid.UUID]liveSession) {
		session, exists := live[identifier]
		if exists {
			handle, found = session.handle, true
		}
	})
	return handle, found
}

func (registry *sessionRegistry) remove(identifier uuid.UUID) (BridgeSession, bool) {
	var handle BridgeSession
	found := false
	registry.run(func(live map[uuid.UUID]liveSession) {
		session, exists := live[identifier]
		if !exists {
			return
		}
		if session.cancel != nil {
			session.cancel()
		}
		handle, found = session.handle, true
		delete(live, identifier)
	})
	return handle, found
}

func (registry *sessionRegistry) stop() {
	registry.stopOnce.Do(func() { close(registry.stopRequested) })
	<-registry.stopped
}

// Close ends every live CLI session and stops the registry. Run does not call
// it: the binary that owns the process decides when the sessions die.
func (adapter *CopilotAdapter) Close() error {
	adapter.sessions.stop()
	return adapter.terminals.Close()
}

var errNoLiveSession = errors.New("copilot-adapter: the session has no live CLI handle in this process")

// attach runs only after the worktree and session transaction committed. A
// bridge may already have buffered events; every projection therefore finds
// the session and its counter row.
func (adapter *CopilotAdapter) attach(sessionID uuid.UUID, handle BridgeSession) {
	ctx, cancel := context.WithCancel(context.Background())
	if !adapter.sessions.register(sessionID, liveSession{handle: handle, cancel: cancel}) {
		cancel()
		if handle.Close != nil {
			closeContext, cancelClose := adapter.durableTransitionContext()
			defer cancelClose()
			_ = handle.Close(closeContext)
		}
		return
	}
	if handle.Events != nil {
		go func() {
			if handle.UsageHistory != nil {
				historyContext, stop := context.WithTimeout(ctx, time.Duration(adapter.config.GetDurableTransitionTimeoutMillis())*time.Millisecond)
				history, err := handle.UsageHistory(historyContext)
				stop()
				if err != nil {
					adapter.logger.Warn("provider usage history unavailable", "sessionId", sessionID, "error", err)
				} else {
					for _, event := range history {
						if event.Kind != BridgeEventUsage {
							continue
						}
						if !adapter.retryProjection(ctx, sessionID, event.Kind, func() error { return adapter.projectEvent(ctx, sessionID, event) }) {
							return
						}
					}
				}
			}
			adapter.projectEvents(ctx, sessionID, handle.Events)
		}()
	}
}

func (adapter *CopilotAdapter) projectEvents(ctx context.Context, sessionID uuid.UUID, events <-chan BridgeEvent) {
	pending := make([]BridgeEvent, 0, adapter.assistantDeltaMaxEvents())
	pendingBytes := 0
	var flushTimer *time.Timer
	var flush <-chan time.Time
	stopTimer := func() {
		if flushTimer != nil && !flushTimer.Stop() {
			select {
			case <-flushTimer.C:
			default:
			}
		}
		flushTimer = nil
		flush = nil
	}
	flushPending := func() bool {
		if len(pending) == 0 {
			return true
		}
		batch := append([]BridgeEvent(nil), pending...)
		pending = pending[:0]
		pendingBytes = 0
		stopTimer()
		return adapter.retryProjection(ctx, sessionID, BridgeEventAssistantDelta, func() error {
			return adapter.projectAssistantDeltas(ctx, sessionID, batch)
		})
	}
	defer stopTimer()
	for {
		select {
		case <-ctx.Done():
			// The live handle owns this projector. Cancellation deliberately
			// discards only the declared in-memory preview window; no detached
			// flush may race handle shutdown, and AssistantMessage is canonical.
			return
		case <-flush:
			if !flushPending() {
				return
			}
		case event, open := <-events:
			if !open {
				flushPending()
				return
			}
			event = normalizeBridgeEvent(event)
			oversizedDelta := event.Kind == BridgeEventAssistantDelta && len(event.Text) > adapter.assistantDeltaSourceMaxBytes()
			if event.Kind != BridgeEventAssistantDelta || event.TurnID == nil || !utf8.ValidString(event.Text) || oversizedDelta {
				if !flushPending() {
					return
				}
				if event.Kind == BridgeEventAssistantDelta && !utf8.ValidString(event.Text) {
					adapter.logger.Warn("discard invalid copilot delta", "sessionId", sessionID, "error", "text is not valid UTF-8")
					continue
				}
				if oversizedDelta {
					adapter.logger.Warn("discard oversized copilot delta", "sessionId", sessionID,
						"bytes", len(event.Text), "maxBytes", adapter.assistantDeltaSourceMaxBytes())
					continue
				}
				if !adapter.retryProjection(ctx, sessionID, event.Kind, func() error {
					return adapter.projectEvent(ctx, sessionID, event)
				}) {
					return
				}
				continue
			}
			if len(pending) > 0 && *pending[0].TurnID != *event.TurnID {
				if !flushPending() {
					return
				}
			}
			if len(event.Text) > adapter.assistantDeltaMaxBytes() {
				if !flushPending() || !adapter.retryProjection(ctx, sessionID, event.Kind, func() error {
					return adapter.projectAssistantDeltas(ctx, sessionID, []BridgeEvent{event})
				}) {
					return
				}
				continue
			}
			if len(pending) > 0 && (pendingBytes+len(event.Text) > adapter.assistantDeltaMaxBytes() ||
				len(pending) == adapter.assistantDeltaMaxEvents()) {
				if !flushPending() {
					return
				}
			}
			pending = append(pending, event)
			pendingBytes += len(event.Text)
			if len(pending) == 1 {
				flushTimer = time.NewTimer(adapter.assistantDeltaFlushInterval())
				flush = flushTimer.C
			}
			if pendingBytes == adapter.assistantDeltaMaxBytes() || len(pending) == adapter.assistantDeltaMaxEvents() {
				if !flushPending() {
					return
				}
			}
		}
	}
}

func normalizeBridgeEvent(event BridgeEvent) BridgeEvent {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	return event
}

func (adapter *CopilotAdapter) retryProjection(
	ctx context.Context,
	sessionID uuid.UUID,
	kind BridgeEventKind,
	projection func() error,
) bool {
	for {
		err := projection()
		if err == nil {
			return true
		}
		if errors.Is(err, errInvalidBridgeEvent) {
			adapter.logger.Warn("discard invalid copilot event", "sessionId", sessionID, "kind", kind, "error", err)
			return true
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return false
		}
		adapter.logger.Warn("retry copilot event projection", "sessionId", sessionID, "kind", kind, "error", err)
		retry := time.NewTimer(adapter.eventStreamPoll())
		select {
		case <-ctx.Done():
			retry.Stop()
			return false
		case <-retry.C:
		}
	}
}

// projectAssistantDeltas claims every original source receipt and writes the
// resulting bounded chunks in one transaction. Ambiguous-commit retry sees
// the receipts and emits nothing; a mixed replay writes only unseen text.
func (adapter *CopilotAdapter) projectAssistantDeltas(
	ctx context.Context,
	sessionID uuid.UUID,
	events []BridgeEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	turnID := events[0].TurnID
	if turnID == nil {
		return fmt.Errorf("%w: assistant delta has no correlated turn id", errInvalidBridgeEvent)
	}
	for _, event := range events {
		if event.ID == "" || event.Kind != BridgeEventAssistantDelta || event.TurnID == nil ||
			*event.TurnID != *turnID || !utf8.ValidString(event.Text) ||
			len(event.Text) > adapter.assistantDeltaSourceMaxBytes() {
			return fmt.Errorf("%w: malformed assistant delta batch", errInvalidBridgeEvent)
		}
	}
	return adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		session, err := queries.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if session.Status == string(api.SessionStatusStarting) {
			return errStartingBridgeSession
		}
		if session.Status == string(api.SessionStatusEnded) || session.Status == string(api.SessionStatusFailed) {
			return nil
		}
		var text strings.Builder
		newReceipts := 0
		occurredAt := events[0].OccurredAt
		for _, event := range events {
			_, claimErr := queries.ClaimBridgeEventProjection(ctx, storedb.ClaimBridgeEventProjectionParams{
				SessionID: sessionID, EventID: event.ID, ProjectedAt: event.OccurredAt,
			})
			if errors.Is(claimErr, sql.ErrNoRows) {
				continue
			}
			if claimErr != nil {
				return claimErr
			}
			newReceipts++
			text.WriteString(event.Text)
			occurredAt = event.OccurredAt
		}
		if newReceipts == 0 {
			return nil
		}
		for _, chunk := range splitUTF8(text.String(), adapter.assistantDeltaMaxBytes()) {
			if err := insertEvent(ctx, queries, storedb.InsertSessionEventParams{
				SessionID: sessionID, Kind: string(api.SessionEventKindAssistantDelta), OccurredAt: occurredAt,
				TurnID: turnID, DeltaText: chunk,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func splitUTF8(value string, maximumBytes int) []string {
	if len(value) <= maximumBytes {
		return []string{value}
	}
	chunks := make([]string, 0, len(value)/maximumBytes+1)
	for len(value) > maximumBytes {
		end := maximumBytes
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		chunks = append(chunks, value[:end])
		value = value[end:]
	}
	return append(chunks, value)
}

// projectEvent stores domain facts and the event reference that announces
// them in one transaction. Session events never persist generated wire JSON.
func (adapter *CopilotAdapter) projectEvent(ctx context.Context, sessionID uuid.UUID, event BridgeEvent) error {
	if event.ID == "" {
		return fmt.Errorf("%w: bridge event has no source id", errInvalidBridgeEvent)
	}
	if event.Kind == BridgeEventFailed {
		unlockScheduleControl := adapter.scheduleControls.lock(sessionID)
		defer unlockScheduleControl()
	}
	if event.Kind == BridgeEventFailed || event.Kind == BridgeEventTurnAborted || event.Kind == BridgeEventRequestCompleted {
		unlock := adapter.mutations.lock(sessionID)
		defer unlock()
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	deliveryAcceptanceDurable := false
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		session, err := queries.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if session.Status == string(api.SessionStatusStarting) {
			return errStartingBridgeSession
		}
		if (session.Status == string(api.SessionStatusEnded) || session.Status == string(api.SessionStatusFailed)) &&
			event.Kind != BridgeEventTurnAborted && event.Kind != BridgeEventUsage {
			return nil
		}
		if event.Kind == BridgeEventUsage {
			return projectUsage(ctx, queries, sessionID, event, occurredAt)
		}
		if _, err = queries.ClaimBridgeEventProjection(ctx, storedb.ClaimBridgeEventProjectionParams{
			SessionID: sessionID, EventID: event.ID, ProjectedAt: occurredAt,
		}); errors.Is(err, sql.ErrNoRows) {
			if event.Kind == BridgeEventTurnAborted {
				_, _, err = finalizeAbortedTurn(ctx, queries, sessionID, event.TurnID, occurredAt)
				return err
			}
			deliveryAcceptanceDurable = event.Kind == BridgeEventTurnStarted && event.TurnID != nil
			return nil
		} else if err != nil {
			return err
		}
		switch event.Kind {
		case BridgeEventTurnStarted:
			err := projectTurnState(ctx, queries, sessionID, event.TurnID, occurredAt, api.TurnStatusRunning, api.SessionEventKindTurnStarted)
			deliveryAcceptanceDurable = err == nil
			return err
		case BridgeEventTurnCompleted:
			return projectTurnState(ctx, queries, sessionID, event.TurnID, occurredAt, api.TurnStatusCompleted, api.SessionEventKindTurnCompleted)
		case BridgeEventTurnAborted:
			_, _, err := finalizeAbortedTurn(ctx, queries, sessionID, event.TurnID, occurredAt)
			return err
		case BridgeEventTurnFailed:
			return projectTurnState(ctx, queries, sessionID, event.TurnID, occurredAt, api.TurnStatusFailed, api.SessionEventKindTurnCompleted)
		case BridgeEventAssistantMessage, BridgeEventToolCall, BridgeEventToolResult:
			return projectTranscript(ctx, queries, sessionID, event, occurredAt)
		case BridgeEventAssistantDelta:
			if event.TurnID == nil {
				return nil
			}
			return insertEvent(ctx, queries, storedb.InsertSessionEventParams{
				SessionID: sessionID, Kind: string(api.SessionEventKindAssistantDelta), OccurredAt: occurredAt,
				TurnID: event.TurnID, DeltaText: event.Text,
			})
		case BridgeEventRequestOpened:
			return projectRequest(ctx, queries, sessionID, event, occurredAt)
		case BridgeEventRequestCompleted:
			return projectRequestCompletion(ctx, queries, sessionID, event, occurredAt)
		case BridgeEventSubagentStarted, BridgeEventSubagentCompleted, BridgeEventSubagentFailed:
			return projectSubagentLifecycle(ctx, queries, sessionID, event, occurredAt)
		case BridgeEventSubagentProgress, BridgeEventSubagentMessage, BridgeEventSubagentToolCall, BridgeEventSubagentToolResult:
			return projectSubagentActivity(ctx, queries, sessionID, event, occurredAt)
		case BridgeEventFailed:
			if err := finalizeSessionActivity(ctx, queries, sessionID, occurredAt); err != nil {
				return err
			}
			if _, err := queries.PauseSessionSchedules(ctx, storedb.PauseSessionSchedulesParams{
				UpdatedAt: occurredAt, SessionID: sessionID,
			}); err != nil {
				return err
			}
			if _, err := queries.MarkSessionEnded(ctx, storedb.MarkSessionEndedParams{
				ID: sessionID, Status: string(api.SessionStatusFailed),
				EndedAt: null.TimeFrom(occurredAt), UpdatedAt: occurredAt,
			}); err != nil {
				return err
			}
			return insertSessionUpdatedEvent(ctx, queries, sessionID, occurredAt)
		default:
			return nil
		}
	})
	if err != nil {
		if event.Kind == BridgeEventTurnAborted && errors.Is(err, ErrAbortTargetMismatch) {
			return fmt.Errorf("%w: bridge abort target belongs to another session", errInvalidBridgeEvent)
		}
		return err
	}
	if deliveryAcceptanceDurable {
		turn, lookupErr := adapter.store.GetTurn(ctx, *event.TurnID)
		if lookupErr != nil {
			return fmt.Errorf("confirm durable turn-start delivery: %w", lookupErr)
		}
		if turn.SessionID != sessionID {
			return fmt.Errorf("%w: projected bridge turn belongs to another session", errInvalidBridgeEvent)
		}
		handle, live := adapter.sessions.lookup(sessionID)
		if turn.DeliveryStatus == turnDeliveryAccepted && live && handle.AcknowledgeDelivery != nil {
			handle.AcknowledgeDelivery(*event.TurnID)
		}
	}
	if event.Kind == BridgeEventFailed {
		adapter.reloadSchedules()
		adapter.detachUnusableLiveSession(sessionID)
	}
	if event.Kind == BridgeEventTurnAborted && event.TurnID != nil {
		if err := adapter.abandonTurnResolutionCallbacks(sessionID, *event.TurnID); err != nil {
			return err
		}
	}
	if event.Kind == BridgeEventRequestCompleted && event.RequestID != nil {
		if handle, live := adapter.sessions.lookup(sessionID); live && handle.AcknowledgeResolution != nil {
			handle.AcknowledgeResolution(*event.RequestID)
		}
	}
	return nil
}

func projectTurnState(
	ctx context.Context,
	queries storedb.Querier,
	sessionID uuid.UUID,
	turnID *uuid.UUID,
	occurredAt time.Time,
	status api.TurnStatus,
	eventKind api.SessionEventKind,
) error {
	if turnID == nil {
		return fmt.Errorf("%w: turn event has no correlated turn id", errInvalidBridgeEvent)
	}
	turn, err := queries.GetTurn(ctx, *turnID)
	if err != nil {
		return err
	}
	if turn.SessionID != sessionID {
		return fmt.Errorf("%w: bridge turn belongs to another session", errInvalidBridgeEvent)
	}
	if status == api.TurnStatusRunning {
		_, err = queries.MarkTurnRunning(ctx, *turnID)
	} else {
		_, err = queries.FinalizeTurn(ctx, storedb.FinalizeTurnParams{
			ID: *turnID, Status: string(status), CompletedAt: null.TimeFrom(occurredAt),
		})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status == api.TurnStatusRunning {
		if _, err := queries.RecordTurnStarted(ctx, storedb.RecordTurnStartedParams{TurnID: *turnID, StartedAt: occurredAt}); err != nil {
			return err
		}
	}
	activeTurns, err := queries.CountActiveSessionTurns(ctx, sessionID)
	if err != nil {
		return err
	}
	sessionStatus := api.SessionStatusIdle
	if activeTurns > 0 {
		sessionStatus = api.SessionStatusRunning
	}
	if _, err := queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
		ID: sessionID, Status: string(sessionStatus), UpdatedAt: occurredAt,
	}); err != nil {
		return err
	}
	if err := insertTurnEvent(ctx, queries, sessionID, *turnID, eventKind, occurredAt); err != nil {
		return err
	}
	return insertSessionUpdatedEvent(ctx, queries, sessionID, occurredAt)
}

var transcriptKinds = map[BridgeEventKind]api.TranscriptItemKind{
	BridgeEventAssistantMessage: api.TranscriptItemKindAssistantMessage,
	BridgeEventToolCall:         api.TranscriptItemKindToolCall,
	BridgeEventToolResult:       api.TranscriptItemKindToolResult,
}

func projectTranscript(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, event BridgeEvent, occurredAt time.Time) error {
	return insertTranscriptItem(ctx, queries, storedb.InsertTranscriptItemParams{
		SessionID: sessionID, TurnID: event.TurnID, Kind: string(transcriptKinds[event.Kind]),
		OccurredAt: occurredAt,
		Author:     null.NewString(event.Author, event.Author != ""),
		ToolName:   null.NewString(string(event.ToolName), event.ToolName != ""),
		ToolCallID: null.NewString(string(event.ToolCallID), event.ToolCallID != ""), Body: event.Text,
	})
}

func insertTranscriptItem(ctx context.Context, queries storedb.Querier, item storedb.InsertTranscriptItemParams) error {
	seq, err := queries.AllocateTranscriptSeq(ctx, item.SessionID)
	if err != nil {
		return err
	}
	item.Seq = int64(seq)
	if _, err := queries.InsertTranscriptItem(ctx, item); err != nil {
		return err
	}
	return insertEvent(ctx, queries, storedb.InsertSessionEventParams{
		SessionID: item.SessionID, Kind: string(api.SessionEventKindTranscriptAppended), OccurredAt: item.OccurredAt,
		TranscriptSeq: null.IntFrom(int64(seq)),
	})
}

var requestKinds = map[BridgeRequestKind]api.SessionRequestKind{
	BridgeRequestPermission: api.Permission,
}

func projectRequest(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, event BridgeEvent, occurredAt time.Time) error {
	if event.RequestID == nil || event.RequestKind != BridgeRequestPermission {
		return fmt.Errorf("%w: permission request has no exact identity or unsupported kind", errInvalidBridgeEvent)
	}
	identifier := *event.RequestID
	requestKind := requestKinds[event.RequestKind]
	if _, err := queries.InsertSessionRequest(ctx, storedb.InsertSessionRequestParams{
		ID: identifier, SessionID: sessionID, TurnID: event.TurnID, Kind: string(requestKind),
		Status: string(api.Pending), Prompt: event.Text,
		ToolName: null.NewString(string(event.ToolName), event.ToolName != ""), CreatedAt: occurredAt,
	}); err != nil {
		return err
	}
	return insertRequestEvent(ctx, queries, sessionID, identifier, api.SessionEventKindRequestOpened, occurredAt)
}

func projectRequestCompletion(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, event BridgeEvent, occurredAt time.Time) error {
	if event.RequestID == nil {
		return fmt.Errorf("%w: permission completion has no exact request identity", errInvalidBridgeEvent)
	}
	status, found := map[string]string{
		string(api.Approve): string(api.Approved),
		string(api.Deny):    string(api.Denied),
	}[event.ResolutionDecision]
	if !found {
		return fmt.Errorf("%w: permission completion has unsupported decision", errInvalidBridgeEvent)
	}
	request, err := queries.GetSessionRequest(ctx, *event.RequestID)
	if err != nil {
		return err
	}
	if request.SessionID != sessionID || request.Kind != string(api.Permission) {
		return fmt.Errorf("%w: permission completion belongs to another session or request kind", errInvalidBridgeEvent)
	}
	if request.Status != string(api.Pending) {
		if request.DeliveryStatus == "abandoned" || sameRequestResolution(request, event.ResolutionDecision, status) {
			return nil
		}
		return fmt.Errorf("%w: permission completion conflicts with durable resolution", errInvalidBridgeEvent)
	}
	resolved, err := queries.CompleteExternalSessionRequestResolution(ctx, storedb.CompleteExternalSessionRequestResolutionParams{
		ID: *event.RequestID, Status: status, Decision: event.ResolutionDecision, ResolvedAt: null.TimeFrom(occurredAt),
	})
	if err != nil {
		return err
	}
	if err := insertDeniedRequestNotice(ctx, queries, resolved, occurredAt); err != nil {
		return err
	}
	return insertRequestEvent(ctx, queries, sessionID, resolved.ID, api.SessionEventKindRequestResolved, occurredAt)
}

func insertDeniedRequestNotice(
	ctx context.Context,
	queries storedb.Querier,
	request storedb.PendingRequest,
	occurredAt time.Time,
) error {
	if request.Status != string(api.Denied) {
		return nil
	}
	text := "Permission denied."
	if toolName := strings.TrimSpace(request.ToolName.String); toolName != "" {
		text = fmt.Sprintf("Permission denied for %s.", toolName)
	}
	return insertTranscriptItem(ctx, queries, storedb.InsertTranscriptItemParams{
		SessionID: request.SessionID, TurnID: request.TurnID,
		Kind: string(api.TranscriptItemKindSystemNotice), OccurredAt: occurredAt, Body: text,
	})
}

func projectSubagentLifecycle(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, event BridgeEvent, occurredAt time.Time) error {
	if event.AgentID == "" {
		return fmt.Errorf("%w: subagent event has no agent id", errInvalidBridgeEvent)
	}
	status := api.SubagentStatusActive
	activityKind := api.SubagentActivityKindStarted
	completedAt := null.Time{}
	switch event.Kind {
	case BridgeEventSubagentCompleted:
		status, activityKind, completedAt = api.SubagentStatusCompleted, api.SubagentActivityKindCompleted, null.TimeFrom(occurredAt)
	case BridgeEventSubagentFailed:
		status, activityKind, completedAt = api.SubagentStatusFailed, api.SubagentActivityKindFailed, null.TimeFrom(occurredAt)
	}
	if _, err := queries.UpsertSubagent(ctx, storedb.UpsertSubagentParams{
		SessionID: sessionID, ID: event.AgentID, TurnID: event.TurnID, DisplayName: event.DisplayName,
		Status: string(status), Summary: null.NewString(event.Text, event.Text != ""), StartedAt: occurredAt, UpdatedAt: occurredAt,
		CompletedAt: completedAt,
	}); err != nil {
		return err
	}
	if err := appendSubagentActivity(ctx, queries, sessionID, event, occurredAt, activityKind); err != nil {
		return err
	}
	return insertSubagentEvent(ctx, queries, sessionID, event.AgentID, occurredAt)
}

var subagentActivityKinds = map[BridgeEventKind]api.SubagentActivityKind{
	BridgeEventSubagentProgress:   api.SubagentActivityKindProgress,
	BridgeEventSubagentMessage:    api.SubagentActivityKindMessage,
	BridgeEventSubagentToolCall:   api.SubagentActivityKindToolCall,
	BridgeEventSubagentToolResult: api.SubagentActivityKindToolResult,
}

func projectSubagentActivity(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, event BridgeEvent, occurredAt time.Time) error {
	if event.AgentID == "" {
		return fmt.Errorf("%w: subagent activity has no agent id", errInvalidBridgeEvent)
	}
	if _, err := queries.UpsertSubagent(ctx, storedb.UpsertSubagentParams{
		SessionID: sessionID, ID: event.AgentID, TurnID: event.TurnID, DisplayName: event.DisplayName,
		Status:  string(api.SubagentStatusActive),
		Summary: null.NewString(event.Text, event.Text != ""), StartedAt: occurredAt, UpdatedAt: occurredAt,
	}); err != nil {
		return err
	}
	if err := appendSubagentActivity(ctx, queries, sessionID, event, occurredAt, subagentActivityKinds[event.Kind]); err != nil {
		return err
	}
	return insertSubagentEvent(ctx, queries, sessionID, event.AgentID, occurredAt)
}

// failTurnDelivery is the one durable transition for a prompt proved not to
// have crossed the Copilot SDK boundary. It is shared by an immediate rejected
// Send and restart reconciliation of a prepared, still-pending delivery.
func (adapter *CopilotAdapter) failTurnDelivery(ctx context.Context, sessionID uuid.UUID, turnID uuid.UUID, failedAt time.Time) (storedb.Turn, error) {
	turn := storedb.Turn{}
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		if _, err := queries.LockSession(ctx, sessionID); err != nil {
			return err
		}
		failed, err := queries.MarkTurnDeliveryFailed(ctx, storedb.MarkTurnDeliveryFailedParams{
			ID: turnID, CompletedAt: null.TimeFrom(failedAt),
		})
		if errors.Is(err, sql.ErrNoRows) {
			turn, err = queries.GetTurn(ctx, turnID)
			return err
		}
		if err != nil {
			return err
		}
		turn = failed
		activeTurns, err := queries.CountActiveSessionTurns(ctx, sessionID)
		if err != nil {
			return err
		}
		sessionStatus := api.SessionStatusIdle
		if activeTurns > 0 {
			sessionStatus = api.SessionStatusRunning
		}
		if _, err = queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(sessionStatus), UpdatedAt: failedAt,
		}); err != nil {
			return err
		}
		if err = insertTurnEvent(ctx, queries, sessionID, turn.ID, api.SessionEventKindTurnCompleted, failedAt); err != nil {
			return err
		}
		return insertSessionUpdatedEvent(ctx, queries, sessionID, failedAt)
	})
	return turn, err
}

func appendSubagentActivity(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, event BridgeEvent, occurredAt time.Time, kind api.SubagentActivityKind) error {
	seq, err := queries.AllocateSubagentActivitySeq(ctx, storedb.AllocateSubagentActivitySeqParams{
		SessionID: sessionID, SubagentID: event.AgentID, UpdatedAt: occurredAt,
	})
	if err != nil {
		return err
	}
	if _, err := queries.InsertSubagentActivity(ctx, storedb.InsertSubagentActivityParams{
		SessionID: sessionID, SubagentID: event.AgentID, Seq: seq, Kind: string(kind), OccurredAt: occurredAt,
		Body: event.Text, ToolName: string(event.ToolName), ToolCallID: string(event.ToolCallID),
	}); err != nil {
		return err
	}
	return insertEvent(ctx, queries, storedb.InsertSessionEventParams{
		SessionID: sessionID, Kind: string(api.SessionEventKindSubagentActivityAppended), OccurredAt: occurredAt,
		SubagentID:          null.StringFrom(event.AgentID),
		SubagentActivitySeq: null.IntFrom(seq),
	})
}

func insertEvent(ctx context.Context, queries storedb.Querier, parameters storedb.InsertSessionEventParams) error {
	_, err := insertEventWithSeq(ctx, queries, parameters)
	return err
}

func insertEventWithSeq(ctx context.Context, queries storedb.Querier, parameters storedb.InsertSessionEventParams) (int64, error) {
	seq, err := queries.AllocateSessionEventSeq(ctx, parameters.SessionID)
	if err != nil {
		return 0, err
	}
	parameters.Seq = int64(seq)
	if _, err = queries.InsertSessionEvent(ctx, parameters); err != nil {
		return 0, err
	}
	return int64(seq), nil
}

func insertSessionUpdatedEvent(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, occurredAt time.Time) error {
	_, err := insertSessionUpdatedEventWithSeq(ctx, queries, sessionID, occurredAt)
	return err
}

func insertSessionUpdatedEventWithSeq(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, occurredAt time.Time) (int64, error) {
	seq, err := insertEventWithSeq(ctx, queries, storedb.InsertSessionEventParams{
		SessionID: sessionID, Kind: string(api.SessionEventKindSessionUpdated), OccurredAt: occurredAt,
	})
	if err != nil {
		return 0, err
	}
	_, err = queries.SnapshotSessionEvent(ctx, storedb.SnapshotSessionEventParams{SessionID: sessionID, EventSeq: seq})
	return seq, err
}

func insertTurnEvent(
	ctx context.Context,
	queries storedb.Querier,
	sessionID uuid.UUID,
	turnID uuid.UUID,
	kind api.SessionEventKind,
	occurredAt time.Time,
) error {
	seq, err := insertEventWithSeq(ctx, queries, storedb.InsertSessionEventParams{
		SessionID: sessionID, Kind: string(kind), OccurredAt: occurredAt, TurnID: &turnID,
	})
	if err != nil {
		return err
	}
	_, err = queries.SnapshotTurnEvent(ctx, storedb.SnapshotTurnEventParams{TurnID: turnID, EventSeq: seq})
	if err == nil && kind == api.SessionEventKindTurnCompleted {
		_, err = queries.EnqueueTurnTrace(ctx, turnID)
	}
	return err
}

func insertRequestEvent(
	ctx context.Context,
	queries storedb.Querier,
	sessionID uuid.UUID,
	requestID uuid.UUID,
	kind api.SessionEventKind,
	occurredAt time.Time,
) error {
	seq, err := insertEventWithSeq(ctx, queries, storedb.InsertSessionEventParams{
		SessionID: sessionID, Kind: string(kind), OccurredAt: occurredAt, RequestID: &requestID,
	})
	if err != nil {
		return err
	}
	_, err = queries.SnapshotRequestEvent(ctx, storedb.SnapshotRequestEventParams{RequestID: requestID, EventSeq: seq})
	return err
}

func abandonSessionRequests(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, occurredAt time.Time) (int, error) {
	requests, err := queries.AbandonSessionRequests(ctx, storedb.AbandonSessionRequestsParams{
		ResolvedAt: null.TimeFrom(occurredAt), SessionID: sessionID,
	})
	if err != nil {
		return 0, err
	}
	for _, request := range requests {
		if err := insertRequestEvent(ctx, queries, sessionID, request.ID, api.SessionEventKindRequestResolved, occurredAt); err != nil {
			return 0, err
		}
	}
	return len(requests), nil
}

func abandonTurnRequests(
	ctx context.Context,
	queries storedb.Querier,
	sessionID uuid.UUID,
	turnID uuid.UUID,
	occurredAt time.Time,
) ([]storedb.PendingRequest, error) {
	requests, err := queries.AbandonTurnRequests(ctx, storedb.AbandonTurnRequestsParams{
		ResolvedAt: null.TimeFrom(occurredAt), SessionID: sessionID, TurnID: &turnID,
	})
	if err != nil {
		return nil, err
	}
	for _, request := range requests {
		if err := insertRequestEvent(ctx, queries, sessionID, request.ID, api.SessionEventKindRequestResolved, occurredAt); err != nil {
			return nil, err
		}
	}
	return requests, nil
}

// finalizeSessionActivity makes every in-flight child of a terminal session
// terminal in the same transaction as its parent transition.
func finalizeSessionActivity(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, occurredAt time.Time) error {
	turns, err := queries.FinalizeSessionTurns(ctx, storedb.FinalizeSessionTurnsParams{
		CompletedAt: null.TimeFrom(occurredAt), SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	for _, turn := range turns {
		if err := insertTurnEvent(ctx, queries, sessionID, turn.ID, api.SessionEventKindTurnCompleted, occurredAt); err != nil {
			return err
		}
	}
	if _, err := abandonSessionRequests(ctx, queries, sessionID, occurredAt); err != nil {
		return err
	}
	subagents, err := queries.FinalizeSessionSubagents(ctx, storedb.FinalizeSessionSubagentsParams{
		CompletedAt: null.TimeFrom(occurredAt), UpdatedAt: occurredAt, SessionID: sessionID,
	})
	if err != nil {
		return err
	}
	for _, subagent := range subagents {
		if err := insertSubagentEvent(ctx, queries, sessionID, subagent.ID, occurredAt); err != nil {
			return err
		}
	}
	return nil
}

func insertSubagentEvent(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, subagentID string, occurredAt time.Time) error {
	seq, err := insertEventWithSeq(ctx, queries, storedb.InsertSessionEventParams{
		SessionID: sessionID, Kind: string(api.SessionEventKindSubagentUpdated), OccurredAt: occurredAt,
		SubagentID: null.StringFrom(subagentID),
	})
	if err != nil {
		return err
	}
	_, err = queries.SnapshotSubagentEvent(ctx, storedb.SnapshotSubagentEventParams{
		SessionID: sessionID, SubagentID: subagentID, EventSeq: seq,
	})
	return err
}
