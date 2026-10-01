package copilotadapter

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/guregu/null/v5"

	adapterconfig "github.com/candacelabs/csf/services/copilot-adapter/config"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

func (adapter *CopilotAdapter) pageLimit(requested *int32) int32 {
	if requested == nil || *requested <= 0 {
		return adapterconfig.DefaultPageLimit(adapter.config)
	}
	return *requested
}

func (adapter *CopilotAdapter) sessionView(ctx context.Context, row storedb.Session) (api.Session, error) {
	turnCount, err := adapter.store.CountSessionTurns(ctx, row.ID)
	if err != nil {
		return api.Session{}, err
	}
	view := views.Session(row)
	if err := adapter.hydrateSessionPermissionPolicy(ctx, row.ID, &view); err != nil {
		return api.Session{}, err
	}
	view.TurnCount = &turnCount
	return view, nil
}

// hydrateSessionPermissionPolicy projects the immutable, ordered child rows
// into the generated read model. Missing legacy rows remain the least-authority
// ask policy and never invent allowlist entries.
func (adapter *CopilotAdapter) hydrateSessionPermissionPolicy(ctx context.Context, sessionID uuid.UUID, view *api.Session) error {
	tools, err := adapter.store.ListSessionPermissionTools(ctx, sessionID)
	if err != nil {
		return err
	}
	globs, err := adapter.store.ListSessionPermissionShellGlobs(ctx, sessionID)
	if err != nil {
		return err
	}
	view.ToolAllowlist = nonNilStrings(tools)
	view.ShellAllowlist = nonNilStrings(globs)
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (adapter *CopilotAdapter) durableSessionView(row storedb.Session) (api.Session, error) {
	ctx, cancel := adapter.durableTransitionContext()
	defer cancel()
	return adapter.sessionView(ctx, row)
}

// closeLiveSession unregisters a session's CLI handle and closes it. A handle
// that outlives the row it belongs to is invisible to every client, so every
// path that abandons a session goes through here.
func (adapter *CopilotAdapter) closeLiveSession(identifier uuid.UUID) error {
	handle, live := adapter.sessions.lookup(identifier)
	if !live || handle.Close == nil {
		return nil
	}
	closeContext, cancelClose := adapter.durableTransitionContext()
	defer cancelClose()
	if err := handle.Close(closeContext); err != nil {
		return err
	}
	adapter.sessions.remove(identifier)
	return nil
}

// detachUnusableLiveSession gives up ownership before invoking the bounded
// Close seam. A Close implementation waiting for projector cancellation
// therefore cannot deadlock the projector or mutation handler that detected
// the failure. Normal close paths retain a failed handle so the operator can
// retry.
func (adapter *CopilotAdapter) detachUnusableLiveSession(identifier uuid.UUID) {
	handle, live := adapter.sessions.remove(identifier)
	if !live || handle.Close == nil {
		return
	}
	closeContext, cancelClose := adapter.durableTransitionContext()
	defer cancelClose()
	if err := handle.Close(closeContext); err != nil {
		adapter.logger.Warn("close detached unusable copilot session", "sessionId", identifier, "error", err)
	}
}

type sessionCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func encodeSessionCursor(row storedb.Session) (string, error) {
	body, err := json.Marshal(sessionCursor{CreatedAt: row.CreatedAt.UTC(), ID: row.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeSessionCursor(value string) (sessionCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return sessionCursor{}, err
	}
	var cursor sessionCursor
	if err := json.Unmarshal(body, &cursor); err != nil {
		return sessionCursor{}, err
	}
	if cursor.CreatedAt.IsZero() || cursor.ID == uuid.Nil {
		return sessionCursor{}, errors.New("cursor is missing its timestamp or id")
	}
	return cursor, nil
}

// GetHealth reports adapter liveness.
func (adapter *CopilotAdapter) health(ctx context.Context) (api.Health, error) {
	available := true
	if _, err := adapter.bridge.ListModels(ctx); err != nil {
		available = false
	}
	version := adapter.version
	status := api.Ok
	if !available {
		status = api.Degraded
	}
	return api.Health{Status: status, CliAvailable: &available, Version: &version}, nil
}

// ListModels lists the models the CLI can use.
func (adapter *CopilotAdapter) models(ctx context.Context) (api.ModelList, error) {
	models, err := adapter.bridge.ListModels(ctx)
	if err != nil {
		// The CLI owns the catalog. The UI disables creation when this fallback
		// is empty; POST independently verifies new requests against the CLI.
		adapter.logger.Warn("list copilot models", "error", err)
		return api.ModelList{Data: []api.Model{}}, nil
	}
	page := api.ModelList{Data: make([]api.Model, 0, len(models))}
	for _, model := range models {
		page.Data = append(page.Data, api.Model{Id: model.ID, DisplayName: model.DisplayName, Capabilities: model.Capabilities})
	}
	return page, nil
}

// ListSessions returns one page of sessions, newest first.
func (adapter *CopilotAdapter) listSessions(ctx context.Context, parameters api.ListSessionsParams) (api.SessionList, error) {
	arguments := storedb.ListSessionsParams{RowLimit: adapter.pageLimit(parameters.Limit)}
	if parameters.Status != nil {
		arguments.Status = null.StringFrom(string(*parameters.Status))
	}
	if parameters.Cursor != nil {
		parsed, err := decodeSessionCursor(*parameters.Cursor)
		if err != nil {
			return api.SessionList{}, fail(http.StatusBadRequest, errorCodeInvalidCursor, "cursor is not an opaque cursor from a previous page")
		}
		arguments.CursorCreatedAt = null.TimeFrom(parsed.CreatedAt)
		arguments.CursorID = &parsed.ID
	}
	rows, err := adapter.store.ListSessions(ctx, arguments)
	if err != nil {
		return api.SessionList{}, storeFailure(err)
	}
	page := api.SessionList{Data: make([]api.Session, 0, len(rows))}
	for _, row := range rows {
		view, err := adapter.sessionView(ctx, row)
		if err != nil {
			return api.SessionList{}, storeFailure(err)
		}
		page.Data = append(page.Data, view)
	}
	if int32(len(rows)) == arguments.RowLimit && len(rows) > 0 {
		cursor, err := encodeSessionCursor(rows[len(rows)-1])
		if err != nil {
			return api.SessionList{}, storeFailure(err)
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

type createSessionSubmission struct {
	IdempotencyKey     uuid.UUID
	Model              string
	AgentID            *string
	RepositoryID       string
	WorktreeMode       api.WorktreeMode
	WorktreeID         *uuid.UUID
	BaseRef            *string
	DisplayName        *string
	SystemInstructions *string
	PermissionPolicy   PermissionPolicy
}

// CreateSession claims the client-generated identity in relational storage
// before either worktree or SDK provisioning crosses an external boundary.
func (adapter *CopilotAdapter) createSession(ctx context.Context, submission createSessionSubmission) (api.Session, error) {
	unlock := adapter.creations.lock(submission.IdempotencyKey)
	defer unlock()
	if err := adapter.validateNewSessionModel(ctx, submission); err != nil {
		return api.Session{}, err
	}
	receipt, err := adapter.claimSessionCreation(ctx, submission)
	if err != nil {
		return api.Session{}, err
	}
	row, lookupErr := adapter.store.GetSession(ctx, receipt.SessionID)
	if lookupErr == nil &&
		(receipt.CompletedAt.Valid || row.Status != string(api.SessionStatusStarting)) {
		unlockSession := adapter.mutations.lock(receipt.SessionID)
		defer unlockSession()
		row, lookupErr = adapter.store.GetSession(ctx, receipt.SessionID)
		if lookupErr != nil {
			return api.Session{}, storeFailure(lookupErr)
		}
		return createdSessionView(row, submission.PermissionPolicy), nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return api.Session{}, storeFailure(lookupErr)
	}
	row, err = adapter.createSessionUnderWorktreeFence(ctx, submission, receipt)
	if err != nil {
		return api.Session{}, err
	}
	return createdSessionView(row, submission.PermissionPolicy), nil
}

func (adapter *CopilotAdapter) validateNewSessionModel(ctx context.Context, submission createSessionSubmission) error {
	_, err := adapter.store.GetSessionCreation(ctx, submission.IdempotencyKey)
	if err == nil {
		// Retrying an accepted identity must still work if the provider changes
		// its catalog or is unavailable. ClaimSessionCreation checks body equality.
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return storeFailure(err)
	}
	models, err := adapter.bridge.ListModels(ctx)
	if err != nil {
		adapter.logger.Warn("validate new session model", "error", err)
		return fail(http.StatusBadGateway, errorCodeCLIUnavailable, "the Copilot model catalog is unavailable; retry session creation when /v1/models is available")
	}
	for _, model := range models {
		if model.ID == submission.Model {
			return nil
		}
	}
	return fail(http.StatusBadRequest, errorCodeInvalidRequest, "model must match an id advertised by /v1/models")
}

// createSessionUnderWorktreeFence follows the global creation lock order:
// idempotency receipt (owned by the caller), canonical path, then session.
// Keeping the path fence through SDK activation prevents quarantine from
// taking an earlier session snapshot and allowing a late live handle.
func (adapter *CopilotAdapter) createSessionUnderWorktreeFence(
	ctx context.Context,
	submission createSessionSubmission,
	receipt storedb.SessionCreation,
) (storedb.Session, error) {
	path, selected, prepared, err := adapter.sessionCreationWorktreePath(ctx, submission, receipt.SessionID)
	if err != nil {
		return storedb.Session{}, err
	}
	unlockWorktree := adapter.worktreeMutations.lock(path)
	defer unlockWorktree()
	if selected != nil {
		validated, validationErr := adapter.validatedWorktree(ctx, *selected)
		if validationErr != nil {
			if !errors.Is(validationErr, ErrInvalidWorktree) {
				return storedb.Session{}, storeFailure(validationErr)
			}
			_, quarantined, quarantineErr := adapter.quarantineInvalidWorktreeLocked(ctx, *selected)
			if quarantineErr != nil {
				return storedb.Session{}, storeFailure(quarantineErr)
			}
			if quarantined {
				persisted, lookupErr := adapter.store.GetSession(ctx, receipt.SessionID)
				if lookupErr == nil {
					return persisted, nil
				}
				if !errors.Is(lookupErr, sql.ErrNoRows) {
					return storedb.Session{}, storeFailure(lookupErr)
				}
				return storedb.Session{}, fail(http.StatusNotFound, errorCodeWorktreeNotFound, "the worktree path is missing")
			}
		} else {
			*selected = validated
		}
	}
	if prepared == nil {
		preparedWorktree, prepareErr := adapter.prepareSessionWorktree(ctx, submission, receipt.SessionID)
		if prepareErr != nil {
			return storedb.Session{}, prepareErr
		}
		prepared = &preparedWorktree
	} else if selected == nil {
		revalidated, validationErr := adapter.worktrees.Reuse(ctx, prepared.Repository.ID, prepared.Path)
		if validationErr != nil {
			return storedb.Session{}, worktreeValidationFailure(validationErr)
		}
		if revalidated.Repository.Root != prepared.Repository.Root || revalidated.Path != prepared.Path {
			return storedb.Session{}, fail(http.StatusConflict, errorCodeWorktreePrepareFailed,
				"the prepared worktree changed canonical identity before session creation")
		}
	} else {
		prepared.BaseRef, prepared.Managed = selected.BaseRef, selected.Managed
	}
	unlockSession := adapter.mutations.lock(receipt.SessionID)
	defer unlockSession()
	row, lookupErr := adapter.store.GetSession(ctx, receipt.SessionID)
	if lookupErr == nil &&
		(receipt.CompletedAt.Valid || row.Status != string(api.SessionStatusStarting)) {
		return row, nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return storedb.Session{}, storeFailure(lookupErr)
	}
	row, preparedWorktree, err := adapter.ensureStartingSessionWithWorktree(ctx, submission, receipt, *prepared)
	if err != nil {
		return storedb.Session{}, err
	}
	row, err = adapter.activateStartingSession(row, preparedWorktree, receipt)
	if err != nil {
		return storedb.Session{}, fail(http.StatusBadGateway, errorCodeCLIUnavailable, err.Error())
	}
	return row, nil
}

func (adapter *CopilotAdapter) sessionCreationWorktreePath(
	ctx context.Context,
	submission createSessionSubmission,
	sessionID uuid.UUID,
) (string, *storedb.Worktree, *PreparedWorktree, error) {
	row, err := adapter.store.GetSession(ctx, sessionID)
	if err == nil {
		worktree, lookupErr := adapter.store.GetWorktree(ctx, row.WorktreeID)
		if lookupErr != nil {
			return "", nil, nil, storeFailure(lookupErr)
		}
		return worktree.Path, &worktree, nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil, storeFailure(err)
	}
	if submission.WorktreeMode == api.ReuseExistingWorktree {
		if submission.WorktreeID == nil {
			return "", nil, nil, fail(http.StatusBadRequest, errorCodeInvalidRequest, "reuseExistingWorktree requires worktreeId")
		}
		worktree, err := adapter.store.GetWorktree(ctx, *submission.WorktreeID)
		if err != nil {
			return "", nil, nil, lookupFailure(err, errorCodeWorktreeNotFound, "no worktree with that id")
		}
		return worktree.Path, &worktree, nil, nil
	}
	prepared, err := adapter.prepareSessionWorktree(ctx, submission, sessionID)
	if err != nil {
		return "", nil, nil, err
	}
	worktree, lookupErr := adapter.store.GetWorktreeByPath(ctx, prepared.Path)
	if lookupErr == nil {
		return prepared.Path, &worktree, &prepared, nil
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return "", nil, nil, storeFailure(lookupErr)
	}
	return prepared.Path, nil, &prepared, nil
}

func createdSessionView(row storedb.Session, policy PermissionPolicy) api.Session {
	view := views.Session(row)
	// The creation receipt represents the original response, before any turn
	// can exist. The validated submission carries the immutable policy, avoiding
	// a fallible post-commit read that could turn a durable 201 into a 500.
	view.ToolAllowlist = append([]string{}, policy.ToolAllowlist...)
	view.ShellAllowlist = append([]string{}, policy.ShellAllowlist...)
	turnCount := int64(0)
	view.TurnCount = &turnCount
	return view
}

func (adapter *CopilotAdapter) ensureStartingSession(
	ctx context.Context,
	submission createSessionSubmission,
	receipt storedb.SessionCreation,
) (storedb.Session, PreparedWorktree, error) {
	prepared, err := adapter.prepareSessionWorktree(ctx, submission, receipt.SessionID)
	if err != nil {
		return storedb.Session{}, PreparedWorktree{}, err
	}
	return adapter.ensureStartingSessionWithWorktree(ctx, submission, receipt, prepared)
}

func (adapter *CopilotAdapter) ensureStartingSessionWithWorktree(
	ctx context.Context,
	submission createSessionSubmission,
	receipt storedb.SessionCreation,
	prepared PreparedWorktree,
) (storedb.Session, PreparedWorktree, error) {
	existing, lookupErr := adapter.store.GetSession(ctx, receipt.SessionID)
	if lookupErr == nil {
		if validateErr := adapter.validatePersistedCreationWorktree(ctx, existing, prepared); validateErr != nil {
			return storedb.Session{}, PreparedWorktree{}, validateErr
		}
		return existing, prepared, nil
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return storedb.Session{}, PreparedWorktree{}, storeFailure(lookupErr)
	}
	displayName := submission.Model
	if submission.DisplayName != nil {
		displayName = *submission.DisplayName
	}
	instructions := ""
	if submission.SystemInstructions != nil {
		instructions = *submission.SystemInstructions
	}
	durableContext, cancelPersistence := adapter.durableTransitionContext()
	defer cancelPersistence()
	now := receipt.CreatedAt.UTC()
	var row storedb.Session
	err := adapter.store.Transact(durableContext, func(queries storedb.Querier) error {
		worktree, err := queries.CreateWorktree(durableContext, storedb.CreateWorktreeParams{
			ID: receipt.SessionID, RepositoryID: prepared.Repository.ID, RepositoryRoot: prepared.Repository.Root,
			Path: prepared.Path, BaseRef: prepared.BaseRef, Managed: prepared.Managed,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if err = validateCreationWorktreeRow(worktree, prepared); err != nil {
			return err
		}
		row, err = queries.CreateSession(durableContext, storedb.CreateSessionParams{
			ID: receipt.SessionID, WorktreeID: worktree.ID, DisplayName: displayName, Model: submission.Model,
			AgentID:          null.StringFromPtr(submission.AgentID).String,
			WorkingDirectory: prepared.Path, SystemInstructions: instructions,
			PermissionMode: string(submission.PermissionPolicy.Mode),
			Status:         string(api.SessionStatusStarting), CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := createSessionPermissionPolicy(durableContext, queries, receipt.SessionID, submission.PermissionPolicy); err != nil {
			return err
		}
		return queries.EnsureSessionCounter(durableContext, receipt.SessionID)
	})
	if err != nil {
		transactionErr := err
		reconcileContext, cancelReconciliation := adapter.durableTransitionContext()
		persisted, reconcileErr := adapter.store.GetSession(reconcileContext, receipt.SessionID)
		cancelReconciliation()
		switch {
		case reconcileErr == nil:
			row = persisted
		case errors.Is(reconcileErr, sql.ErrNoRows):
			return storedb.Session{}, PreparedWorktree{}, storeFailure(transactionErr)
		default:
			return storedb.Session{}, PreparedWorktree{}, storeFailure(errors.Join(transactionErr,
				fmt.Errorf("reconcile starting session %s: %w", receipt.SessionID, reconcileErr)))
		}
	}
	return row, prepared, nil
}

func validateCreationWorktreeRow(row storedb.Worktree, prepared PreparedWorktree) error {
	if row.RepositoryID != prepared.Repository.ID || row.RepositoryRoot != prepared.Repository.Root ||
		row.Path != prepared.Path || row.BaseRef != prepared.BaseRef || row.Managed != prepared.Managed {
		return fmt.Errorf("persist starting session: worktree path is already owned by different metadata")
	}
	return nil
}

func (adapter *CopilotAdapter) validatePersistedCreationWorktree(
	ctx context.Context,
	session storedb.Session,
	prepared PreparedWorktree,
) error {
	row, err := adapter.store.GetWorktree(ctx, session.WorktreeID)
	if err != nil {
		return storeFailure(err)
	}
	if err := validateCreationWorktreeRow(row, prepared); err != nil {
		return fail(http.StatusConflict, errorCodeWorktreePrepareFailed, err.Error())
	}
	return nil
}

func (adapter *CopilotAdapter) activateStartingSession(
	row storedb.Session,
	prepared PreparedWorktree,
	receipt storedb.SessionCreation,
) (storedb.Session, error) {
	if row.Status != string(api.SessionStatusStarting) {
		return row, nil
	}
	spec := BridgeSessionSpec{
		SessionID: row.ID, Model: row.Model, AgentID: row.AgentID, WorkingDirectory: prepared.Path,
		SystemInstructions: row.SystemInstructions,
	}
	policyContext, cancelPolicy := adapter.durableTransitionContext()
	policy, err := adapter.sessionPermissionPolicy(policyContext, row.ID, row.PermissionMode)
	cancelPolicy()
	if err != nil {
		return storedb.Session{}, err
	}
	spec.PermissionPolicy = policy
	handle, retained := adapter.sessions.lookup(row.ID)
	ownedAttempt := false
	if !retained && receipt.SdkCreateAttemptID == nil {
		attemptID := uuid.New()
		attemptContext, cancelAttempt := adapter.durableTransitionContext()
		attempt, err := adapter.store.BeginSessionCreationSDKAttempt(
			attemptContext,
			storedb.BeginSessionCreationSDKAttemptParams{
				SdkCreateAttemptID: &attemptID, IdempotencyKey: receipt.IdempotencyKey, SessionID: row.ID,
			},
		)
		cancelAttempt()
		if err == nil {
			receipt = attempt
		} else {
			// Every error, including a lost acknowledgement, is resolved from
			// the receipt itself. The exact persisted token alone owns Create;
			// a different token can only Resume, and no token is retryable.
			attemptErr := err
			lookupContext, cancelLookup := adapter.durableTransitionContext()
			receipt, err = adapter.store.GetSessionCreation(lookupContext, receipt.IdempotencyKey)
			cancelLookup()
			if err != nil {
				return storedb.Session{}, errors.Join(
					fmt.Errorf("claim SDK create attempt: %w", attemptErr),
					fmt.Errorf("load claimed SDK attempt: %w", err),
				)
			}
			if receipt.SdkCreateAttemptID == nil {
				return storedb.Session{}, fmt.Errorf("claim SDK create attempt remains uncommitted: %w", attemptErr)
			}
		}
		if receipt.SdkCreateAttemptID == nil {
			return storedb.Session{}, errors.New("claim SDK create attempt returned no durable owner")
		}
		ownedAttempt = *receipt.SdkCreateAttemptID == attemptID
	}
	if retained {
		// A previous request lost the completion acknowledgement after the SDK
		// session was created. Keep using that exact handle; creating or
		// resuming another one would leak the first identity.
	} else if ownedAttempt {
		bridgeContext, cancelBridge := adapter.durableTransitionContext()
		handle, err = adapter.bridge.CreateSession(bridgeContext, spec)
		cancelBridge()
		if err != nil {
			createErr := err
			reconcileContext, cancelReconcile := adapter.durableTransitionContext()
			handle, err = adapter.bridge.ResumeSession(reconcileContext, spec)
			cancelReconcile()
			if errors.Is(err, ErrBridgeSessionMissing) {
				failed, failErr := adapter.failStartingSession(row.ID, receipt, createErr)
				if failErr != nil {
					return storedb.Session{}, errors.Join(createErr, failErr)
				}
				return failed, nil
			}
			if err != nil {
				return storedb.Session{}, errors.Join(createErr, fmt.Errorf("reconcile SDK create: %w", err))
			}
		}
	} else {
		resumeContext, cancelResume := adapter.durableTransitionContext()
		handle, err = adapter.bridge.ResumeSession(resumeContext, spec)
		cancelResume()
		if errors.Is(err, ErrBridgeSessionMissing) {
			return adapter.failStartingSession(row.ID, receipt, err)
		}
		if err != nil {
			return storedb.Session{}, fmt.Errorf("resume attempted SDK session: %w", err)
		}
	}
	completed, err := adapter.completeStartingSession(row.ID, receipt)
	if err != nil {
		reconciled, committed, reconcileErr := adapter.reconcileStartingSession(row.ID, receipt.IdempotencyKey)
		if reconcileErr != nil {
			adapter.retainBridgeSession(row.ID, handle)
			return storedb.Session{}, errors.Join(err, reconcileErr)
		}
		if committed {
			if reconciled.Status != string(api.SessionStatusFailed) && reconciled.Status != string(api.SessionStatusEnded) {
				adapter.retainBridgeSession(row.ID, handle)
			}
			return reconciled, nil
		}
		adapter.retainBridgeSession(row.ID, handle)
		return storedb.Session{}, err
	}
	// The bridge can buffer events while the durable transition runs. Attach
	// only after both the starting->idle CAS and receipt completion commit.
	adapter.retainBridgeSession(row.ID, handle)
	return completed, nil
}

// reconcileStartingSession distinguishes a lost COMMIT acknowledgement from
// a transaction that did not commit. Only the pair idle+completed proves the
// transition; starting+incomplete remains safely retryable with the exact live
// handle retained in this process. A later terminal transition is also proof
// once the receipt is complete because both original phase writes were atomic.
func (adapter *CopilotAdapter) reconcileStartingSession(
	sessionID uuid.UUID,
	idempotencyKey uuid.UUID,
) (storedb.Session, bool, error) {
	ctx, cancel := adapter.durableTransitionContext()
	defer cancel()
	row, err := adapter.store.GetSession(ctx, sessionID)
	if err != nil {
		return storedb.Session{}, false, fmt.Errorf("reconcile starting session: %w", err)
	}
	receipt, err := adapter.store.GetSessionCreation(ctx, idempotencyKey)
	if err != nil {
		return storedb.Session{}, false, fmt.Errorf("reconcile starting session receipt: %w", err)
	}
	if receipt.SessionID != sessionID {
		return storedb.Session{}, false, fmt.Errorf("reconcile starting session: receipt belongs to %s", receipt.SessionID)
	}
	switch {
	case row.Status != string(api.SessionStatusStarting) && receipt.CompletedAt.Valid:
		return row, true, nil
	case row.Status == string(api.SessionStatusStarting) && !receipt.CompletedAt.Valid:
		return row, false, nil
	default:
		return storedb.Session{}, false, fmt.Errorf(
			"reconcile starting session: inconsistent status %q and completed receipt %t",
			row.Status, receipt.CompletedAt.Valid,
		)
	}
}

func (adapter *CopilotAdapter) retainBridgeSession(sessionID uuid.UUID, handle BridgeSession) {
	if _, live := adapter.sessions.lookup(sessionID); live {
		return
	}
	adapter.attach(sessionID, handle)
}

func (adapter *CopilotAdapter) completeStartingSession(sessionID uuid.UUID, receipt storedb.SessionCreation) (storedb.Session, error) {
	if receipt.SdkCreateAttemptID == nil {
		return storedb.Session{}, errors.New("complete starting session: SDK create attempt is missing")
	}
	ctx, cancel := adapter.durableTransitionContext()
	defer cancel()
	now := time.Now().UTC()
	var row storedb.Session
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		var err error
		row, err = queries.CompleteStartingSession(ctx, storedb.CompleteStartingSessionParams{UpdatedAt: now, ID: sessionID})
		if err != nil {
			return err
		}
		_, err = queries.CompleteSessionCreation(ctx, storedb.CompleteSessionCreationParams{
			CompletedAt: null.TimeFrom(now), IdempotencyKey: receipt.IdempotencyKey,
			SessionID: sessionID, SdkCreateAttemptID: receipt.SdkCreateAttemptID,
		})
		return err
	})
	if err != nil {
		return storedb.Session{}, fmt.Errorf("complete starting session: %w", err)
	}
	return row, nil
}

func (adapter *CopilotAdapter) failStartingSession(sessionID uuid.UUID, receipt storedb.SessionCreation, createErr error) (storedb.Session, error) {
	if receipt.SdkCreateAttemptID == nil {
		return storedb.Session{}, errors.New("fail starting session: SDK create attempt is missing")
	}
	ctx, cancel := adapter.durableTransitionContext()
	defer cancel()
	now := time.Now().UTC()
	code := api.FailureCodeSessionCreationFailed
	if errors.Is(createErr, ErrBridgeSessionMissing) {
		code = api.FailureCodeProviderSessionMissing
	}
	var row storedb.Session
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		var err error
		row, err = queries.FailStartingSession(ctx, storedb.FailStartingSessionParams{
			EndedAt: null.TimeFrom(now), UpdatedAt: now, ID: sessionID,
			FailureCode:   int32(code),
			FailureReason: normalizeFailureReason(createErrString(createErr), sessionFailureDescription(code)),
		})
		if err != nil {
			return err
		}
		_, err = queries.CompleteSessionCreation(ctx, storedb.CompleteSessionCreationParams{
			CompletedAt: null.TimeFrom(now), IdempotencyKey: receipt.IdempotencyKey,
			SessionID: sessionID, SdkCreateAttemptID: receipt.SdkCreateAttemptID,
		})
		return err
	})
	if err != nil {
		reconciled, committed, reconcileErr := adapter.reconcileStartingSession(sessionID, receipt.IdempotencyKey)
		if reconcileErr == nil && committed && reconciled.Status == string(api.SessionStatusFailed) {
			return reconciled, nil
		}
		return storedb.Session{}, errors.Join(fmt.Errorf("fail missing SDK session: %w", err), reconcileErr)
	}
	return row, nil
}

func createErrString(createErr error) string {
	if createErr == nil {
		return ""
	}
	return createErr.Error()
}

func decodeCreateSessionRequest(request api.CreateSessionRequest) (createSessionSubmission, error) {
	discriminator, err := request.Discriminator()
	if err != nil {
		return createSessionSubmission{}, fail(http.StatusBadRequest, errorCodeInvalidBody, "the session body is not a valid worktree-mode request")
	}
	var submission createSessionSubmission
	switch discriminator {
	case string(api.NewWorktree):
		body, decodeErr := request.AsNewWorktreeSessionRequest()
		if decodeErr != nil {
			return createSessionSubmission{}, fail(http.StatusBadRequest, errorCodeInvalidBody, "the new-worktree request is invalid")
		}
		submission = createSessionSubmission{
			IdempotencyKey: uuid.UUID(body.IdempotencyKey), Model: body.Model, AgentID: body.AgentId, RepositoryID: body.RepositoryId,
			WorktreeMode: api.NewWorktree, BaseRef: body.BaseRef, DisplayName: body.DisplayName,
			SystemInstructions: body.SystemInstructions,
		}
		submission.PermissionPolicy, err = decodePermissionPolicy(body.Permissions, body.ToolAllowlist, body.ShellAllowlist)
	case string(api.ReuseExistingWorktree):
		body, decodeErr := request.AsExistingWorktreeSessionRequest()
		if decodeErr != nil {
			return createSessionSubmission{}, fail(http.StatusBadRequest, errorCodeInvalidBody, "the existing-worktree request is invalid")
		}
		worktreeID := uuid.UUID(body.WorktreeId)
		submission = createSessionSubmission{
			IdempotencyKey: uuid.UUID(body.IdempotencyKey), Model: body.Model, AgentID: body.AgentId, RepositoryID: body.RepositoryId,
			WorktreeMode: api.ReuseExistingWorktree, WorktreeID: &worktreeID, DisplayName: body.DisplayName,
			SystemInstructions: body.SystemInstructions,
		}
		submission.PermissionPolicy, err = decodePermissionPolicy(body.Permissions, body.ToolAllowlist, body.ShellAllowlist)
	case string(api.ReuseCurrentWorktree):
		body, decodeErr := request.AsCurrentWorktreeSessionRequest()
		if decodeErr != nil {
			return createSessionSubmission{}, fail(http.StatusBadRequest, errorCodeInvalidBody, "the current-worktree request is invalid")
		}
		submission = createSessionSubmission{
			IdempotencyKey: uuid.UUID(body.IdempotencyKey), Model: body.Model, AgentID: body.AgentId, RepositoryID: body.RepositoryId,
			WorktreeMode: api.ReuseCurrentWorktree, DisplayName: body.DisplayName,
			SystemInstructions: body.SystemInstructions,
		}
		submission.PermissionPolicy, err = decodePermissionPolicy(body.Permissions, body.ToolAllowlist, body.ShellAllowlist)
	default:
		return createSessionSubmission{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, "worktreeMode is not supported")
	}
	if err != nil {
		return createSessionSubmission{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, err.Error())
	}
	if submission.IdempotencyKey == uuid.Nil || submission.Model == "" || submission.RepositoryID == "" {
		return createSessionSubmission{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, "idempotencyKey, model, and repositoryId are required")
	}
	return submission, nil
}

func decodePermissionPolicy(
	mode *api.PermissionPolicyMode,
	tools *api.PermissionToolAllowlist,
	globs *api.PermissionShellAllowlist,
) (PermissionPolicy, error) {
	policy := PermissionPolicy{Mode: api.Ask}
	if mode != nil {
		policy.Mode = *mode
	}
	if tools != nil {
		policy.ToolAllowlist = append([]string(nil), *tools...)
	}
	if globs != nil {
		policy.ShellAllowlist = append([]string(nil), *globs...)
	}
	switch policy.Mode {
	case api.Ask, api.ApproveAll, api.Allowlist:
	default:
		return PermissionPolicy{}, fmt.Errorf("permissions must be ask, approveAll, or allowlist")
	}
	if policy.Mode != api.Allowlist && (len(policy.ToolAllowlist) != 0 || len(policy.ShellAllowlist) != 0) {
		return PermissionPolicy{}, fmt.Errorf("toolAllowlist and shellAllowlist require permissions allowlist")
	}
	if err := validatePermissionAllowlist(policy.ToolAllowlist, 256, "toolAllowlist"); err != nil {
		return PermissionPolicy{}, err
	}
	if err := validatePermissionAllowlist(policy.ShellAllowlist, 1024, "shellAllowlist"); err != nil {
		return PermissionPolicy{}, err
	}
	for _, glob := range policy.ShellAllowlist {
		if _, err := path.Match(glob, ""); err != nil {
			return PermissionPolicy{}, fmt.Errorf("shellAllowlist contains an invalid glob")
		}
	}
	return policy, nil
}

func validatePermissionAllowlist(values []string, maximumLength int, field string) error {
	if len(values) > 128 {
		return fmt.Errorf("%s must contain at most 128 entries", field)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || strings.IndexByte(value, 0) >= 0 || utf8.RuneCountInString(value) > maximumLength {
			return fmt.Errorf("%s contains an invalid entry", field)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s entries must be unique", field)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (adapter *CopilotAdapter) claimSessionCreation(ctx context.Context, submission createSessionSubmission) (storedb.SessionCreation, error) {
	agentID := null.StringFromPtr(submission.AgentID)
	if agentID.String == "" {
		agentID = null.String{}
	}
	parameters := storedb.ClaimSessionCreationParams{
		IdempotencyKey: submission.IdempotencyKey, SessionID: uuid.New(), Model: submission.Model, AgentID: agentID,
		RepositoryID: submission.RepositoryID, WorktreeMode: string(submission.WorktreeMode),
		WorktreeID: submission.WorktreeID, BaseRef: null.StringFromPtr(submission.BaseRef),
		DisplayName: null.StringFromPtr(submission.DisplayName), SystemInstructions: null.StringFromPtr(submission.SystemInstructions),
		PermissionMode: string(submission.PermissionPolicy.Mode),
		CreatedAt:      time.Now().UTC(),
	}
	var receipt storedb.SessionCreation
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		var transactionErr error
		receipt, transactionErr = queries.ClaimSessionCreation(ctx, parameters)
		if transactionErr != nil {
			return transactionErr
		}
		return createSessionCreationPermissionPolicy(ctx, queries, receipt.IdempotencyKey, submission.PermissionPolicy)
	})
	if err == nil {
		return receipt, nil
	}
	claimErr := err
	lookupContext, cancelLookup := adapter.durableTransitionContext()
	defer cancelLookup()
	receipt, lookupErr := adapter.store.GetSessionCreation(lookupContext, submission.IdempotencyKey)
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return storedb.SessionCreation{}, storeFailure(claimErr)
	}
	if lookupErr != nil {
		return storedb.SessionCreation{}, storeFailure(errors.Join(claimErr, lookupErr))
	}
	if !sameSessionCreation(receipt, submission) {
		return storedb.SessionCreation{}, fail(http.StatusConflict, errorCodeIdempotencyKeyReused, "idempotencyKey already identifies a different session request")
	}
	tools, toolsErr := adapter.store.ListSessionCreationPermissionTools(lookupContext, receipt.IdempotencyKey)
	if toolsErr != nil {
		return storedb.SessionCreation{}, storeFailure(toolsErr)
	}
	globs, globsErr := adapter.store.ListSessionCreationPermissionShellGlobs(lookupContext, receipt.IdempotencyKey)
	if globsErr != nil {
		return storedb.SessionCreation{}, storeFailure(globsErr)
	}
	if !slices.Equal(tools, submission.PermissionPolicy.ToolAllowlist) || !slices.Equal(globs, submission.PermissionPolicy.ShellAllowlist) {
		return storedb.SessionCreation{}, fail(http.StatusConflict, errorCodeIdempotencyKeyReused, "idempotencyKey already identifies a different session request")
	}
	return receipt, nil
}

func sameSessionCreation(receipt storedb.SessionCreation, submission createSessionSubmission) bool {
	agentID := null.StringFromPtr(submission.AgentID)
	if agentID.String == "" {
		agentID = null.String{}
	}
	return receipt.IdempotencyKey == submission.IdempotencyKey &&
		receipt.Model == submission.Model && receipt.AgentID.Equal(agentID) && receipt.RepositoryID == submission.RepositoryID &&
		receipt.WorktreeMode == string(submission.WorktreeMode) && sameUUIDPointer(receipt.WorktreeID, submission.WorktreeID) &&
		receipt.BaseRef.Equal(null.StringFromPtr(submission.BaseRef)) &&
		receipt.DisplayName.Equal(null.StringFromPtr(submission.DisplayName)) &&
		receipt.SystemInstructions.Equal(null.StringFromPtr(submission.SystemInstructions)) &&
		receipt.PermissionMode == string(submission.PermissionPolicy.Mode)
}

func createSessionCreationPermissionPolicy(ctx context.Context, queries storedb.Querier, key uuid.UUID, policy PermissionPolicy) error {
	return writePermissionPolicy(policy,
		func(position int32, tool string) error {
			return queries.CreateSessionCreationPermissionTool(ctx, storedb.CreateSessionCreationPermissionToolParams{
				IdempotencyKey: key, Position: position, ToolName: tool,
			})
		},
		func(position int32, glob string) error {
			return queries.CreateSessionCreationPermissionShellGlob(ctx, storedb.CreateSessionCreationPermissionShellGlobParams{
				IdempotencyKey: key, Position: position, ShellGlob: glob,
			})
		})
}

func createSessionPermissionPolicy(ctx context.Context, queries storedb.Querier, sessionID uuid.UUID, policy PermissionPolicy) error {
	return writePermissionPolicy(policy,
		func(position int32, tool string) error {
			return queries.CreateSessionPermissionTool(ctx, storedb.CreateSessionPermissionToolParams{
				SessionID: sessionID, Position: position, ToolName: tool,
			})
		},
		func(position int32, glob string) error {
			return queries.CreateSessionPermissionShellGlob(ctx, storedb.CreateSessionPermissionShellGlobParams{
				SessionID: sessionID, Position: position, ShellGlob: glob,
			})
		})
}

// writePermissionPolicy preserves list order and stops on the first failed
// insert. The caller's transaction owns rollback for both lists together.
func writePermissionPolicy(policy PermissionPolicy, writeTool func(position int32, tool string) error, writeGlob func(position int32, glob string) error) error {
	for position, tool := range policy.ToolAllowlist {
		if err := writeTool(int32(position), tool); err != nil {
			return err
		}
	}
	for position, glob := range policy.ShellAllowlist {
		if err := writeGlob(int32(position), glob); err != nil {
			return err
		}
	}
	return nil
}

func (adapter *CopilotAdapter) sessionPermissionPolicy(ctx context.Context, sessionID uuid.UUID, mode string) (PermissionPolicy, error) {
	tools, err := adapter.store.ListSessionPermissionTools(ctx, sessionID)
	if err != nil {
		return PermissionPolicy{}, storeFailure(err)
	}
	globs, err := adapter.store.ListSessionPermissionShellGlobs(ctx, sessionID)
	if err != nil {
		return PermissionPolicy{}, storeFailure(err)
	}
	tools, globs = nonNilStrings(tools), nonNilStrings(globs)
	policy, err := decodePermissionPolicy(pointerPermissionPolicyMode(api.PermissionPolicyMode(mode)), &tools, &globs)
	if err != nil {
		return PermissionPolicy{}, storeFailure(err)
	}
	return policy, nil
}

func pointerPermissionPolicyMode(value api.PermissionPolicyMode) *api.PermissionPolicyMode {
	return &value
}

func sameUUIDPointer(left *uuid.UUID, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (adapter *CopilotAdapter) prepareSessionWorktree(
	ctx context.Context,
	submission createSessionSubmission,
	sessionID uuid.UUID,
) (PreparedWorktree, error) {
	baseRef := ""
	if submission.BaseRef != nil {
		baseRef = *submission.BaseRef
	}
	if submission.WorktreeMode != api.ReuseExistingWorktree {
		if submission.WorktreeID != nil {
			return PreparedWorktree{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, "worktreeId requires reuseExistingWorktree")
		}
		if submission.WorktreeMode == api.ReuseCurrentWorktree && submission.BaseRef != nil {
			return PreparedWorktree{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, "baseRef is only valid for a new worktree")
		}
		prepared, err := adapter.worktrees.Prepare(ctx, WorktreeRequest{
			RepositoryID: submission.RepositoryID, Mode: string(submission.WorktreeMode), BaseRef: baseRef, SessionID: sessionID,
		})
		if err != nil {
			return PreparedWorktree{}, fail(http.StatusConflict, errorCodeWorktreePrepareFailed, err.Error())
		}
		return prepared, nil
	}
	if submission.WorktreeID == nil {
		return PreparedWorktree{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, "reuseExistingWorktree requires worktreeId")
	}
	if submission.BaseRef != nil {
		return PreparedWorktree{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, "baseRef is only valid for a new worktree")
	}
	row, err := adapter.store.GetWorktree(ctx, *submission.WorktreeID)
	if err != nil {
		return PreparedWorktree{}, lookupFailure(err, errorCodeWorktreeNotFound, "no worktree with that id")
	}
	if row.RepositoryID == "legacy" {
		row, err = adapter.validatedWorktree(ctx, row)
		if err != nil {
			return PreparedWorktree{}, worktreeValidationFailure(err)
		}
	}
	if row.RepositoryID != submission.RepositoryID {
		return PreparedWorktree{}, fail(http.StatusConflict, errorCodeWorktreePrepareFailed, "the worktree belongs to another repository")
	}
	prepared, err := adapter.worktrees.Reuse(ctx, row.RepositoryID, row.Path)
	if err != nil {
		return PreparedWorktree{}, worktreeValidationFailure(err)
	}
	if prepared.Repository.Root != row.RepositoryRoot || prepared.Path != row.Path {
		err = fmt.Errorf("%w: the selected worktree does not match configured canonical paths", ErrInvalidWorktree)
		return PreparedWorktree{}, worktreeValidationFailure(err)
	}
	prepared.BaseRef, prepared.Managed = row.BaseRef, row.Managed
	return prepared, nil
}

// GetSession returns one session.
func (adapter *CopilotAdapter) getSession(ctx context.Context, sessionID uuid.UUID) (api.Session, error) {
	row, err := adapter.store.GetSession(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Session{}, fail(http.StatusNotFound, errorCodeSessionNotFound, "no session with that id")
	}
	if err != nil {
		return api.Session{}, storeFailure(err)
	}
	view, err := adapter.sessionView(ctx, row)
	if err != nil {
		return api.Session{}, storeFailure(err)
	}
	return view, nil
}

// GetActiveTurn returns the durable running turn a client must bind into an
// abort. The abort handler rechecks the identity at the CLI boundary.
func (adapter *CopilotAdapter) activeTurn(ctx context.Context, sessionID uuid.UUID) (api.Turn, error) {
	if err := adapter.requireSession(ctx, sessionID); err != nil {
		return api.Turn{}, err
	}
	turn, err := adapter.store.GetRunningTurn(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Turn{}, fail(http.StatusNotFound, errorCodeNoTurnInFlight, "the session has no running turn")
	}
	if err != nil {
		return api.Turn{}, storeFailure(err)
	}
	return views.Turn(turn), nil
}

// UpdateSession changes a session's model or display name.
func (adapter *CopilotAdapter) updateSession(ctx context.Context, sessionID uuid.UUID, patch api.UpdateSessionRequest) (api.Session, error) {
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	existing, err := adapter.store.GetSession(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Session{}, fail(http.StatusNotFound, errorCodeSessionNotFound, "no session with that id")
	}
	if err != nil {
		return api.Session{}, storeFailure(err)
	}
	if err := rejectStartingSession(existing); err != nil {
		return api.Session{}, err
	}
	if existing.Status == string(api.SessionStatusEnded) || existing.Status == string(api.SessionStatusFailed) {
		return api.Session{}, fail(http.StatusConflict, errorCodeSessionTerminal, "the session has ended")
	}
	arguments := storedb.UpdateSessionMetadataParams{ID: sessionID, UpdatedAt: time.Now().UTC()}
	if patch.Model != nil {
		arguments.Model = null.StringFrom(*patch.Model)
	}
	if patch.DisplayName != nil {
		arguments.DisplayName = null.StringFrom(*patch.DisplayName)
	}
	// A CLI switched to a model the store never recorded is the one state
	// nothing can reconcile later, so the switch carries its own undo.
	var restoreModel func() error
	if patch.Model != nil {
		if handle, live := adapter.sessions.lookup(sessionID); live && handle.SetModel != nil {
			previous, setModel := existing.Model, handle.SetModel
			restoreModel = func() error {
				durableContext, cancelDurableTransition := adapter.durableTransitionContext()
				defer cancelDurableTransition()
				if err := setModel(durableContext, previous); err != nil {
					adapter.detachUnusableLiveSession(sessionID)
					adapter.logger.Error("restore the copilot model after a failed model transition",
						"sessionId", sessionID, "model", previous, "error", err)
					return err
				}
				return nil
			}
			if err := handle.SetModel(ctx, *patch.Model); err != nil {
				rollbackErr := restoreModel()
				if rollbackErr != nil {
					err = errors.Join(err, fmt.Errorf("restore previous model: %w", rollbackErr))
				}
				return api.Session{}, fail(http.StatusInternalServerError, errorCodeCLIModelSwitchFailed, err.Error())
			}
		}
	}
	var row storedb.Session
	var eventSeq int64
	err = adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		row, err = queries.UpdateSessionMetadata(ctx, arguments)
		if err != nil {
			return err
		}
		eventSeq, err = insertSessionUpdatedEventWithSeq(ctx, queries, sessionID, arguments.UpdatedAt)
		return err
	})
	if err != nil {
		if eventSeq != 0 {
			reconciled, committed, reconcileErr := adapter.reconcileSessionEvent(eventSeq, row)
			if reconcileErr != nil {
				if restoreModel != nil {
					adapter.detachUnusableLiveSession(sessionID)
				}
				return api.Session{}, storeFailure(errors.Join(err, reconcileErr))
			}
			if committed {
				viewContext, cancelView := adapter.durableTransitionContext()
				defer cancelView()
				view, viewErr := adapter.sessionView(viewContext, reconciled)
				if viewErr != nil {
					return api.Session{}, storeFailure(viewErr)
				}
				return view, nil
			}
		}
		if restoreModel != nil {
			if rollbackErr := restoreModel(); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("restore previous model: %w", rollbackErr))
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			return api.Session{}, fail(http.StatusConflict, errorCodeSessionTerminal, "the session has ended")
		}
		return api.Session{}, storeFailure(err)
	}
	view, err := adapter.sessionView(ctx, row)
	if err != nil {
		return api.Session{}, storeFailure(err)
	}
	return view, nil
}

func (adapter *CopilotAdapter) reconcileSessionEvent(eventSeq int64, expected storedb.Session) (storedb.Session, bool, error) {
	ctx, cancel := adapter.durableTransitionContext()
	defer cancel()
	version, err := adapter.store.GetSessionEventVersion(ctx, storedb.GetSessionEventVersionParams{
		SessionID: expected.ID, EventSeq: eventSeq,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return storedb.Session{}, false, nil
	}
	if err != nil {
		return storedb.Session{}, false, fmt.Errorf("reconcile session event receipt: %w", err)
	}
	if version.ID != expected.ID || version.WorktreeID != expected.WorktreeID ||
		version.DisplayName != expected.DisplayName || version.Model != expected.Model ||
		version.WorkingDirectory != expected.WorkingDirectory || version.Status != expected.Status ||
		!version.CreatedAt.Equal(expected.CreatedAt) || !version.UpdatedAt.Equal(expected.UpdatedAt) {
		return storedb.Session{}, false, errors.New("reconcile session event receipt: persisted version does not match the attempted transition")
	}
	row, err := adapter.store.GetSession(ctx, expected.ID)
	if err != nil {
		return storedb.Session{}, false, fmt.Errorf("reconcile committed session: %w", err)
	}
	return row, true, nil
}

// EndSession marks a session terminal.
func (adapter *CopilotAdapter) endSession(ctx context.Context, sessionID uuid.UUID) (api.Session, error) {
	unlockScheduleControl := adapter.scheduleControls.lock(sessionID)
	defer unlockScheduleControl()
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	durableContext, cancelDurableTransition := adapter.durableTransitionContext()
	defer cancelDurableTransition()
	existing, err := adapter.store.GetSession(durableContext, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Session{}, fail(http.StatusNotFound, errorCodeSessionNotFound, "no session with that id")
	}
	if err != nil {
		return api.Session{}, storeFailure(err)
	}
	if err := rejectStartingSession(existing); err != nil {
		return api.Session{}, err
	}
	if existing.Status == string(api.SessionStatusFailed) {
		adapter.detachUnusableLiveSession(sessionID)
		adapter.reloadSchedules()
		view, viewErr := adapter.durableSessionView(existing)
		if viewErr != nil {
			return api.Session{}, storeFailure(viewErr)
		}
		return view, nil
	}
	if existing.Status == string(api.SessionStatusEnded) {
		closeErr := adapter.closeLiveSession(sessionID)
		adapter.reloadSchedules()
		if closeErr != nil {
			return api.Session{}, fail(http.StatusInternalServerError, errorCodeCLICloseFailed, closeErr.Error())
		}
		view, viewErr := adapter.durableSessionView(existing)
		if viewErr != nil {
			return api.Session{}, storeFailure(viewErr)
		}
		return view, nil
	}
	now := time.Now().UTC()
	var row storedb.Session
	var eventSeq int64
	err = adapter.store.Transact(durableContext, func(queries storedb.Querier) error {
		locked, transitionErr := queries.LockSession(durableContext, sessionID)
		if transitionErr != nil {
			return transitionErr
		}
		if locked.Status == string(api.SessionStatusEnded) || locked.Status == string(api.SessionStatusFailed) {
			row = locked
			return nil
		}
		if err := rejectStartingSession(locked); err != nil {
			return err
		}
		if transitionErr = finalizeSessionActivity(durableContext, queries, sessionID, now); transitionErr != nil {
			return transitionErr
		}
		if _, transitionErr = queries.PauseSessionSchedules(durableContext, storedb.PauseSessionSchedulesParams{
			UpdatedAt: now, SessionID: sessionID,
		}); transitionErr != nil {
			return transitionErr
		}
		row, transitionErr = queries.MarkSessionEnded(durableContext, storedb.MarkSessionEndedParams{
			ID: sessionID, Status: string(api.SessionStatusEnded),
			EndedAt: null.TimeFrom(now), UpdatedAt: now,
		})
		if transitionErr != nil {
			return transitionErr
		}
		eventSeq, transitionErr = insertSessionUpdatedEventWithSeq(durableContext, queries, sessionID, now)
		return transitionErr
	})
	if err != nil {
		var requestFailure failure
		if errors.As(err, &requestFailure) {
			return api.Session{}, requestFailure
		}
		if eventSeq == 0 {
			return api.Session{}, storeFailure(err)
		}
		reconciled, committed, reconcileErr := adapter.reconcileSessionEvent(eventSeq, row)
		if reconcileErr != nil {
			adapter.detachUnusableLiveSession(sessionID)
			adapter.reloadSchedules()
			return api.Session{}, storeFailure(errors.Join(err, reconcileErr))
		}
		if !committed {
			return api.Session{}, storeFailure(err)
		}
		row = reconciled
	}
	var closeErr error
	if row.Status == string(api.SessionStatusFailed) {
		adapter.detachUnusableLiveSession(sessionID)
	} else {
		closeErr = adapter.closeLiveSession(sessionID)
	}
	adapter.reloadSchedules()
	if closeErr != nil {
		return api.Session{}, fail(http.StatusInternalServerError, errorCodeCLICloseFailed, closeErr.Error())
	}
	view, err := adapter.durableSessionView(row)
	if err != nil {
		return api.Session{}, storeFailure(err)
	}
	return view, nil
}

type promptSubmission struct {
	Text                 string
	Mode                 api.PromptMode
	Author               string
	IdempotencyKey       uuid.UUID
	ScheduleOccurrenceID string
}

func (adapter *CopilotAdapter) submitPrompt(ctx context.Context, sessionID uuid.UUID, submission promptSubmission) (storedb.Turn, bool, error) {
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	return adapter.submitPromptLocked(ctx, sessionID, submission)
}

// submitPromptLocked performs one submission while the caller owns the
// session mutation lock. HTTP submissions acquire it in submitPrompt;
// scheduled submissions acquire it before their final durable status check so
// a pause acknowledgement and the external handoff have one ordering.
func (adapter *CopilotAdapter) submitPromptLocked(ctx context.Context, sessionID uuid.UUID, submission promptSubmission) (storedb.Turn, bool, error) {
	if submission.IdempotencyKey != uuid.Nil {
		turn, replayed, err := adapter.replayPromptSubmission(ctx, sessionID, submission)
		if err != nil || replayed {
			return turn, false, err
		}
	}
	session, err := adapter.store.GetSession(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return storedb.Turn{}, false, fail(http.StatusNotFound, errorCodeSessionNotFound, "no session with that id")
	}
	if err != nil {
		return storedb.Turn{}, false, storeFailure(err)
	}
	if err := rejectStartingSession(session); err != nil {
		return storedb.Turn{}, false, err
	}
	if session.Status == string(api.SessionStatusEnded) || session.Status == string(api.SessionStatusFailed) {
		return storedb.Turn{}, false, fail(http.StatusConflict, errorCodeSessionTerminal, "the session has ended")
	}
	handle, live := adapter.sessions.lookup(sessionID)
	if !live || handle.Send == nil {
		return storedb.Turn{}, false, fail(http.StatusConflict, errorCodeSessionNotLive, errNoLiveSession.Error())
	}
	acknowledgeDelivery := func(turnID uuid.UUID) {
		if handle.AcknowledgeDelivery != nil {
			handle.AcknowledgeDelivery(turnID)
		}
	}
	now := time.Now().UTC()
	turn := storedb.Turn{}
	created := true
	terminal := false
	starting := false
	err = adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		locked, lockErr := queries.LockSession(ctx, sessionID)
		if lockErr != nil {
			return lockErr
		}
		if locked.Status == string(api.SessionStatusEnded) || locked.Status == string(api.SessionStatusFailed) {
			terminal = true
			return nil
		}
		if locked.Status == string(api.SessionStatusStarting) {
			starting = true
			return nil
		}
		parameters := storedb.CreateTurnParams{
			ID: uuid.New(), SessionID: sessionID, Status: string(api.TurnStatusQueued),
			PromptText: submission.Text, PromptMode: string(submission.Mode), Author: submission.Author,
			CreatedAt: now, DeliveryStatus: turnDeliveryPending,
		}
		switch {
		case submission.ScheduleOccurrenceID != "":
			_, err = queries.ClaimScheduledTurnOccurrence(ctx, storedb.ClaimScheduledTurnOccurrenceParams{
				ScheduleOccurrenceID: submission.ScheduleOccurrenceID, TurnID: parameters.ID,
			})
			if errors.Is(err, sql.ErrNoRows) {
				turn, err = queries.GetTurnByScheduleOccurrence(ctx, submission.ScheduleOccurrenceID)
				created = false
			} else if err == nil {
				turn, err = queries.CreateTurn(ctx, parameters)
			}
		case submission.IdempotencyKey != uuid.Nil:
			_, err = queries.ClaimPromptSubmission(ctx, storedb.ClaimPromptSubmissionParams{
				SessionID: sessionID, IdempotencyKey: submission.IdempotencyKey, TurnID: parameters.ID,
			})
			if errors.Is(err, sql.ErrNoRows) {
				turn, err = queries.GetTurnByPromptIdempotencyKey(ctx, storedb.GetTurnByPromptIdempotencyKeyParams{
					SessionID: sessionID, IdempotencyKey: submission.IdempotencyKey,
				})
				created = false
				if err == nil && !samePromptSubmission(turn, sessionID, submission) {
					return fail(http.StatusConflict, errorCodeIdempotencyKeyReused, "idempotencyKey already identifies a different prompt")
				}
			} else if err == nil {
				turn, err = queries.CreateTurn(ctx, parameters)
			}
		default:
			turn, err = queries.CreateTurn(ctx, parameters)
		}
		if err != nil || !created {
			return err
		}
		transcriptSeq, err := queries.AllocateTranscriptSeq(ctx, sessionID)
		if err != nil {
			return err
		}
		if _, err := queries.InsertTranscriptItem(ctx, storedb.InsertTranscriptItemParams{
			SessionID: sessionID, Seq: transcriptSeq, TurnID: &turn.ID,
			Kind: string(api.TranscriptItemKindUserMessage), OccurredAt: now,
			Author: null.NewString(submission.Author, submission.Author != ""), Body: submission.Text,
		}); err != nil {
			return err
		}
		if _, err := queries.TouchSessionLastTurn(ctx, storedb.TouchSessionLastTurnParams{
			ID: sessionID, LastTurnAt: null.TimeFrom(now), UpdatedAt: now,
		}); err != nil {
			return err
		}
		if _, err := queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(api.SessionStatusRunning), UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := insertEvent(ctx, queries, storedb.InsertSessionEventParams{
			SessionID: sessionID, Kind: string(api.SessionEventKindTranscriptAppended), OccurredAt: now,
			TranscriptSeq: null.IntFrom(transcriptSeq),
		}); err != nil {
			return err
		}
		return insertSessionUpdatedEvent(ctx, queries, sessionID, now)
	})
	if err != nil {
		var requestFailure failure
		if errors.As(err, &requestFailure) {
			return storedb.Turn{}, false, requestFailure
		}
		if submission.ScheduleOccurrenceID == "" {
			return storedb.Turn{}, false, storeFailure(err)
		}
		transactionErr := err
		reconcileContext, cancelReconciliation := adapter.durableTransitionContext()
		turn, err = adapter.store.GetTurnByScheduleOccurrence(reconcileContext, submission.ScheduleOccurrenceID)
		cancelReconciliation()
		if errors.Is(err, sql.ErrNoRows) {
			return storedb.Turn{}, false, storeFailure(transactionErr)
		}
		if err != nil {
			return storedb.Turn{}, false, storeFailure(errors.Join(transactionErr, err))
		}
		if !samePromptSubmission(turn, sessionID, submission) {
			return storedb.Turn{}, false, storeFailure(errors.Join(transactionErr,
				errors.New("reconcile scheduled prompt transaction: persisted turn does not match the attempted submission")))
		}
		created = false
	}
	if terminal {
		return storedb.Turn{}, false, fail(http.StatusConflict, errorCodeSessionTerminal, "the session has ended")
	}
	if starting {
		return storedb.Turn{}, false, fail(http.StatusConflict, errorCodeSessionNotReady, "the session is still starting")
	}
	if !created {
		switch turn.DeliveryStatus {
		case turnDeliveryAccepted:
			acknowledgeDelivery(turn.ID)
			return turn, false, nil
		case turnDeliveryPending:
			if turn.Status != string(api.TurnStatusQueued) {
				return turn, false, fail(http.StatusConflict, errorCodeCLISendFailed, "the scheduled prompt reached a terminal state before delivery")
			}
		case turnDeliveryUnknown:
			if submission.IdempotencyKey != uuid.Nil {
				return turn, false, nil
			}
			if turn.Status != string(api.TurnStatusQueued) {
				return turn, false, fail(http.StatusConflict, errorCodeCLISendFailed, "the scheduled prompt reached a terminal state before delivery")
			}
		default:
			return turn, false, fail(http.StatusBadGateway, errorCodeCLISendFailed, "the prompt previously failed delivery")
		}
	}
	if turn.DeliveryStatus == turnDeliveryPending {
		unknownContext, cancelUnknown := adapter.durableTransitionContext()
		unknown, unknownErr := adapter.store.MarkTurnDeliveryUnknown(unknownContext, turn.ID)
		cancelUnknown()
		if unknownErr != nil {
			reconcileContext, cancelReconciliation := adapter.durableTransitionContext()
			persisted, reconcileErr := adapter.store.GetTurn(reconcileContext, turn.ID)
			cancelReconciliation()
			if reconcileErr == nil && persisted.DeliveryStatus == turnDeliveryAccepted {
				acknowledgeDelivery(persisted.ID)
				return persisted, true, nil
			}
			if reconcileErr != nil || persisted.DeliveryStatus != turnDeliveryUnknown {
				return turn, true, storeFailure(errors.Join(unknownErr, reconcileErr))
			}
			unknown = persisted
		}
		turn = unknown
	}
	sendContext, cancelSend := adapter.durableTransitionContext()
	delivery, sendErr := handle.Send(sendContext, BridgePrompt{
		TurnID: turn.ID, Text: submission.Text, Mode: string(submission.Mode), Author: submission.Author,
	})
	cancelSend()
	if delivery == BridgePromptDeliveryRejected {
		if sendErr == nil {
			sendErr = errors.New("the CLI rejected the prompt before delivery")
		}
		failedAt := time.Now().UTC()
		compensationContext, cancelCompensation := adapter.durableTransitionContext()
		failed, compensationErr := adapter.failTurnDelivery(compensationContext, sessionID, turn.ID, failedAt)
		cancelCompensation()
		if compensationErr == nil {
			turn = failed
		}
		if compensationErr != nil {
			adapter.logger.Error("persist rejected CLI send", "sessionId", sessionID, "turnId", turn.ID, "error", compensationErr)
		}
		return turn, true, fail(http.StatusBadGateway, errorCodeCLISendFailed, sendErr.Error())
	}
	if delivery != BridgePromptDeliveryAccepted || sendErr != nil {
		adapter.logger.Warn("CLI prompt delivery remains unresolved", "sessionId", sessionID, "turnId", turn.ID, "error", sendErr)
		return turn, true, nil
	}
	ackContext, cancelAcknowledgement := adapter.durableTransitionContext()
	accepted, err := adapter.store.MarkTurnDeliveryAccepted(ackContext, turn.ID)
	cancelAcknowledgement()
	if err != nil {
		ackErr := err
		reconcileContext, cancelReconciliation := adapter.durableTransitionContext()
		accepted, err = adapter.store.GetTurn(reconcileContext, turn.ID)
		cancelReconciliation()
		if err == nil && accepted.DeliveryStatus != turnDeliveryAccepted {
			err = ackErr
		} else if err != nil {
			err = errors.Join(ackErr, err)
		}
	}
	if err != nil {
		return turn, true, storeFailure(err)
	}
	turn = accepted
	acknowledgeDelivery(turn.ID)
	return turn, true, nil
}

// replayPromptSubmission answers a completed HTTP retry from its durable
// receipt before checking mutable session lifecycle or process ownership. A
// pending turn still needs the live bridge and therefore follows the ordinary
// submission path; unknown must never cross that boundary a second time.
func (adapter *CopilotAdapter) replayPromptSubmission(
	ctx context.Context,
	sessionID uuid.UUID,
	submission promptSubmission,
) (storedb.Turn, bool, error) {
	turn, err := adapter.store.GetTurnByPromptIdempotencyKey(ctx, storedb.GetTurnByPromptIdempotencyKeyParams{
		SessionID: sessionID, IdempotencyKey: submission.IdempotencyKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return storedb.Turn{}, false, nil
	}
	if err != nil {
		return storedb.Turn{}, true, storeFailure(err)
	}
	if !samePromptSubmission(turn, sessionID, submission) {
		return turn, true, fail(http.StatusConflict, errorCodeIdempotencyKeyReused, "idempotencyKey already identifies a different prompt")
	}
	switch turn.DeliveryStatus {
	case turnDeliveryAccepted:
		handle, live := adapter.sessions.lookup(sessionID)
		if live && handle.AcknowledgeDelivery != nil {
			handle.AcknowledgeDelivery(turn.ID)
		}
		return turn, true, nil
	case turnDeliveryUnknown:
		return turn, true, nil
	case turnDeliveryPending:
		return turn, false, nil
	default:
		return turn, true, fail(http.StatusBadGateway, errorCodeCLISendFailed, "the prompt previously failed delivery")
	}
}

func samePromptSubmission(turn storedb.Turn, sessionID uuid.UUID, submission promptSubmission) bool {
	return turn.SessionID == sessionID &&
		turn.PromptText == submission.Text &&
		turn.PromptMode == string(submission.Mode) &&
		turn.Author == submission.Author
}

// AbortTurn stops exactly the durable running turn named by the client. The
// receipt is claimed before the SDK call so a lost response can never retarget
// a successor.
func (adapter *CopilotAdapter) abortTurn(ctx context.Context, sessionID uuid.UUID, targetTurnID uuid.UUID, idempotencyKey uuid.UUID) (api.Turn, error) {
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	replay, found, err := adapter.replayAbortSubmission(ctx, sessionID, idempotencyKey, targetTurnID)
	if err != nil {
		return api.Turn{}, err
	}
	if found {
		durableContext, cancelConvergence := adapter.durableTransitionContext()
		replay, err = adapter.persistAbortedTurn(durableContext, sessionID, targetTurnID, time.Now().UTC())
		cancelConvergence()
		if err != nil {
			return api.Turn{}, storeFailure(err)
		}
		if err := adapter.abandonTurnResolutionCallbacks(sessionID, targetTurnID); err != nil {
			return api.Turn{}, storeFailure(err)
		}
		return views.Turn(replay), nil
	}
	session, err := adapter.store.GetSession(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Turn{}, fail(http.StatusNotFound, errorCodeSessionNotFound, "no session with that id")
	}
	if err != nil {
		return api.Turn{}, storeFailure(err)
	}
	if err := rejectStartingSession(session); err != nil {
		return api.Turn{}, err
	}
	target, err := adapter.store.GetTurn(ctx, targetTurnID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && target.SessionID != sessionID) {
		return api.Turn{}, fail(http.StatusConflict, errorCodeAbortTargetChanged, "turnId is not a turn in this session")
	}
	if err != nil {
		return api.Turn{}, storeFailure(err)
	}
	if target.Status != string(api.TurnStatusRunning) {
		return api.Turn{}, fail(http.StatusConflict, errorCodeAbortTargetChanged, "turnId is no longer running")
	}
	handle, live := adapter.sessions.lookup(sessionID)
	if !live || handle.Abort == nil {
		return api.Turn{}, fail(http.StatusConflict, errorCodeSessionNotLive, errNoLiveSession.Error())
	}
	if handle.ActiveTurn == nil {
		return api.Turn{}, fail(http.StatusConflict, errorCodeNoTurnInFlight, "the session has no active turn")
	}
	activeTurnID, active := handle.ActiveTurn()
	if !active {
		return api.Turn{}, fail(http.StatusConflict, errorCodeNoTurnInFlight, "the session has no active turn")
	}
	if activeTurnID != targetTurnID {
		return api.Turn{}, fail(http.StatusConflict, errorCodeAbortTargetChanged, "the active turn changed before the abort")
	}
	if err := adapter.claimAbortSubmission(ctx, sessionID, idempotencyKey, targetTurnID); err != nil {
		return api.Turn{}, err
	}
	turnID, err := handle.Abort(ctx, targetTurnID)
	if err != nil {
		if errors.Is(err, ErrNoActiveTurn) || errors.Is(err, ErrAbortPending) {
			return api.Turn{}, fail(http.StatusConflict, errorCodeNoTurnInFlight, err.Error())
		}
		if errors.Is(err, ErrAbortTargetMismatch) {
			return api.Turn{}, fail(http.StatusConflict, errorCodeAbortTargetChanged, err.Error())
		}
		// The CLI may still be running the target, so the row stays
		// nonterminal rather than reporting a stop that did not happen.
		return api.Turn{}, fail(http.StatusInternalServerError, errorCodeCLIAbortFailed, err.Error())
	}
	if turnID != targetTurnID {
		return api.Turn{}, fail(http.StatusInternalServerError, errorCodeCLIAbortFailed, "the CLI aborted a turn other than the requested target")
	}
	durableContext, cancelDurableTransition := adapter.durableTransitionContext()
	defer cancelDurableTransition()
	now := time.Now().UTC()
	aborted, err := adapter.persistAbortedTurn(durableContext, sessionID, targetTurnID, now)
	if err != nil {
		firstErr := err
		retryContext, cancelRetry := adapter.durableTransitionContext()
		aborted, err = adapter.persistAbortedTurn(retryContext, sessionID, targetTurnID, now)
		cancelRetry()
		if err != nil {
			if errors.Is(err, ErrAbortTargetMismatch) {
				return api.Turn{}, fail(http.StatusConflict, errorCodeAbortTargetChanged, "the target turn completed before the abort was recorded")
			}
			return api.Turn{}, storeFailure(errors.Join(firstErr, err))
		}
	}
	if err := adapter.abandonTurnResolutionCallbacks(sessionID, targetTurnID); err != nil {
		return api.Turn{}, storeFailure(err)
	}
	return views.Turn(aborted), nil
}

// persistAbortedTurn converges both the turn and its scoped requests. It is
// safe after a projector wins, a transaction rollback, or a lost commit
// acknowledgement: only the transition that changes a row emits its snapshot.
func (adapter *CopilotAdapter) persistAbortedTurn(
	ctx context.Context,
	sessionID uuid.UUID,
	targetTurnID uuid.UUID,
	occurredAt time.Time,
) (storedb.Turn, error) {
	var aborted storedb.Turn
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		if _, lockErr := queries.LockSession(ctx, sessionID); lockErr != nil {
			return lockErr
		}
		var err error
		aborted, _, err = finalizeAbortedTurn(ctx, queries, sessionID, &targetTurnID, occurredAt)
		return err
	})
	return aborted, err
}

// finalizeAbortedTurn is the one transactional abort transition shared by
// synchronous HTTP acknowledgement and delayed bridge projection. Request
// abandonment and its versioned events commit with the target turn; callbacks
// are intentionally invoked only by the caller after this transaction commits.
func finalizeAbortedTurn(
	ctx context.Context,
	queries storedb.Querier,
	sessionID uuid.UUID,
	targetTurnID *uuid.UUID,
	occurredAt time.Time,
) (storedb.Turn, bool, error) {
	if targetTurnID == nil {
		return storedb.Turn{}, false, fmt.Errorf("%w: abort event has no correlated turn id", errInvalidBridgeEvent)
	}
	target, err := queries.GetTurn(ctx, *targetTurnID)
	if err != nil {
		return storedb.Turn{}, false, err
	}
	if target.SessionID != sessionID {
		return storedb.Turn{}, false, ErrAbortTargetMismatch
	}
	aborted, err := queries.FinalizeTurn(ctx, storedb.FinalizeTurnParams{
		ID: *targetTurnID, Status: string(api.TurnStatusAborted), CompletedAt: null.TimeFrom(occurredAt),
	})
	newlyAborted := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		aborted, err = queries.GetTurn(ctx, *targetTurnID)
		if err != nil {
			return storedb.Turn{}, false, err
		}
		if aborted.SessionID != sessionID || aborted.Status != string(api.TurnStatusAborted) {
			return storedb.Turn{}, false, ErrAbortTargetMismatch
		}
	}
	if err != nil {
		return storedb.Turn{}, false, err
	}
	if _, err = abandonTurnRequests(ctx, queries, sessionID, *targetTurnID, occurredAt); err != nil {
		return storedb.Turn{}, false, err
	}
	if !newlyAborted {
		return aborted, false, nil
	}
	activeTurns, err := queries.CountActiveSessionTurns(ctx, sessionID)
	if err != nil {
		return storedb.Turn{}, false, err
	}
	sessionStatus := api.SessionStatusIdle
	if activeTurns > 0 {
		sessionStatus = api.SessionStatusRunning
	}
	if _, err = queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
		ID: sessionID, Status: string(sessionStatus), UpdatedAt: occurredAt,
	}); err != nil {
		return storedb.Turn{}, false, err
	}
	if err = insertTurnEvent(ctx, queries, sessionID, *targetTurnID, api.SessionEventKindTurnCompleted, occurredAt); err != nil {
		return storedb.Turn{}, false, err
	}
	if err = insertSessionUpdatedEvent(ctx, queries, sessionID, occurredAt); err != nil {
		return storedb.Turn{}, false, err
	}
	return aborted, true, nil
}

