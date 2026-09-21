package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// Telemetry reads the same retained facts used by the API, board and metrics.
// SQLC keyset pagination prevents silently truncating totals at one page.
func (adapter *CopilotAdapter) Telemetry(ctx context.Context) (api.TelemetrySnapshot, error) {
	snapshot := api.TelemetrySnapshot{ObservedAt: time.Now().UTC(), Sessions: []api.SessionTelemetry{}, Traces: []api.TraceDeliveryCount{}}
	page := storedb.ListSessionTelemetryParams{RowLimit: adapter.defaultPageLimit()}
	for {
		rows, err := adapter.store.ListSessionTelemetry(ctx, page)
		if err != nil {
			return api.TelemetrySnapshot{}, err
		}
		for _, row := range rows {
			snapshot.Sessions = append(snapshot.Sessions, views.SessionTelemetry(row))
		}
		if len(rows) < int(page.RowLimit) {
			break
		}
		last := rows[len(rows)-1]
		page.CursorCreatedAt, page.CursorID = null.TimeFrom(last.CreatedAt), &last.SessionID
	}
	counts, err := adapter.store.CountTraceDeliveries(ctx)
	if err != nil {
		return api.TelemetrySnapshot{}, err
	}
	for _, count := range counts {
		snapshot.Traces = append(snapshot.Traces, views.TraceCount(count))
	}
	return snapshot, nil
}

func (adapter *CopilotAdapter) GetTelemetry(ctx context.Context, request api.GetTelemetryRequestObject) (api.GetTelemetryResponseObject, error) {
	snapshot, err := adapter.Telemetry(ctx)
	if err != nil {
		return nil, storeFailure(err)
	}
	return api.GetTelemetry200JSONResponse(snapshot), nil
}

// projectUsage shares the source-event transaction with its trace delivery.
// Unknown turn ownership remains NULL; a successor turn is never guessed.
func projectUsage(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, event BridgeEvent, occurredAt time.Time) error {
	if event.Usage == nil {
		return fmt.Errorf("%w: missing usage", errInvalidBridgeEvent)
	}
	usage := event.Usage
	if usage.Kind != api.ModelCall && usage.Kind != api.SessionCheckpoint {
		return fmt.Errorf("%w: unknown usage kind", errInvalidBridgeEvent)
	}
	for _, value := range []*int64{usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, usage.ReasoningTokens, usage.ApiDurationMs} {
		if value != nil && *value < 0 {
			return fmt.Errorf("%w: negative usage", errInvalidBridgeEvent)
		}
	}
	for _, value := range []*float64{usage.BillingMultiplier, usage.PremiumRequests, usage.NanoAiu} {
		if value != nil && (*value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return fmt.Errorf("%w: invalid usage value", errInvalidBridgeEvent)
		}
	}
	if usage.Kind == api.ModelCall && usage.PremiumRequests != nil {
		return fmt.Errorf("%w: premium consumption requires a provider checkpoint", errInvalidBridgeEvent)
	}
	if event.TurnID != nil {
		turn, err := queries.GetTurn(ctx, *event.TurnID)
		if err != nil {
			return err
		}
		if turn.SessionID != sessionID {
			return fmt.Errorf("%w: usage turn belongs to another session", errInvalidBridgeEvent)
		}
	}
	row := views.UsageInsert(*usage)
	row.SessionID, row.EventID, row.TurnID, row.OccurredAt = sessionID, event.ID, event.TurnID, occurredAt
	row.ProviderEvent = event.UsagePayload
	if _, err := queries.InsertProviderUsageEvent(ctx, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: conflicting provider usage identity", errInvalidBridgeEvent)
		}
		return err
	}
	_, err := queries.EnqueueUsageTrace(ctx, storedb.EnqueueUsageTraceParams{SessionID: sessionID, EventID: event.ID})
	return err
}
