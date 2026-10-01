package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const errorCodeWorktreeNotFound = "worktree_not_found"

func (adapter *CopilotAdapter) listRepositories() []api.Repository {
	data := make([]api.Repository, 0, len(adapter.worktrees.Repositories()))
	for _, repository := range adapter.worktrees.Repositories() {
		root, defaultRef := repository.Root, repository.DefaultRef
		data = append(data, api.Repository{Id: repository.ID, DisplayName: repository.DisplayName, Root: &root, DefaultRef: &defaultRef})
	}
	return data
}

func (adapter *CopilotAdapter) listWorktrees(ctx context.Context) ([]api.Worktree, error) {
	rows, err := adapter.store.ListWorktrees(ctx)
	if err != nil {
		return nil, storeFailure(err)
	}
	data := make([]api.Worktree, 0, len(rows))
	for _, row := range rows {
		view, err := adapter.currentWorktreeView(ctx, row)
		if err != nil {
			return nil, storeFailure(err)
		}
		data = append(data, view)
	}
	return data, nil
}

// quarantineWorktree takes the canonical-path fence before it snapshots the
// worktree's sessions. Creation takes the same fence before inserting or
// activating a session, so the snapshot cannot miss a late SDK activation.
func (adapter *CopilotAdapter) quarantineWorktree(ctx context.Context, row storedb.Worktree) (bool, error) {
	unlockWorktree := adapter.worktreeMutations.lock(row.Path)
	defer unlockWorktree()
	_, quarantined, err := adapter.quarantineInvalidWorktreeLocked(ctx, row)
	return quarantined, err
}

// quarantineInvalidWorktreeLocked revalidates while the caller owns row.Path.
// A recovered path and a transient inspection failure are never quarantined.
func (adapter *CopilotAdapter) quarantineInvalidWorktreeLocked(
	ctx context.Context,
	row storedb.Worktree,
) (storedb.Worktree, bool, error) {
	validated, err := adapter.validatedWorktree(ctx, row)
	if err == nil {
		return validated, false, nil
	}
	if !errors.Is(err, ErrInvalidWorktree) {
		return storedb.Worktree{}, false, err
	}
	if err := adapter.quarantineWorktreeSessionsLocked(ctx, row.ID); err != nil {
		return storedb.Worktree{}, false, err
	}
	return row, true, nil
}

