package copilotadapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/httpserver"
	adapterconfig "github.com/candacelabs/csf/services/copilot-adapter/config"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
	cron "github.com/candacelabs/csf/services/cron"
)

// CopilotAdapter is the adapter, mounted into a binary's existing Gin engine
// through Register. It owns no process concerns and never opens a listener:
// a service is an option slid into a pre-existing binary (CS-10).
type CopilotAdapter struct {
	bridge            ICopilotBridge
	store             IStore
	logger            *slog.Logger
	version           string
	config            *copilotv1.AdapterConfig
	sessions          *sessionRegistry
	worktrees         IWorktreeManager
	terminals         ITerminalManager
	scheduleStore     cron.IStore
	scheduleReload    chan scheduleReloadRequest
	scheduleControls  *mutationRegistry[uuid.UUID]
	scheduleRunMutex  sync.Mutex
	scheduleRunActive bool
	scheduleRunDone   chan struct{}
	mutations         *mutationRegistry[uuid.UUID]
	creations         *mutationRegistry[uuid.UUID]
	worktreeMutations *mutationRegistry[string]
	changes           *workspaceChanges
	tasks             *workspaceTasks
}

// NewCopilotAdapter validates the whole option set, then builds the adapter.
func NewCopilotAdapter(options ...Option) (*CopilotAdapter, error) {
	resolved, err := resolve(options)
	if err != nil {
		return nil, err
	}
	changes := &workspaceChanges{}
	return &CopilotAdapter{
		bridge:  resolved.bridge,
		store:   &notifyingStore{IStore: resolved.store, changes: changes},
		logger:  resolved.logger,
		version: resolved.version,
		config:  resolved.config,
		sessions: newSessionRegistry(
			adapterconfig.DurableTransitionTimeout(resolved.config),
		),
		worktrees:         resolved.worktrees,
		terminals:         resolved.terminals,
		scheduleStore:     resolved.scheduleStore,
		scheduleReload:    make(chan scheduleReloadRequest, 1),
		scheduleControls:  newMutationRegistry[uuid.UUID](),
		mutations:         newMutationRegistry[uuid.UUID](),
		creations:         newMutationRegistry[uuid.UUID](),
		worktreeMutations: newMutationRegistry[string](),
		changes:           changes,
		tasks:             &workspaceTasks{source: resolved.taskContinuity, cache: make(map[string]taskObservation)},
	}, nil
}

// Register installs OpenAPI request validation and the generated strict
// handlers onto router. It is the service's only mount point.
func (adapter *CopilotAdapter) Register(router gin.IRouter) error {
	specification, err := api.GetSwagger()
	if err != nil {
		return fmt.Errorf("copilot-adapter: load the embedded contract: %w", err)
	}
	specification.Servers = nil
	validator, err := httpserver.ValidateOpenAPIRequests(specification, openapi3filter.Options{
		// The contract's bearer scheme is optional and unimplemented (the
		// loopback deployment carries no credential), so authentication is a
		// no-op here and a proxy adds one where it is wanted.
		AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		// Validation failures answer with the contract's Error shape, not
		// the middleware's plain-text default.
	}, func(context *gin.Context, message string, statusCode int) {
		context.AbortWithStatusJSON(statusCode, errorBody(errorCodeInvalidRequest, message))
	})
	if err != nil {
		return fmt.Errorf("copilot-adapter: validate the embedded contract: %w", err)
	}
	// The middlewares are scoped to the generated routes: a service mounted
	// into a binary's engine never installs anything engine-wide, and the
	// validator would otherwise 404 every path the contract does not declare.
	api.RegisterHandlersWithOptions(router, api.NewStrictHandler(&apiHandlers{service: adapter}, []api.StrictMiddlewareFunc{renderFailures}), api.GinServerOptions{
		Middlewares: []api.MiddlewareFunc{promoteLastEventIDQuery, api.MiddlewareFunc(validator)},
	})
	return nil
}

// sessionEvents owns the complete persisted replay read. HTTP framing consumes
// concrete events and never receives database rows, queries, or transactions.
func (adapter *CopilotAdapter) sessionEvents(ctx context.Context, sessionID uuid.UUID, afterSeq int64) (sessionEventPage, error) {
	page := sessionEventPage{AfterSeq: afterSeq}
	rows, err := adapter.store.ListSessionEventsAfterSeq(ctx, storedb.ListSessionEventsAfterSeqParams{
		SessionID: sessionID, AfterSeq: afterSeq, RowLimit: adapterconfig.EventStreamPageSize(adapter.config),
	})
	if err != nil {
		return page, err
	}
	for _, row := range rows {
		envelope, err := adapter.sessionEventEnvelope(ctx, row)
		if errors.Is(err, errUnsupportedRequestEvent) {
			page.AfterSeq = row.Seq
			continue
		}
		if err != nil {
			adapter.logger.Warn("reconstruct session event", "sessionId", sessionID, "seq", row.Seq, "error", err)
			return page, err
		}
		page.Frames = append(page.Frames, sessionEventFrame{Seq: row.Seq, Event: envelope})
		page.AfterSeq = row.Seq
	}
	return page, nil
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
		payload := views.SessionEventVersion(version)
		if err := adapter.hydrateSessionPermissionPolicy(ctx, version.ID, &payload); err != nil {
			return envelope, err
		}
		err = envelope.FromSessionUpdatedEvent(api.SessionUpdatedEvent{
			Kind: api.SessionUpdatedEventKindSessionUpdated, OccurredAt: row.OccurredAt.UTC(),
			Seq: row.Seq, SessionId: row.SessionID, Payload: payload,
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
