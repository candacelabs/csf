package copilotadapter

import (
	"context"

	"github.com/guregu/null/v5"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// Workspace reads retained identities without filesystem inspection or provider
// calls. SubscribeWorkspace must be called before loading a live snapshot.
func (adapter *CopilotAdapter) Workspace(ctx context.Context) (api.WorkspaceSnapshot, error) {
	snapshot := api.WorkspaceSnapshot{Sessions: []api.Session{}, Worktrees: []api.WorkspaceWorktree{}, Repositories: []api.Repository{}, TaskLinks: []api.WorkspaceTaskLink{}}
	query := storedb.ListSessionsParams{RowLimit: adapter.defaultPageLimit()}
	for {
		rows, err := adapter.store.ListSessions(ctx, query)
		if err != nil {
			return snapshot, err
		}
		for _, row := range rows {
			snapshot.Sessions = append(snapshot.Sessions, views.Session(row))
		}
		if int32(len(rows)) < query.RowLimit {
			break
		}
		last := rows[len(rows)-1]
		query.CursorCreatedAt, query.CursorID = null.TimeFrom(last.CreatedAt), &last.ID
	}
	rows, err := adapter.store.ListWorktrees(ctx)
	if err != nil {
		return snapshot, err
	}
	for _, row := range rows {
		// Git state is unknown until explicitly inspected; a database read must
		// not pretend that the worktree is clean or that its HEAD was checked.
		snapshot.Worktrees = append(snapshot.Worktrees, views.WorkspaceWorktree(row))
	}
	for _, repository := range adapter.worktrees.Repositories() {
		identifier, name, root, ref := repository.ID, repository.DisplayName, repository.Root, repository.DefaultRef
		snapshot.Repositories = append(snapshot.Repositories, api.Repository{Id: identifier, DisplayName: name, Root: &root, DefaultRef: &ref})
	}
	links, err := adapter.store.ListSessionTasks(ctx)
	if err != nil {
		return snapshot, err
	}
	for _, link := range links {
		snapshot.TaskLinks = append(snapshot.TaskLinks, views.WorkspaceTaskLink(link))
	}
	return snapshot, nil
}

func (adapter *CopilotAdapter) GetWorkspace(ctx context.Context, request api.GetWorkspaceRequestObject) (api.GetWorkspaceResponseObject, error) {
	snapshot, err := adapter.Workspace(requestContext(ctx))
	if err != nil {
		return nil, storeFailure(err)
	}
	return api.GetWorkspace200JSONResponse(snapshot), nil
}