func (adapter *CopilotAdapter) quarantineWorktreeSessionsLocked(_ context.Context, identifier uuid.UUID) error {
	sessions, unlockSessions, err := adapter.lockWorktreeSessions(identifier)
	if err != nil {
		return err
	}
	defer unlockSessions()
	durableContext, cancelDurableTransition := adapter.durableTransitionContext()
	now := time.Now().UTC()
	var quarantined []storedb.Session
	err = adapter.store.Transact(durableContext, func(queries storedb.Querier) error {
		var err error
		quarantined, err = queries.QuarantineWorktreeSessions(durableContext, storedb.QuarantineWorktreeSessionsParams{
			EndedAt: null.TimeFrom(now), UpdatedAt: now, WorktreeID: identifier,
			FailureCode:   int32(api.FailureCodeWorktreeUnavailable),
			FailureReason: null.StringFrom(sessionFailureDescription(api.FailureCodeWorktreeUnavailable)),
		})
		if err != nil {
			return err
		}
		targets := make(map[uuid.UUID]struct{}, len(sessions)+len(quarantined))
		for _, session := range sessions {
			targets[session.ID] = struct{}{}
		}
		for _, session := range quarantined {
			targets[session.ID] = struct{}{}
		}
		for sessionID := range targets {
			if err := finalizeSessionActivity(durableContext, queries, sessionID, now); err != nil {
				return err
			}
			if err := queries.CompleteQuarantinedSessionCreation(
				durableContext,
				storedb.CompleteQuarantinedSessionCreationParams{
					CompletedAt: null.TimeFrom(now), SessionID: sessionID,
				},
			); err != nil {
				return err
			}
		}
		for _, session := range quarantined {
			if err := insertSessionUpdatedEvent(durableContext, queries, session.ID, now); err != nil {
				return err
			}
		}
		_, err = queries.PauseWorktreeSchedules(durableContext, storedb.PauseWorktreeSchedulesParams{
			UpdatedAt: now, WorktreeID: identifier,
		})
		return err
	})
	cancelDurableTransition()
	// A COMMIT error is ambiguous: the durable terminal transitions may already
	// be visible. Reconcile schedules and handles for either outcome.
	adapter.reloadSchedules()
	reconcileContext, cancelReconciliation := adapter.durableTransitionContext()
	defer cancelReconciliation()
	targets := make(map[uuid.UUID]struct{}, len(sessions)+len(quarantined))
	for _, session := range sessions {
		targets[session.ID] = struct{}{}
	}
	for _, session := range quarantined {
		targets[session.ID] = struct{}{}
	}
	var closeErrs []error
	for sessionID := range targets {
		if err != nil {
			persisted, reconcileErr := adapter.store.GetSession(reconcileContext, sessionID)
			if reconcileErr != nil {
				adapter.logger.Warn("reconcile quarantined session", "sessionId", sessionID, "error", reconcileErr)
				continue
			}
			if persisted.Status != string(api.SessionStatusFailed) {
				continue
			}
		}
		if closeErr := adapter.closeLiveSession(sessionID); closeErr != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close session %s: %w", sessionID, closeErr))
		}
	}
	if err != nil {
		for _, closeErr := range closeErrs {
			adapter.logger.Warn("close quarantined session", "error", closeErr)
		}
		return err
	}
	return errors.Join(closeErrs...)
}

// lockValidatedWorktree establishes the immutable path fence before
// validation. The returned unlock must remain held across the filesystem
// operation that consumes the validated path.
func (adapter *CopilotAdapter) lockValidatedWorktree(
	ctx context.Context,
	row storedb.Worktree,
) (storedb.Worktree, func(), error) {
	unlock := adapter.worktreeMutations.lock(row.Path)
	validated, err := adapter.validatedWorktree(ctx, row)
	if err == nil {
		return validated, unlock, nil
	}
	if !errors.Is(err, ErrInvalidWorktree) {
		unlock()
		return storedb.Worktree{}, nil, err
	}
	validated, quarantined, quarantineErr := adapter.quarantineInvalidWorktreeLocked(ctx, row)
	if quarantineErr != nil {
		unlock()
		return storedb.Worktree{}, nil, quarantineErr
	}
	if quarantined {
		unlock()
		return storedb.Worktree{}, nil, ErrInvalidWorktree
	}
	return validated, unlock, nil
}

func (adapter *CopilotAdapter) lockWorktreeSessions(worktreeID uuid.UUID) ([]storedb.Session, func(), error) {
	discoveryContext, cancelDiscovery := adapter.durableTransitionContext()
	sessions, err := adapter.store.ListWorktreeSessions(discoveryContext, worktreeID)
	cancelDiscovery()
	if err != nil {
		return nil, nil, err
	}
	unlocks := make([]func(), 0, len(sessions)*2)
	for _, session := range sessions {
		unlocks = append(unlocks, adapter.scheduleControls.lock(session.ID))
	}
	for _, session := range sessions {
		unlocks = append(unlocks, adapter.mutations.lock(session.ID))
	}
	return sessions, func() {
		for index := len(unlocks) - 1; index >= 0; index-- {
			unlocks[index]()
		}
	}, nil
}

