package copilotadapter

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/google/uuid"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const (
	errorCodeTerminalNotFound = "terminal_not_found"
	errorCodeTerminalState    = "terminal_state"
)

// listTerminals lists process-owned terminals for a persisted worktree.
func (adapter *CopilotAdapter) listTerminals(ctx context.Context, worktreeID uuid.UUID) ([]api.Terminal, error) {
	if _, err := adapter.store.GetWorktree(ctx, worktreeID); err != nil {
		return nil, worktreeLookupError(err)
	}
	rows := adapter.terminals.List(worktreeID)
	data := make([]api.Terminal, 0, len(rows))
	for _, row := range rows {
		data = append(data, terminalView(row))
	}
	return data, nil
}

func (adapter *CopilotAdapter) createTerminal(ctx context.Context, worktreeID uuid.UUID, body api.CreateTerminalJSONRequestBody) (api.Terminal, error) {
	worktree, err := adapter.store.GetWorktree(ctx, worktreeID)
	if err != nil {
		return api.Terminal{}, worktreeLookupError(err)
	}
	worktree, unlockWorktree, err := adapter.lockValidatedWorktree(ctx, worktree)
	if err != nil {
		if errors.Is(err, ErrInvalidWorktree) {
			return api.Terminal{}, fail(http.StatusNotFound, errorCodeWorktreeNotFound, "the worktree path is missing")
		}
		return api.Terminal{}, worktreeValidationFailure(err)
	}
	defer unlockWorktree()
	terminal, err := adapter.terminals.Create(ctx, TerminalSpec{WorktreeID: worktree.ID, Directory: worktree.Path, Rows: body.Rows, Columns: body.Columns})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if quarantineErr := adapter.quarantineWorktreeSessionsLocked(ctx, worktree.ID); quarantineErr != nil {
				return api.Terminal{}, storeFailure(quarantineErr)
			}
			return api.Terminal{}, fail(http.StatusNotFound, errorCodeWorktreeNotFound, "the worktree path is missing")
		}
		return api.Terminal{}, fail(http.StatusConflict, errorCodeTerminalState, err.Error())
	}
	return terminalView(terminal), nil
}

func (adapter *CopilotAdapter) terminalForWorktree(worktreeID uuid.UUID, terminalID uuid.UUID) (TerminalSnapshot, error) {
	terminal, found := adapter.terminals.Get(terminalID)
	if !found || terminal.WorktreeID != worktreeID {
		return TerminalSnapshot{}, fail(http.StatusNotFound, errorCodeTerminalNotFound, "no terminal with that id in this worktree")
	}
	return terminal, nil
}

func (adapter *CopilotAdapter) getTerminal(worktreeID uuid.UUID, terminalID uuid.UUID) (api.Terminal, error) {
	terminal, err := adapter.terminalForWorktree(worktreeID, terminalID)
	if err != nil {
		return api.Terminal{}, err
	}
	return terminalView(terminal), nil
}

func (adapter *CopilotAdapter) resizeTerminal(worktreeID uuid.UUID, terminalID uuid.UUID, body api.ResizeTerminalJSONRequestBody) (api.Terminal, error) {
	if _, err := adapter.terminalForWorktree(worktreeID, terminalID); err != nil {
		return api.Terminal{}, err
	}
	terminal, err := adapter.terminals.Resize(terminalID, body.Rows, body.Columns)
	if err != nil {
		return api.Terminal{}, terminalFailure(err)
	}
	return terminalView(terminal), nil
}

func (adapter *CopilotAdapter) writeTerminalInput(worktreeID uuid.UUID, terminalID uuid.UUID, body api.WriteTerminalInputJSONRequestBody) (api.Terminal, error) {
	if _, err := adapter.terminalForWorktree(worktreeID, terminalID); err != nil {
		return api.Terminal{}, err
	}
	terminal, err := adapter.terminals.Write(terminalID, body.Data)
	if err != nil {
		return api.Terminal{}, terminalFailure(err)
	}
	return terminalView(terminal), nil
}

func (adapter *CopilotAdapter) stopTerminal(worktreeID uuid.UUID, terminalID uuid.UUID) (api.Terminal, error) {
	if _, err := adapter.terminalForWorktree(worktreeID, terminalID); err != nil {
		return api.Terminal{}, err
	}
	terminal, err := adapter.terminals.Stop(terminalID)
	if err != nil {
		return api.Terminal{}, terminalFailure(err)
	}
	return terminalView(terminal), nil
}

func (adapter *CopilotAdapter) terminalEventsAfter(worktreeID uuid.UUID, terminalID uuid.UUID, afterSeq int64) (TerminalEventReplay, error) {
	replay, found := adapter.terminals.EventsAfter(terminalID, afterSeq)
	if !found || replay.Snapshot.WorktreeID != worktreeID {
		return TerminalEventReplay{}, fail(http.StatusNotFound, errorCodeTerminalNotFound, "no terminal with that id in this worktree")
	}
	return replay, nil
}

func terminalFailure(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fail(http.StatusNotFound, errorCodeTerminalNotFound, "no terminal with that id")
	}
	return fail(http.StatusConflict, errorCodeTerminalState, err.Error())
}

func terminalView(row TerminalSnapshot) api.Terminal {
	id, worktreeID, shell := row.ID, row.WorktreeID, row.Shell
	created, updated := row.CreatedAt.UTC(), row.UpdatedAt.UTC()
	return api.Terminal{
		Id: &id, WorktreeId: &worktreeID, Rows: row.Rows, Columns: row.Columns,
		Shell: &shell, Status: api.TerminalStatus(row.Status), ExitCode: row.ExitCode,
		CreatedAt: &created, UpdatedAt: &updated,
	}
}

func terminalEventView(row TerminalOutput) api.TerminalEvent {
	seq, identifier, occurredAt, truncated := row.Seq, row.TerminalID, row.OccurredAt.UTC(), row.ReplayTruncated
	view := api.TerminalEvent{
		Seq: &seq, TerminalId: &identifier, Kind: api.TerminalEventKind(row.Kind),
		OccurredAt: &occurredAt, ReplayTruncated: &truncated, ExitCode: row.ExitCode,
	}
	if row.Kind == string(api.TerminalEventKindOutput) {
		view.Data = &row.Data
	}
	return view
}
