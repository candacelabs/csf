package copilotadapter

import (
	"context"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func (handler *apiHandlers) GetWorkspace(ctx context.Context, request api.GetWorkspaceRequestObject) (api.GetWorkspaceResponseObject, error) {
	snapshot, err := handler.service.Workspace(requestContext(ctx))
	if err != nil {
		return nil, storeFailure(err)
	}
	return api.GetWorkspace200JSONResponse(snapshot), nil
}