func (adapter *CopilotAdapter) missingWorktreeView(ctx context.Context, row storedb.Worktree) (api.Worktree, error) {
	count, err := adapter.store.CountWorktreeSessions(ctx, row.ID)
	if err != nil {
		return api.Worktree{}, err
	}
	id, repositoryID, repositoryRoot, path, baseRef := row.ID, row.RepositoryID, row.RepositoryRoot, row.Path, row.BaseRef
	clean, managed := false, row.Managed
	branch, head := "", ""
	created, updated := row.CreatedAt.UTC(), row.UpdatedAt.UTC()
	return api.Worktree{
		Id: &id, RepositoryId: &repositoryID, RepositoryRoot: &repositoryRoot, Path: &path,
		BaseRef: &baseRef, Branch: &branch, HeadSha: &head, Clean: &clean, Managed: &managed,
		SessionCount: &count, State: api.WorktreeStateMissing, CreatedAt: &created, UpdatedAt: &updated,
	}, nil
}

func (adapter *CopilotAdapter) getWorktree(ctx context.Context, worktreeID uuid.UUID) (api.Worktree, error) {
	row, err := adapter.store.GetWorktree(ctx, worktreeID)
	if err != nil {
		return api.Worktree{}, lookupFailure(err, errorCodeWorktreeNotFound, "no worktree with that id")
	}
	view, err := adapter.currentWorktreeView(ctx, row)
	if err != nil {
		return api.Worktree{}, storeFailure(err)
	}
	return view, nil
}

func (adapter *CopilotAdapter) getWorktreeChanges(ctx context.Context, worktreeID uuid.UUID) (api.WorktreeChanges, error) {
	row, err := adapter.store.GetWorktree(ctx, worktreeID)
	if err != nil {
		return api.WorktreeChanges{}, lookupFailure(err, errorCodeWorktreeNotFound, "no worktree with that id")
	}
	row, unlockWorktree, err := adapter.lockValidatedWorktree(ctx, row)
	if err != nil {
		if errors.Is(err, ErrInvalidWorktree) {
			return api.WorktreeChanges{}, fail(http.StatusNotFound, errorCodeWorktreeNotFound, "the worktree path is missing")
		}
		return api.WorktreeChanges{}, storeFailure(err)
	}
	defer unlockWorktree()
	changes, err := adapter.worktrees.Changes(ctx, row.Path)
	if err != nil {
		if errors.Is(err, ErrInvalidWorktree) {
			if quarantineErr := adapter.quarantineWorktreeSessionsLocked(ctx, row.ID); quarantineErr != nil {
				return api.WorktreeChanges{}, storeFailure(quarantineErr)
			}
			return api.WorktreeChanges{}, fail(http.StatusNotFound, errorCodeWorktreeNotFound, "the worktree path is missing")
		}
		return api.WorktreeChanges{}, storeFailure(err)
	}
	files := make([]api.GitChange, 0, len(changes.Files))
	for _, change := range changes.Files {
		view := api.GitChange{Path: change.Path, IndexState: api.GitFileState(change.IndexState), WorktreeState: api.GitFileState(change.WorktreeState)}
		if change.PreviousPath != "" {
			view.PreviousPath = &change.PreviousPath
		}
		files = append(files, view)
	}
	clean, truncated, captured, head := changes.Clean, changes.Truncated, changes.Captured.UTC(), changes.HeadSHA
	return api.WorktreeChanges{WorktreeId: &row.ID, HeadSha: &head, Clean: &clean, Files: files, Patch: changes.Patch, Truncated: &truncated, CapturedAt: &captured}, nil
}