func (adapter *CopilotAdapter) abandonTurnResolutionCallbacks(sessionID uuid.UUID, turnID uuid.UUID) error {
	handle, live := adapter.sessions.lookup(sessionID)
	if !live || handle.AbandonResolution == nil {
		return nil
	}
	ctx, cancel := adapter.durableTransitionContext()
	defer cancel()
	requests, err := adapter.store.ListAbandonedTurnRequests(ctx, storedb.ListAbandonedTurnRequestsParams{
		SessionID: sessionID, TurnID: &turnID,
	})
	if err != nil {
		return fmt.Errorf("list aborted turn requests: %w", err)
	}
	for _, request := range requests {
		handle.AbandonResolution(request.ID)
	}
	return nil
}

func (adapter *CopilotAdapter) replayAbortSubmission(
	ctx context.Context,
	sessionID uuid.UUID,
	idempotencyKey uuid.UUID,
	targetTurnID uuid.UUID,
) (storedb.Turn, bool, error) {
	turn, err := adapter.store.GetTurnByAbortIdempotencyKey(ctx, storedb.GetTurnByAbortIdempotencyKeyParams{
		SessionID: sessionID, IdempotencyKey: idempotencyKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return storedb.Turn{}, false, nil
	}
	if err != nil {
		return storedb.Turn{}, true, storeFailure(err)
	}
	if turn.ID != targetTurnID {
		return storedb.Turn{}, true, fail(http.StatusConflict, errorCodeIdempotencyKeyReused, "idempotencyKey already identifies a different abort target")
	}
	if turn.Status == string(api.TurnStatusAborted) {
		return turn, true, nil
	}
	if turn.Status != string(api.TurnStatusRunning) {
		return storedb.Turn{}, true, fail(http.StatusConflict, errorCodeAbortTargetChanged, "the exact abort target is no longer running")
	}
	return turn, false, nil
}

func (adapter *CopilotAdapter) claimAbortSubmission(
	ctx context.Context,
	sessionID uuid.UUID,
	idempotencyKey uuid.UUID,
	targetTurnID uuid.UUID,
) error {
	parameters := storedb.ClaimTurnAbortSubmissionParams{
		SessionID: sessionID, IdempotencyKey: idempotencyKey, TurnID: targetTurnID,
	}
	claimedTurnID, err := adapter.store.ClaimTurnAbortSubmission(ctx, parameters)
	if err == nil {
		if claimedTurnID != targetTurnID {
			return fail(http.StatusConflict, errorCodeIdempotencyKeyReused, "idempotencyKey already identifies a different abort target")
		}
		return nil
	}
	claimErr := err
	lookupContext, cancelLookup := adapter.durableTransitionContext()
	defer cancelLookup()
	turn, lookupErr := adapter.store.GetTurnByAbortIdempotencyKey(lookupContext, storedb.GetTurnByAbortIdempotencyKeyParams{
		SessionID: sessionID, IdempotencyKey: idempotencyKey,
	})
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return storeFailure(claimErr)
	}
	if lookupErr != nil {
		return storeFailure(errors.Join(claimErr, lookupErr))
	}
	if turn.ID != targetTurnID {
		return fail(http.StatusConflict, errorCodeIdempotencyKeyReused, "idempotencyKey already identifies a different abort target")
	}
	return nil
}

