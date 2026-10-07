package copilotadapter

import (
	"context"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

// ListSubagents maps one session identifier to its API collection.
func (handler *apiHandlers) ListSubagents(ctx context.Context, request api.ListSubagentsRequestObject) (api.ListSubagentsResponseObject, error) {
	data, err := handler.service.listSubagents(requestContext(ctx), request.SessionId)
	if err != nil {
		return nil, err
	}
	return api.ListSubagents200JSONResponse(api.SubagentList{Data: data}), nil
}

// GetSubagent maps exact session and subagent identifiers to the API response.
func (handler *apiHandlers) GetSubagent(ctx context.Context, request api.GetSubagentRequestObject) (api.GetSubagentResponseObject, error) {
	row, err := handler.service.getSubagent(requestContext(ctx), request.SessionId, request.SubagentId)
	if err != nil {
		return nil, err
	}
	return api.GetSubagent200JSONResponse(row), nil
}

// ListSubagentActivity maps pagination parameters to the API page.
func (handler *apiHandlers) ListSubagentActivity(ctx context.Context, request api.ListSubagentActivityRequestObject) (api.ListSubagentActivityResponseObject, error) {
	page, err := handler.service.listSubagentActivity(requestContext(ctx), request.SessionId, request.SubagentId, request.Params.AfterSeq, request.Params.Limit)
	if err != nil {
		return nil, err
	}
	return api.ListSubagentActivity200JSONResponse(page), nil
}
