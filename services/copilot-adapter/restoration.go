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

const missingBridgeSessionEventID = "copilot-adapter.restore.missing-bridge-session.v1"

// RestoreSessions reconnects every persisted nonterminal SDK session before
// HTTP or scheduled jobs can address it. Each stored working directory is
// revalidated through the configured worktree manager first, so restart does
// not turn legacy database paths into filesystem authority.
func (adapter *CopilotAdapter) RestoreSessions(ctx context.Context) error {
	if err := adapter.restoreIncompleteCreations(ctx); err != nil {
		return err
	}
	rows, err := adapter.store.ListResumableSessions(ctx)
	if err != nil {
		return fmt.Errorf("copilot-adapter: list sessions to restore: %w", err)
	}
	for _, row := range rows {
		if _, live := adapter.sessions.lookup(row.ID); live {
			continue
		}
		unlock := adapter.mutations.lock(row.ID)
		worktree, err := adapter.store.GetWorktree(ctx, row.WorktreeID)
		if err != nil {
			unlock()
			return fmt.Errorf("copilot-adapter: restore session %s worktree: %w", row.ID, err)
		}
		persistedWorktree := worktree
		worktree, err = adapter.validatedWorktree(ctx, persistedWorktree)
		if err != nil {
			if !errors.Is(err, ErrInvalidWorktree) {
				unlock()
				return fmt.Errorf("copilot-adapter: validate session %s worktree: %w", row.ID, err)
			}
			adapter.logger.Warn("quarantine session with invalid worktree", "sessionId", row.ID, "worktreeId", row.WorktreeID, "error", err)
			// quarantineWorktree acquires every affected session lock. Release
			// this row's non-reentrant lock before handing ownership over.
			unlock()
			quarantined, quarantineErr := adapter.quarantineWorktree(ctx, persistedWorktree)
			if quarantineErr != nil {
				return fmt.Errorf("copilot-adapter: quarantine session %s: %w", row.ID, quarantineErr)
			}
			if !quarantined {
				return fmt.Errorf("copilot-adapter: worktree %s recovered while restoring session %s; retry restoration", row.WorktreeID, row.ID)
			}
			continue
		}
		if row.Status == string(api.SessionStatusStarting) {
			receipt, receiptErr := adapter.store.GetSessionCreationBySessionID(ctx, row.ID)
			if receiptErr != nil {
				unlock()
				return fmt.Errorf("copilot-adapter: restore starting session %s receipt: %w", row.ID, receiptErr)
			}
			prepared, prepareErr := adapter.prepareSessionWorktree(ctx, sessionCreationSubmission(receipt), row.ID)
			if prepareErr != nil {
				unlock()
				return fmt.Errorf("copilot-adapter: restore starting session %s identity: %w", row.ID, prepareErr)
			}
			if validateErr := adapter.validatePersistedCreationWorktree(ctx, row, prepared); validateErr != nil {
				unlock()
				return fmt.Errorf("copilot-adapter: restore starting session %s worktree: %w", row.ID, validateErr)
			}
			_, activateErr := adapter.activateStartingSession(row, prepared, receipt)
			unlock()
			if activateErr != nil {
				return fmt.Errorf("copilot-adapter: restore starting session %s: %w", row.ID, activateErr)
			}
			continue
		}
		now := time.Now().UTC()
		if err := adapter.reconcileInterruptedSession(ctx, row.ID, now); err != nil {
			unlock()
			return fmt.Errorf("copilot-adapter: reconcile interrupted session %s: %w", row.ID, err)
		}
		handle, err := adapter.bridge.ResumeSession(ctx, BridgeSessionSpec{
			SessionID: row.ID, Model: row.Model, WorkingDirectory: worktree.Path,
			AgentID:            row.AgentID,
			SystemInstructions: row.SystemInstructions,
		})
		if err != nil {
			if errors.Is(err, ErrBridgeSessionMissing) {
				adapter.logger.Warn("quarantine session missing from Copilot SDK", "sessionId", row.ID, "error", err)
				// projectEvent reacquires the session lock. Transfer ownership
				// before projecting the terminal recovery event.
				unlock()
				if quarantineErr := adapter.quarantineMissingBridgeSession(ctx, row.ID, now); quarantineErr != nil {
					return fmt.Errorf("copilot-adapter: quarantine missing bridge session %s: %w", row.ID, quarantineErr)
				}
				continue
			}
			unlock()
			return fmt.Errorf("copilot-adapter: restore session %s: %w", row.ID, err)
		}
		adapter.attach(row.ID, handle)
		unlock()
	}
	return nil
}