// ListTranscript pages a session's persisted transcript.
func (adapter *CopilotAdapter) transcript(ctx context.Context, sessionID uuid.UUID, parameters api.ListTranscriptParams) (api.TranscriptPage, error) {
	if err := adapter.requireSession(ctx, sessionID); err != nil {
		return api.TranscriptPage{}, err
	}
	limit := adapter.pageLimit(parameters.Limit)
	afterSeq := int64(0)
	if parameters.AfterSeq != nil {
		afterSeq = *parameters.AfterSeq
	}
	rows, err := adapter.store.ListTranscriptItemsAfterSeq(ctx, storedb.ListTranscriptItemsAfterSeqParams{
		SessionID: sessionID,
		AfterSeq:  afterSeq,
		RowLimit:  limit,
	})
	if err != nil {
		return api.TranscriptPage{}, storeFailure(err)
	}
	page := api.TranscriptPage{Data: make([]api.TranscriptItem, 0, len(rows))}
	for _, row := range rows {
		page.Data = append(page.Data, views.TranscriptItem(row))
	}
	if int32(len(rows)) == limit && len(rows) > 0 {
		next := rows[len(rows)-1].Seq
		page.NextAfterSeq = &next
	}
	return page, nil
}

// ListSessionRequests lists the requests awaiting a decision.
func (adapter *CopilotAdapter) sessionRequests(ctx context.Context, sessionID uuid.UUID) (api.SessionRequestList, error) {
	if err := adapter.requireSession(ctx, sessionID); err != nil {
		return api.SessionRequestList{}, err
	}
	rows, err := adapter.store.ListPendingPermissionSessionRequests(ctx, sessionID)
	if err != nil {
		return api.SessionRequestList{}, storeFailure(err)
	}
	page := api.SessionRequestList{Data: make([]api.SessionRequest, 0, len(rows))}
	for _, row := range rows {
		page.Data = append(page.Data, views.SessionRequest(row))
	}
	return page, nil
}

