package copilotadapter

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/httpserver"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func (handler *apiHandlers) ListTerminals(ctx context.Context, request api.ListTerminalsRequestObject) (api.ListTerminalsResponseObject, error) {
	data, err := handler.service.listTerminals(requestContext(ctx), request.WorktreeId)
	if err != nil {
		return nil, err
	}
	return api.ListTerminals200JSONResponse(api.TerminalList{Data: data}), nil
}

func (handler *apiHandlers) CreateTerminal(ctx context.Context, request api.CreateTerminalRequestObject) (api.CreateTerminalResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "terminal rows and columns are required")
	}
	terminal, err := handler.service.createTerminal(requestContext(ctx), request.WorktreeId, *request.Body)
	if err != nil {
		return nil, err
	}
	return api.CreateTerminal201JSONResponse(terminal), nil
}

func (handler *apiHandlers) GetTerminal(ctx context.Context, request api.GetTerminalRequestObject) (api.GetTerminalResponseObject, error) {
	terminal, err := handler.service.getTerminal(request.WorktreeId, request.TerminalId)
	if err != nil {
		return nil, err
	}
	return api.GetTerminal200JSONResponse(terminal), nil
}

func (handler *apiHandlers) ResizeTerminal(ctx context.Context, request api.ResizeTerminalRequestObject) (api.ResizeTerminalResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "terminal rows and columns are required")
	}
	terminal, err := handler.service.resizeTerminal(request.WorktreeId, request.TerminalId, *request.Body)
	if err != nil {
		return nil, err
	}
	return api.ResizeTerminal200JSONResponse(terminal), nil
}

func (handler *apiHandlers) WriteTerminalInput(ctx context.Context, request api.WriteTerminalInputRequestObject) (api.WriteTerminalInputResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "terminal input is required")
	}
	terminal, err := handler.service.writeTerminalInput(request.WorktreeId, request.TerminalId, *request.Body)
	if err != nil {
		return nil, err
	}
	return api.WriteTerminalInput202JSONResponse(terminal), nil
}

func (handler *apiHandlers) StopTerminal(ctx context.Context, request api.StopTerminalRequestObject) (api.StopTerminalResponseObject, error) {
	terminal, err := handler.service.stopTerminal(request.WorktreeId, request.TerminalId)
	if err != nil {
		return nil, err
	}
	return api.StopTerminal200JSONResponse(terminal), nil
}

func (handler *apiHandlers) StreamTerminalEvents(ctx context.Context, request api.StreamTerminalEventsRequestObject) (api.StreamTerminalEventsResponseObject, error) {
	if _, err := handler.service.terminalForWorktree(request.WorktreeId, request.TerminalId); err != nil {
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
	handler.streamTerminalEvents(ginContext, request.WorktreeId, request.TerminalId, afterSeq)
	return nil, nil
}

func (handler *apiHandlers) streamTerminalEvents(ginContext *gin.Context, worktreeID uuid.UUID, terminalID uuid.UUID, afterSeq int64) {
	ctx := ginContext.Request.Context()
	cursor := afterSeq
	httpserver.EventStream(ginContext, func(writer io.Writer) bool {
		replay, err := handler.service.terminalEventsAfter(worktreeID, terminalID, cursor)
		if err != nil {
			return false
		}
		for _, event := range replay.Events {
			if err := httpserver.EncodeEvent(writer, strconv.FormatInt(event.Seq, 10), terminalEventView(event)); err != nil {
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
