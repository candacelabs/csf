package copilotadapter

import (
	"context"
	"net/http"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func (handler *apiHandlers) LinkSessionTask(ctx context.Context, request api.LinkSessionTaskRequestObject) (api.LinkSessionTaskResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidRequest, "task association is required")
	}
	link, err := handler.service.linkSessionTask(requestContext(ctx), request.SessionId, *request.Body)
	if err != nil {
		return nil, err
	}
	return api.LinkSessionTask200JSONResponse(link), nil
}

func (handler *apiHandlers) RefreshWorkspace(ctx context.Context, request api.RefreshWorkspaceRequestObject) (api.RefreshWorkspaceResponseObject, error) {
	if err := handler.service.RefreshWorkspace(requestContext(ctx)); err != nil {
		return nil, err
	}
	return api.RefreshWorkspace204Response{}, nil
}