// reconcileInterruptedSession closes the process-restart gap before the SDK
// session is resumed. Copilot SDK v1.0.11 cannot rehydrate permission-blocked
// work and reuses iteration identifiers for later prompts. Every turn that may
// have crossed the old process boundary is therefore aborted, every definitely
// pre-boundary delivery is failed, and every unresolved request is abandoned
// in one transaction. The session itself remains usable.
func (adapter *CopilotAdapter) reconcileInterruptedSession(ctx context.Context, sessionID uuid.UUID, occurredAt time.Time) error {
	return adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		if _, err := queries.LockSession(ctx, sessionID); err != nil {
			return err
		}
		turns, err := queries.ListActiveSessionTurns(ctx, sessionID)
		if err != nil {
			return err
		}
		transitioned := false
		for _, turn := range turns {
			switch turn.DeliveryStatus {
			case turnDeliveryPending:
				if _, err = queries.MarkTurnDeliveryFailed(ctx, storedb.MarkTurnDeliveryFailedParams{
					ID: turn.ID, CompletedAt: null.TimeFrom(occurredAt),
				}); err != nil {
					return err
				}
			case turnDeliveryAccepted, turnDeliveryUnknown:
				if _, err = queries.FinalizeTurn(ctx, storedb.FinalizeTurnParams{
					ID: turn.ID, Status: string(api.TurnStatusAborted), CompletedAt: null.TimeFrom(occurredAt),
				}); err != nil {
					return err
				}
			default:
				return fmt.Errorf("active turn %s has invalid delivery status %q", turn.ID, turn.DeliveryStatus)
			}
			if err = insertTurnEvent(ctx, queries, sessionID, turn.ID, api.SessionEventKindTurnCompleted, occurredAt); err != nil {
				return err
			}
			transitioned = true
		}
		abandonedRequests, err := abandonSessionRequests(ctx, queries, sessionID, occurredAt)
		if err != nil {
			return err
		}
		subagents, err := queries.FinalizeSessionSubagents(ctx, storedb.FinalizeSessionSubagentsParams{
			CompletedAt: null.TimeFrom(occurredAt), UpdatedAt: occurredAt, SessionID: sessionID,
		})
		if err != nil {
			return err
		}
		for _, subagent := range subagents {
			if err = insertSubagentEvent(ctx, queries, sessionID, subagent.ID, occurredAt); err != nil {
				return err
			}
		}
		if !transitioned && abandonedRequests == 0 && len(subagents) == 0 {
			return nil
		}
		if _, err = queries.UpdateSessionStatus(ctx, storedb.UpdateSessionStatusParams{
			ID: sessionID, Status: string(api.SessionStatusIdle), UpdatedAt: occurredAt,
		}); err != nil {
			return err
		}
		return insertSessionUpdatedEvent(ctx, queries, sessionID, occurredAt)
	})
}

// restoreIncompleteCreations closes the only pre-session crash window. A
// receipt may exist before git or the starting-row transaction; startup
// replays that exact validated identity. Stale receipts whose configured
// repository or worktree no longer exists remain retryable and do not prevent
// unrelated sessions from restoring.
func (adapter *CopilotAdapter) restoreIncompleteCreations(ctx context.Context) error {
	receipts, err := adapter.store.ListIncompleteSessionCreations(ctx)
	if err != nil {
		return fmt.Errorf("copilot-adapter: list incomplete session creations: %w", err)
	}
	for _, receipt := range receipts {
		if _, lookupErr := adapter.store.GetSession(ctx, receipt.SessionID); lookupErr == nil {
			continue
		} else if !errors.Is(lookupErr, sql.ErrNoRows) {
			return fmt.Errorf("copilot-adapter: inspect incomplete session %s: %w", receipt.SessionID, lookupErr)
		}
		unlockCreation := adapter.creations.lock(receipt.IdempotencyKey)
		_, recoverErr := adapter.createSessionUnderWorktreeFence(ctx, sessionCreationSubmission(receipt), receipt)
		unlockCreation()
		if recoverErr == nil {
			continue
		}
		var requestFailure failure
		if errors.As(recoverErr, &requestFailure) && requestFailure.status < http.StatusInternalServerError {
			adapter.logger.Warn("defer invalid incomplete session creation",
				"sessionId", receipt.SessionID, "idempotencyKey", receipt.IdempotencyKey, "error", recoverErr)
			continue
		}
		return fmt.Errorf("copilot-adapter: restore incomplete session %s: %w", receipt.SessionID, recoverErr)
	}
	return nil
}

func sessionCreationSubmission(receipt storedb.SessionCreation) createSessionSubmission {
	return createSessionSubmission{
		IdempotencyKey: receipt.IdempotencyKey, Model: receipt.Model, RepositoryID: receipt.RepositoryID,
		AgentID:      receipt.AgentID.Ptr(),
		WorktreeMode: api.WorktreeMode(receipt.WorktreeMode), WorktreeID: receipt.WorktreeID,
		BaseRef: receipt.BaseRef.Ptr(), DisplayName: receipt.DisplayName.Ptr(),
		SystemInstructions: receipt.SystemInstructions.Ptr(),
	}
}

func (adapter *CopilotAdapter) quarantineMissingBridgeSession(ctx context.Context, sessionID uuid.UUID, occurredAt time.Time) error {
	event := BridgeEvent{
		ID: missingBridgeSessionEventID, Kind: BridgeEventFailed, OccurredAt: occurredAt,
	}
	if err := adapter.projectEvent(ctx, sessionID, event); err != nil {
		if retryErr := adapter.projectEvent(ctx, sessionID, event); retryErr != nil {
			return errors.Join(err, retryErr)
		}
	}
	return nil
}
