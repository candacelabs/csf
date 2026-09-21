package copilotadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/httpserver"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const (
	lastEventIDQuery  = "lastEventId"
	lastEventIDHeader = "Last-Event-ID"
)

var errUnsupportedRequestEvent = errors.New("request event uses an unsupported legacy request kind or status")

func (adapter *CopilotAdapter) streamSessionEvents(ginContext *gin.Context, sessionID uuid.UUID, afterSeq int64) {
	ctx := ginContext.Request.Context()
	cursor := afterSeq
	ticker := time.NewTicker(adapter.eventStreamPoll())
	defer ticker.Stop()
	httpserver.EventStream(ginContext, func(writer io.Writer) bool {
		rows, err := adapter.store.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
			SessionID: sessionID, AfterSeq: cursor, RowLimit: adapter.eventStreamPage(),
		})
		if err != nil {
			return false
		}
		for _, row := range rows {
			envelope, err := adapter.sessionEventEnvelope(ctx, row)
			if err != nil {
				if errors.Is(err, errUnsupportedRequestEvent) {
					cursor = row.Seq
					continue
				}
				adapter.logger.Warn("reconstruct session event", "sessionId", sessionID, "seq", row.Seq, "error", err)
				return false
			}
			if err := httpserver.EncodeEvent(writer, strconv.FormatInt(row.Seq, 10), envelope); err != nil {
				return false
			}
			cursor = row.Seq
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			return true
		}
	})
}

// sessionEventEnvelope reconstructs the generated discriminated union from
// relational facts. No generated HTTP payload is read from persistence.
func (adapter *CopilotAdapter) sessionEventEnvelope(ctx context.Context, row storedb.SessionEvent) (api.SessionEvent, error) {
	var envelope api.SessionEvent
	switch api.SessionEventKind(row.Kind) {
	case api.SessionEventKindSessionUpdated:
		version, err := adapter.store.GetSessionEventVersion(ctx, storedb.GetSessionEventVersionParams{
			SessionID: row.SessionID, EventSeq: row.Seq,
		})
		if err != nil {
			return envelope, err
		}
		err = envelope.FromSessionUpdatedEvent(api.SessionUpdatedEvent{
			Kind: api.SessionUpdatedEventKindSessionUpdated, OccurredAt: row.OccurredAt.UTC(),
			Seq: row.Seq, SessionId: row.SessionID, Payload: views.SessionEventVersion(version),
		})
		return envelope, err
	case api.SessionEventKindTurnStarted, api.SessionEventKindTurnCompleted:
		err := versionedSessionEvent(row.TurnID != nil, "turn event has no turn reference", api.SessionEventKind(row.Kind), api.SessionEventKindTurnStarted, func() (api.Turn, error) {
			return adapter.turnEventPayload(ctx, row)
		}, func(payload api.Turn) error {
			return addTurnStartedEvent(&envelope, row, payload)
		}, func(payload api.Turn) error {
			return addTurnCompletedEvent(&envelope, row, payload)
		})
		return envelope, err
	case api.SessionEventKindTranscriptAppended:
		if !row.TranscriptSeq.Valid {
			return envelope, fmt.Errorf("transcript event has no transcript reference")
		}
		item, err := adapter.store.GetTranscriptItem(ctx, storedb.GetTranscriptItemParams{
			SessionID: row.SessionID, Seq: row.TranscriptSeq.Int64,
		})
		if err != nil {
			return envelope, err
		}
		err = envelope.FromTranscriptAppendedEvent(api.TranscriptAppendedEvent{
			Kind: api.TranscriptAppendedEventKindTranscriptAppended, OccurredAt: row.OccurredAt.UTC(),
			Seq: row.Seq, SessionId: row.SessionID, Payload: views.TranscriptItem(item),
		})
		return envelope, err
	case api.SessionEventKindAssistantDelta:
		if row.TurnID == nil {
			return envelope, fmt.Errorf("assistant delta has no turn reference")
		}
		err := envelope.FromAssistantDeltaEvent(api.AssistantDeltaEvent{
			Kind: api.AssistantDeltaEventKindAssistantDelta, OccurredAt: row.OccurredAt.UTC(),
			Seq: row.Seq, SessionId: row.SessionID,
			Payload: api.AssistantDelta{Text: row.DeltaText, TurnId: *row.TurnID},
		})
		return envelope, err
	case api.SessionEventKindRequestOpened, api.SessionEventKindRequestResolved:
		err := versionedSessionEvent(row.RequestID != nil, "request event has no request reference", api.SessionEventKind(row.Kind), api.SessionEventKindRequestOpened, func() (api.SessionRequest, error) {
			return adapter.requestEventPayload(ctx, row)
		}, func(payload api.SessionRequest) error {
			return addRequestOpenedEvent(&envelope, row, payload)
		}, func(payload api.SessionRequest) error {
			return addRequestResolvedEvent(&envelope, row, payload)
		})
		return envelope, err
	case api.SessionEventKindSubagentUpdated:
		if !row.SubagentID.Valid {
			return envelope, fmt.Errorf("subagent event has no subagent reference")
		}
		version, err := adapter.store.GetSubagentEventVersion(ctx, storedb.GetSubagentEventVersionParams{
			SessionID: row.SessionID, EventSeq: row.Seq,
		})
		if err != nil {
			return envelope, err
		}
		err = envelope.FromSubagentUpdatedEvent(api.SubagentUpdatedEvent{
			Kind: api.SubagentUpdatedEventKindSubagentUpdated, OccurredAt: row.OccurredAt.UTC(),
			Seq: row.Seq, SessionId: row.SessionID, Payload: views.SubagentEventVersion(version),
		})
		return envelope, err
	case api.SessionEventKindSubagentActivityAppended:
		if !row.SubagentID.Valid || !row.SubagentActivitySeq.Valid {
			return envelope, fmt.Errorf("subagent activity event has no activity reference")
		}
		activity, err := adapter.store.GetSubagentActivity(ctx, storedb.GetSubagentActivityParams{
			SessionID: row.SessionID, SubagentID: row.SubagentID.String, Seq: row.SubagentActivitySeq.Int64,
		})
		if err != nil {
			return envelope, err
		}
		err = envelope.FromSubagentActivityAppendedEvent(api.SubagentActivityAppendedEvent{
			Kind:       api.SubagentActivityAppendedEventKindSubagentActivityAppended,
			OccurredAt: row.OccurredAt.UTC(), Seq: row.Seq, SessionId: row.SessionID,
			Payload: subagentActivityView(activity),
		})
		return envelope, err
	case api.SessionEventKindHeartbeat:
		err := envelope.FromHeartbeatEvent(api.HeartbeatEvent{
			Kind: api.HeartbeatEventKindHeartbeat, OccurredAt: row.OccurredAt.UTC(),
			Seq: row.Seq, SessionId: row.SessionID,
		})
		return envelope, err
	default:
		return envelope, fmt.Errorf("unknown session event kind %q", row.Kind)
	}
}