// ResolveSessionRequest resolves a pending permission and routes the decision to
// the CLI.
func (adapter *CopilotAdapter) resolveSessionRequest(ctx context.Context, sessionID uuid.UUID, requestID uuid.UUID, decision api.ResolveDecision) (api.SessionRequest, error) {
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	pending, err := adapter.store.GetSessionRequest(ctx, requestID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && pending.SessionID != sessionID) {
		return api.SessionRequest{}, fail(http.StatusNotFound, errorCodeRequestNotFound, "no pending request with that id in this session")
	}
	if err != nil {
		return api.SessionRequest{}, storeFailure(err)
	}
	if err := validateRequestDecision(api.SessionRequestKind(pending.Kind), decision); err != nil {
		return api.SessionRequest{}, err
	}
	session, err := adapter.store.GetSession(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return api.SessionRequest{}, fail(http.StatusNotFound, errorCodeSessionNotFound, "no session with that id")
	}
	if err != nil {
		return api.SessionRequest{}, storeFailure(err)
	}
	if err := rejectStartingSession(session); err != nil {
		return api.SessionRequest{}, err
	}
	status := map[api.ResolveDecision]string{
		api.Approve: string(api.Approved), api.Deny: string(api.Denied),
	}[decision]
	decisionText := string(decision)
	if pending.Status != string(api.Pending) {
		if sameRequestResolution(pending, decisionText, status) {
			return views.SessionRequest(pending), nil
		}
		return api.SessionRequest{}, fail(http.StatusConflict, errorCodeRequestAlreadyResolved, "the request has already been resolved differently")
	}
	// A decision nothing can deliver is not a resolution: the exact SDK request
	// is still pending and the row would tell the UI it was answered.
	handle, live := adapter.sessions.lookup(sessionID)
	if !live || handle.Resolve == nil {
		return api.SessionRequest{}, fail(http.StatusConflict, errorCodeSessionNotLive, errNoLiveSession.Error())
	}
	prepared, err := adapter.store.PrepareSessionRequestResolution(ctx, storedb.PrepareSessionRequestResolutionParams{
		ID: requestID, Decision: decisionText, Answer: "",
	})
	if errors.Is(err, sql.ErrNoRows) {
		current, lookupErr := adapter.store.GetSessionRequest(ctx, requestID)
		if lookupErr == nil && sameRequestResolution(current, decisionText, status) {
			return views.SessionRequest(current), nil
		}
		return api.SessionRequest{}, fail(http.StatusConflict, errorCodeRequestAlreadyResolved, "the request is already delivering a different decision")
	}
	if err != nil {
		return api.SessionRequest{}, storeFailure(err)
	}
	resolution := BridgeResolution{RequestID: requestID, Decision: prepared.Decision}
	if err := handle.Resolve(ctx, resolution); err != nil {
		current, lookupErr := adapter.store.GetSessionRequest(ctx, requestID)
		if lookupErr == nil && sameRequestResolution(current, decisionText, status) {
			return views.SessionRequest(current), nil
		}
		return api.SessionRequest{}, fail(http.StatusInternalServerError, errorCodeCLIResolveFailed, err.Error())
	}
	durableContext, cancelDurableTransition := adapter.durableTransitionContext()
	defer cancelDurableTransition()
	now := time.Now().UTC()
	var resolved storedb.PendingRequest
	err = adapter.store.Transact(durableContext, func(queries storedb.Querier) error {
		var err error
		resolved, err = queries.CompleteSessionRequestResolution(durableContext, storedb.CompleteSessionRequestResolutionParams{
			ID: requestID, Status: status, Decision: resolution.Decision, Answer: "",
			ResolvedAt: null.TimeFrom(now),
		})
		if err != nil {
			return err
		}
		if err := insertDeniedRequestNotice(durableContext, queries, resolved, now); err != nil {
			return err
		}
		return insertRequestEvent(durableContext, queries, sessionID, requestID, api.SessionEventKindRequestResolved, now)
	})
	if err != nil {
		current, lookupErr := adapter.store.GetSessionRequest(durableContext, requestID)
		if lookupErr == nil && sameRequestResolution(current, decisionText, status) {
			if handle.AcknowledgeResolution != nil {
				handle.AcknowledgeResolution(requestID)
			}
			return views.SessionRequest(current), nil
		}
		return api.SessionRequest{}, storeFailure(err)
	}
	if handle.AcknowledgeResolution != nil {
		handle.AcknowledgeResolution(requestID)
	}
	return views.SessionRequest(resolved), nil
}

func decodeResolveSessionRequest(request api.ResolveSessionRequestBody) (api.ResolveDecision, error) {
	if !request.Decision.Valid() {
		return "", fail(http.StatusBadRequest, errorCodeInvalidRequest, "decision is not supported")
	}
	return request.Decision, nil
}

func validateRequestDecision(kind api.SessionRequestKind, decision api.ResolveDecision) error {
	if kind != api.Permission {
		return fail(http.StatusBadRequest, errorCodeInvalidRequest, "only permission requests can be resolved")
	}
	if decision != api.Approve && decision != api.Deny {
		return fail(http.StatusBadRequest, errorCodeInvalidRequest, "permission requests only accept approve or deny")
	}
	return nil
}

func sameRequestResolution(row storedb.PendingRequest, decision string, status string) bool {
	return row.Status == status && row.Decision == decision && row.DeliveryStatus == "delivered"
}

// requireSession checks existence within the service before opening a stream.
func (adapter *CopilotAdapter) requireSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := adapter.store.GetSession(ctx, sessionID)
	if err != nil {
		return lookupFailure(err, errorCodeSessionNotFound, "no session with that id")
	}
	return nil
}
