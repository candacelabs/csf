package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const errorCodeSubagentNotFound = "subagent_not_found"

// ListSubagents lists actual SDK-reported subagents for a session.
func (adapter *CopilotAdapter) listSubagents(ctx context.Context, sessionID uuid.UUID) ([]api.Subagent, error) {
	if _, err := adapter.store.GetSession(ctx, sessionID); err != nil {
		return nil, lookupFailure(err, errorCodeSessionNotFound, "no session with that id")
	}
	rows, err := adapter.store.ListSubagents(ctx, sessionID)
	if err != nil {
		return nil, storeFailure(err)
	}
	data := make([]api.Subagent, 0, len(rows))
	for _, row := range rows {
		data = append(data, subagentView(row))
	}
	return data, nil
}

// GetSubagent reads one lifecycle row.
func (adapter *CopilotAdapter) getSubagent(ctx context.Context, sessionID uuid.UUID, subagentID string) (api.Subagent, error) {
	row, err := adapter.store.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: sessionID, ID: subagentID})
	if err != nil {
		return api.Subagent{}, lookupFailure(err, errorCodeSubagentNotFound, "no subagent with that id in this session")
	}
	return subagentView(row), nil
}

// ListSubagentActivity pages actual subagent messages and tool activity.
func (adapter *CopilotAdapter) listSubagentActivity(ctx context.Context, sessionID uuid.UUID, subagentID string, after *int64, requestedLimit *int32) (api.SubagentActivityPage, error) {
	_, err := adapter.store.GetSubagent(ctx, storedb.GetSubagentParams{SessionID: sessionID, ID: subagentID})
	if errors.Is(err, sql.ErrNoRows) {
		return api.SubagentActivityPage{}, fail(http.StatusNotFound, errorCodeSubagentNotFound, "no subagent with that id in this session")
	}
	if err != nil {
		return api.SubagentActivityPage{}, storeFailure(err)
	}
	afterSeq := int64(0)
	if after != nil {
		afterSeq = *after
	}
	limit := adapter.pageLimit(requestedLimit)
	rows, err := adapter.store.ListSubagentActivities(ctx, storedb.ListSubagentActivitiesParams{SessionID: sessionID, SubagentID: subagentID, AfterSeq: afterSeq, RowLimit: limit})
	if err != nil {
		return api.SubagentActivityPage{}, storeFailure(err)
	}
	page := api.SubagentActivityPage{Data: make([]api.SubagentActivity, 0, len(rows))}
	for _, row := range rows {
		page.Data = append(page.Data, subagentActivityView(row))
	}
	if int32(len(rows)) == limit && len(rows) > 0 {
		next := rows[len(rows)-1].Seq
		page.NextAfterSeq = &next
	}
	return page, nil
}

func subagentView(row storedb.Subagent) api.Subagent {
	id, displayName, activityCount := row.ID, row.DisplayName, row.ActivityCount
	sessionID, startedAt, updatedAt := row.SessionID, row.StartedAt.UTC(), row.UpdatedAt.UTC()
	view := api.Subagent{
		Id: &id, SessionId: &sessionID, TurnId: row.TurnID, DisplayName: &displayName,
		Status: api.SubagentStatus(row.Status), Summary: row.Summary.Ptr(), ActivityCount: &activityCount,
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
