package copilotadapter

import (
	"context"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func (handler *apiHandlers) ListRepositories(ctx context.Context, request api.ListRepositoriesRequestObject) (api.ListRepositoriesResponseObject, error) {
	return api.ListRepositories200JSONResponse(api.RepositoryList{Data: handler.service.listRepositories()}), nil
}

func (handler *apiHandlers) ListWorktrees(ctx context.Context, request api.ListWorktreesRequestObject) (api.ListWorktreesResponseObject, error) {
	data, err := handler.service.listWorktrees(requestContext(ctx))
	if err != nil {
		return nil, err
	}
	return api.ListWorktrees200JSONResponse(api.WorktreeList{Data: data}), nil
}

func (handler *apiHandlers) GetWorktree(ctx context.Context, request api.GetWorktreeRequestObject) (api.GetWorktreeResponseObject, error) {
	view, err := handler.service.getWorktree(requestContext(ctx), request.WorktreeId)
	if err != nil {
		return nil, err
	}
	return api.GetWorktree200JSONResponse(view), nil
}

func (handler *apiHandlers) GetWorktreeChanges(ctx context.Context, request api.GetWorktreeChangesRequestObject) (api.GetWorktreeChangesResponseObject, error) {
	changes, err := handler.service.getWorktreeChanges(requestContext(ctx), request.WorktreeId)
	if err != nil {
		return nil, err
	}
	return api.GetWorktreeChanges200JSONResponse(changes), nil
}