// validatedWorktree re-establishes the configured filesystem trust boundary
// before any persisted path is inspected or executed within. Pre-workbench
// rows are adopted only when one configured manager can prove the exact path
// is its repository root or a registered worktree under its managed root.
func (adapter *CopilotAdapter) validatedWorktree(ctx context.Context, row storedb.Worktree) (storedb.Worktree, error) {
	if row.RepositoryID != "legacy" {
		prepared, err := adapter.worktrees.Reuse(ctx, row.RepositoryID, row.Path)
		if err != nil {
			return storedb.Worktree{}, err
		}
		if prepared.Repository.Root != row.RepositoryRoot || prepared.Path != row.Path {
			return storedb.Worktree{}, fmt.Errorf("%w: persisted worktree does not match configured canonical paths", ErrInvalidWorktree)
		}
		return row, nil
	}
	var adopted PreparedWorktree
	found := false
	for _, repository := range adapter.worktrees.Repositories() {
		candidate, err := adapter.worktrees.Reuse(ctx, repository.ID, row.Path)
		if err == nil {
			adopted, found = candidate, true
			break
		}
		if !errors.Is(err, ErrInvalidWorktree) {
			return storedb.Worktree{}, err
		}
	}
	if !found || adopted.Path != row.Path {
		return storedb.Worktree{}, fmt.Errorf("%w: legacy worktree is outside every configured repository boundary", ErrInvalidWorktree)
	}
	now := time.Now().UTC()
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		var err error
		row, err = queries.AdoptLegacyWorktree(ctx, storedb.AdoptLegacyWorktreeParams{
			RepositoryID: adopted.Repository.ID, RepositoryRoot: adopted.Repository.Root,
			BaseRef: adopted.BaseRef, Managed: adopted.Managed, UpdatedAt: now, ID: row.ID,
		})
		if err != nil {
			return err
		}
		return queries.AdoptLegacyWorktreeSessions(ctx, storedb.AdoptLegacyWorktreeSessionsParams{
			WorkingDirectory: adopted.Path, UpdatedAt: now, WorktreeID: row.ID,
		})
	})
	return row, err
}

func (adapter *CopilotAdapter) worktreeView(ctx context.Context, row storedb.Worktree) (api.Worktree, error) {
	snapshot, err := adapter.worktrees.Inspect(ctx, row.Path)
	if err != nil {
		return api.Worktree{}, err
	}
	if snapshot.State == string(api.WorktreeStateMissing) {
		return api.Worktree{}, ErrInvalidWorktree
	}
	count, err := adapter.store.CountWorktreeSessions(ctx, row.ID)
	if err != nil {
		return api.Worktree{}, err
	}
	id, repositoryID, repositoryRoot, path, baseRef := row.ID, row.RepositoryID, row.RepositoryRoot, row.Path, row.BaseRef
	branch, head, clean, managed := snapshot.Branch, snapshot.HeadSHA, snapshot.Clean, row.Managed
	created, updated := row.CreatedAt.UTC(), row.UpdatedAt.UTC()
	return api.Worktree{
		Id: &id, RepositoryId: &repositoryID, RepositoryRoot: &repositoryRoot, Path: &path,
		BaseRef: &baseRef, Branch: &branch, HeadSha: &head, Clean: &clean, Managed: &managed,
		SessionCount: &count, State: api.WorktreeState(snapshot.State), CreatedAt: &created, UpdatedAt: &updated,
	}, nil
}

func (adapter *CopilotAdapter) currentWorktreeView(ctx context.Context, row storedb.Worktree) (api.Worktree, error) {
	validated, unlock, err := adapter.lockValidatedWorktree(ctx, row)
	if errors.Is(err, ErrInvalidWorktree) {
		return adapter.missingWorktreeView(ctx, row)
	}
	if err != nil {
		return api.Worktree{}, err
	}
	view, viewErr := adapter.worktreeView(ctx, validated)
	if !errors.Is(viewErr, ErrInvalidWorktree) {
		unlock()
		return view, viewErr
	}
	quarantineErr := adapter.quarantineWorktreeSessionsLocked(ctx, row.ID)
	unlock()
	if quarantineErr != nil {
		return api.Worktree{}, quarantineErr
	}
	return adapter.missingWorktreeView(ctx, row)
}

func worktreeLookupError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fail(http.StatusNotFound, errorCodeWorktreeNotFound, "no worktree with that id")
	}
	return storeFailure(err)
}

func worktreeValidationFailure(err error) error {
	if errors.Is(err, ErrInvalidWorktree) {
		return fail(http.StatusConflict, errorCodeWorktreePrepareFailed, err.Error())
	}
	return storeFailure(err)
}
