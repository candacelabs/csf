package copilotadapter

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const (
	lastEventIDQuery  = "lastEventId"
	lastEventIDHeader = "Last-Event-ID"
)

var errUnsupportedRequestEvent = errors.New("request event uses an unsupported legacy request kind or status")

type sessionEventFrame struct {
	Seq   int64
	Event api.SessionEvent
}

type sessionEventPage struct {
	Frames   []sessionEventFrame
	AfterSeq int64
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
