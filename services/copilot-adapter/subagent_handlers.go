package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const errorCodeSubagentNotFound = "subagent_not_found"

// ListSubagents lists actual SDK-reported subagents for a session.
func (adapter *CopilotAdapter) ListSubagents(ctx context.Context, request api.ListSubagentsRequestObject) (api.ListSubagentsResponseObject, error) {
	ctx = requestContext(ctx)
	if _, err := adapter.store.GetSession(ctx, request.SessionId); err != nil {
		return nil, lookupFailure(err, errorCodeSessionNotFound, "no session with that id")
	}
	rows, err := adapter.store.ListSubagents(ctx, request.SessionId)
	if err != nil {
		return nil, storeFailure(err)
	}
	data := make([]api.Subagent, 0, len(rows))
	for _, row := range rows {
		data = append(data, subagentView(row))
	}
	return api.ListSubagents200JSONResponse(api.SubagentList{Data: data}), nil
}

// GetSubagent reads one lifecycle row.
func (adapter *CopilotAdapter) GetSubagent(ctx context.Context, request api.GetSubagentRequestObject) (api.GetSubagentResponseObject, error) {
	ctx = requestContext(ctx)
	row, err := adapter.store.GetSubagent(ctx, storedb.GetSubagentParams{
		SessionID: request.SessionId, ID: request.SubagentId,
	})
	if err != nil {
		return nil, lookupFailure(err, errorCodeSubagentNotFound, "no subagent with that id in this session")
	}
	return api.GetSubagent200JSONResponse(subagentView(row)), nil
}

// ListSubagentActivity pages actual subagent messages and tool activity.
func (adapter *CopilotAdapter) ListSubagentActivity(ctx context.Context, request api.ListSubagentActivityRequestObject) (api.ListSubagentActivityResponseObject, error) {
	ctx = requestContext(ctx)
	if _, err := adapter.store.GetSubagent(ctx, storedb.GetSubagentParams{
		SessionID: request.SessionId, ID: request.SubagentId,
	}); errors.Is(err, sql.ErrNoRows) {
		return nil, fail(http.StatusNotFound, errorCodeSubagentNotFound, "no subagent with that id in this session")
	} else if err != nil {
		return nil, storeFailure(err)
	}
	afterSeq := int64(0)
	if request.Params.AfterSeq != nil {
		afterSeq = *request.Params.AfterSeq
	}
	limit := adapter.pageLimit(request.Params.Limit)
	rows, err := adapter.store.ListSubagentActivities(ctx, storedb.ListSubagentActivitiesParams{
		SessionID: request.SessionId, SubagentID: request.SubagentId, AfterSeq: afterSeq, RowLimit: limit,
	})
	if err != nil {
		return nil, storeFailure(err)
	}
	page := api.SubagentActivityPage{Data: make([]api.SubagentActivity, 0, len(rows))}
	for _, row := range rows {
		page.Data = append(page.Data, subagentActivityView(row))
	}
	if int32(len(rows)) == limit && len(rows) > 0 {
		next := rows[len(rows)-1].Seq
		page.NextAfterSeq = &next
	}
	return api.ListSubagentActivity200JSONResponse(page), nil
}

func subagentView(row storedb.Subagent) api.Subagent {
	id, displayName, activityCount := row.ID, row.DisplayName, row.ActivityCount
	summary := row.Summary.Ptr()
	sessionID, startedAt, updatedAt := row.SessionID, row.StartedAt.UTC(), row.UpdatedAt.UTC()
	view := api.Subagent{
		Id: &id, SessionId: &sessionID, TurnId: row.TurnID, DisplayName: &displayName,
		Status: api.SubagentStatus(row.Status), Summary: summary, ActivityCount: &activityCount,
		StartedAt: &startedAt, UpdatedAt: &updatedAt,
	}
	if row.CompletedAt.Valid {
		completedAt := row.CompletedAt.Time.UTC()
		view.CompletedAt = &completedAt
	}
	return view
}

func subagentActivityView(row storedb.SubagentActivity) api.SubagentActivity {
	seq, sessionID, subagentID, occurredAt := row.Seq, row.SessionID, row.SubagentID, row.OccurredAt.UTC()
	view := api.SubagentActivity{
		Seq: &seq, SessionId: &sessionID, SubagentId: &subagentID,
		Kind: api.SubagentActivityKind(row.Kind), OccurredAt: &occurredAt, Text: row.Body,
	}
	if row.ToolName != "" {
		view.ToolName = &row.ToolName
	}
	if row.ToolCallID != "" {
		view.ToolCallId = &row.ToolCallID
	}
	return view
}