func (adapter *CopilotAdapter) turnEventPayload(ctx context.Context, row storedb.SessionEvent) (api.Turn, error) {
	version, err := adapter.store.GetTurnEventVersion(ctx, storedb.GetTurnEventVersionParams{
		SessionID: row.SessionID, EventSeq: row.Seq,
	})
	return views.TurnEventVersion(version), err
}

func (adapter *CopilotAdapter) requestEventPayload(ctx context.Context, row storedb.SessionEvent) (api.SessionRequest, error) {
	version, err := adapter.store.GetRequestEventVersion(ctx, storedb.GetRequestEventVersionParams{
		SessionID: row.SessionID, EventSeq: row.Seq,
	})
	if err != nil {
		return api.SessionRequest{}, err
	}
	if version.Kind != string(api.Permission) || !api.SessionRequestStatus(version.Status).Valid() {
		return api.SessionRequest{}, errUnsupportedRequestEvent
	}
	return views.SessionRequestEventVersion(version), err
}

func addTurnStartedEvent(envelope *api.SessionEvent, row storedb.SessionEvent, payload api.Turn) error {
	return envelope.FromTurnStartedEvent(api.TurnStartedEvent{
		Kind: api.TurnStartedEventKindTurnStarted, OccurredAt: row.OccurredAt.UTC(),
		Seq: row.Seq, SessionId: row.SessionID, Payload: payload,
	})
}

func addTurnCompletedEvent(envelope *api.SessionEvent, row storedb.SessionEvent, payload api.Turn) error {
	return envelope.FromTurnCompletedEvent(api.TurnCompletedEvent{
		Kind: api.TurnCompletedEventKindTurnCompleted, OccurredAt: row.OccurredAt.UTC(),
		Seq: row.Seq, SessionId: row.SessionID, Payload: payload,
	})
}

func addRequestOpenedEvent(envelope *api.SessionEvent, row storedb.SessionEvent, payload api.SessionRequest) error {
	return envelope.FromRequestOpenedEvent(api.RequestOpenedEvent{
		Kind: api.RequestOpenedEventKindRequestOpened, OccurredAt: row.OccurredAt.UTC(),
		Seq: row.Seq, SessionId: row.SessionID, Payload: payload,
	})
}

func addRequestResolvedEvent(envelope *api.SessionEvent, row storedb.SessionEvent, payload api.SessionRequest) error {
	return envelope.FromRequestResolvedEvent(api.RequestResolvedEvent{
		Kind: api.RequestResolvedEventKindRequestResolved, OccurredAt: row.OccurredAt.UTC(),
		Seq: row.Seq, SessionId: row.SessionID, Payload: payload,
	})
}

func versionedSessionEvent[Payload any](referencePresent bool, missingReference string, kind api.SessionEventKind, firstKind api.SessionEventKind, load func() (Payload, error), whenFirst func(payload Payload) error, otherwise func(payload Payload) error) error {
	if !referencePresent {
		return errors.New(missingReference)
	}
	payload, err := load()
	if err != nil {
		return err
	}
	if kind == firstKind {
		return whenFirst(payload)
	}
	return otherwise(payload)
}

func requestContext(ctx context.Context) context.Context {
	if ginContext, ok := ctx.(*gin.Context); ok && ginContext.Request != nil {
		return ginContext.Request.Context()
	}
	return ctx
}

func promoteLastEventIDQuery(context *gin.Context) {
	if value := context.Query(lastEventIDQuery); value != "" && context.GetHeader(lastEventIDHeader) == "" {
		context.Request.Header.Set(lastEventIDHeader, value)
	}
	context.Next()
}
