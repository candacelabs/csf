package copilotadapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/httpserver"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const (
	errorCodeTerminalNotFound = "terminal_not_found"
	errorCodeTerminalState    = "terminal_state"
)

// ListTerminals lists process-owned terminals for one persisted worktree.
func (adapter *CopilotAdapter) ListTerminals(ctx context.Context, request api.ListTerminalsRequestObject) (api.ListTerminalsResponseObject, error) {
	ctx = requestContext(ctx)
	if _, err := adapter.store.GetWorktree(ctx, request.WorktreeId); err != nil {
		return nil, worktreeLookupError(err)
	}
	rows := adapter.terminals.List(request.WorktreeId)
	data := make([]api.Terminal, 0, len(rows))
	for _, row := range rows {
		data = append(data, terminalView(row))
	}
	return api.ListTerminals200JSONResponse(api.TerminalList{Data: data}), nil
}

// CreateTerminal starts the fixed shell in a worktree.
func (adapter *CopilotAdapter) CreateTerminal(ctx context.Context, request api.CreateTerminalRequestObject) (api.CreateTerminalResponseObject, error) {
	ctx = requestContext(ctx)
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "terminal rows and columns are required")
	}
	worktree, err := adapter.store.GetWorktree(ctx, request.WorktreeId)
	if err != nil {
		return nil, worktreeLookupError(err)
	}
	worktree, unlockWorktree, err := adapter.lockValidatedWorktree(ctx, worktree)
	if err != nil {
		if errors.Is(err, ErrInvalidWorktree) {
			return nil, fail(http.StatusNotFound, errorCodeWorktreeNotFound, "the worktree path is missing")
		}
		return nil, worktreeValidationFailure(err)
	}
	defer unlockWorktree()
	terminal, err := adapter.terminals.Create(ctx, TerminalSpec{
		WorktreeID: worktree.ID, Directory: worktree.Path, Rows: request.Body.Rows, Columns: request.Body.Columns,
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if quarantineErr := adapter.quarantineWorktreeSessionsLocked(ctx, worktree.ID); quarantineErr != nil {
				return nil, storeFailure(quarantineErr)
			}
			return nil, fail(http.StatusNotFound, errorCodeWorktreeNotFound, "the worktree path is missing")
		}
		return nil, fail(http.StatusConflict, errorCodeTerminalState, err.Error())
	}
	return api.CreateTerminal201JSONResponse(terminalView(terminal)), nil
}

// GetTerminal reads one terminal belonging to the requested worktree.
func (adapter *CopilotAdapter) GetTerminal(ctx context.Context, request api.GetTerminalRequestObject) (api.GetTerminalResponseObject, error) {
	terminal, err := adapter.terminalForWorktree(request.WorktreeId, request.TerminalId)
	if err != nil {
		return nil, err
	}
	return api.GetTerminal200JSONResponse(terminalView(terminal)), nil
}

// ResizeTerminal changes one live PTY size.
func (adapter *CopilotAdapter) ResizeTerminal(ctx context.Context, request api.ResizeTerminalRequestObject) (api.ResizeTerminalResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "terminal rows and columns are required")
	}
	if _, err := adapter.terminalForWorktree(request.WorktreeId, request.TerminalId); err != nil {
		return nil, err
	}
	terminal, err := adapter.terminals.Resize(request.TerminalId, request.Body.Rows, request.Body.Columns)
	if err != nil {
		return nil, terminalFailure(err)
	}
	return api.ResizeTerminal200JSONResponse(terminalView(terminal)), nil
}

// WriteTerminalInput writes one UTF-8 chunk to a live PTY.
func (adapter *CopilotAdapter) WriteTerminalInput(ctx context.Context, request api.WriteTerminalInputRequestObject) (api.WriteTerminalInputResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "terminal input is required")
	}
	if _, err := adapter.terminalForWorktree(request.WorktreeId, request.TerminalId); err != nil {
		return nil, err
	}
	terminal, err := adapter.terminals.Write(request.TerminalId, request.Body.Data)
	if err != nil {
		return nil, terminalFailure(err)
	}
	return api.WriteTerminalInput202JSONResponse(terminalView(terminal)), nil
}

// StopTerminal stops one PTY and returns its terminal state.
func (adapter *CopilotAdapter) StopTerminal(ctx context.Context, request api.StopTerminalRequestObject) (api.StopTerminalResponseObject, error) {
	if _, err := adapter.terminalForWorktree(request.WorktreeId, request.TerminalId); err != nil {
		return nil, err
	}
	terminal, err := adapter.terminals.Stop(request.TerminalId)
	if err != nil {
		return nil, terminalFailure(err)
	}
	return api.StopTerminal200JSONResponse(terminalView(terminal)), nil
}

// StreamTerminalEvents replays bounded output and follows the PTY until exit.
func (adapter *CopilotAdapter) StreamTerminalEvents(ctx context.Context, request api.StreamTerminalEventsRequestObject) (api.StreamTerminalEventsResponseObject, error) {
	if _, err := adapter.terminalForWorktree(request.WorktreeId, request.TerminalId); err != nil {
		return nil, err
	}
	ginContext, ok := ctx.(*gin.Context)
	if !ok {
		return nil, fail(http.StatusInternalServerError, errorCodeStreamUnavailable, "the terminal stream needs the Gin request")
	}
	afterSeq := int64(0)
	if request.Params.LastEventID != nil {
		afterSeq = *request.Params.LastEventID
	}
	adapter.streamTerminalEvents(ginContext, request.TerminalId, afterSeq)
	return nil, nil
}

func (adapter *CopilotAdapter) streamTerminalEvents(ginContext *gin.Context, terminalID uuid.UUID, afterSeq int64) {
	ctx := ginContext.Request.Context()
	cursor := afterSeq
	httpserver.EventStream(ginContext, func(writer io.Writer) bool {
		replay, found := adapter.terminals.EventsAfter(terminalID, cursor)
		if !found {
			return false
		}
		for _, event := range replay.Events {
			view := terminalEventView(event)
			if err := httpserver.EncodeEvent(writer, strconv.FormatInt(event.Seq, 10), view); err != nil {
				return false
			}
			cursor = event.Seq
		}
		if replay.Snapshot.Status == string(api.TerminalStatusExited) || replay.Snapshot.Status == string(api.TerminalStatusFailed) {
			return false
		}
		// Gin flushes when the callback returns. Never wait with encoded PTY
		// output still in its buffer, and wake on output rather than polling.
		if len(replay.Events) > 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-replay.Changed:
			return true
		}
	})
}

func (adapter *CopilotAdapter) terminalForWorktree(worktreeID uuid.UUID, terminalID uuid.UUID) (TerminalSnapshot, error) {
	terminal, found := adapter.terminals.Get(terminalID)
	if !found || terminal.WorktreeID != worktreeID {
		return TerminalSnapshot{}, fail(http.StatusNotFound, errorCodeTerminalNotFound, "no terminal with that id in this worktree")
	}
	return terminal, nil
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
