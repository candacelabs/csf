package copilotadapter

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

// apiHandlers owns HTTP decoding, response envelopes, and stream framing.
// The service owns persistence and lifecycle transitions; mounting this adapter
// adds no process or IPC boundary between those layers.
type apiHandlers struct {
	service *CopilotAdapter
}

func (handler *apiHandlers) GetHealth(ctx context.Context, request api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	result, err := handler.service.health(requestContext(ctx))
	if err != nil {
		return nil, err
	}
	return api.GetHealth200JSONResponse(result), nil
}

func (handler *apiHandlers) ListModels(ctx context.Context, request api.ListModelsRequestObject) (api.ListModelsResponseObject, error) {
	result, err := handler.service.models(requestContext(ctx))
	if err != nil {
		return nil, err
	}
	return api.ListModels200JSONResponse(result), nil
}

func (handler *apiHandlers) ListSessions(ctx context.Context, request api.ListSessionsRequestObject) (api.ListSessionsResponseObject, error) {
	result, err := handler.service.listSessions(requestContext(ctx), request.Params)
	if err != nil {
		return nil, err
	}
	return api.ListSessions200JSONResponse(result), nil
}

func (handler *apiHandlers) CreateSession(ctx context.Context, request api.CreateSessionRequestObject) (api.CreateSessionResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "a session body is required")
	}
	submission, err := decodeCreateSessionRequest(*request.Body)
	if err != nil {
		return nil, err
	}
	result, err := handler.service.createSession(requestContext(ctx), submission)
	if err != nil {
		return nil, err
	}
	return api.CreateSession201JSONResponse(result), nil
}

func (handler *apiHandlers) GetSession(ctx context.Context, request api.GetSessionRequestObject) (api.GetSessionResponseObject, error) {
	result, err := handler.service.getSession(requestContext(ctx), request.SessionId)
	if err != nil {
		return nil, err
	}
	return api.GetSession200JSONResponse(result), nil
}

func (handler *apiHandlers) GetActiveTurn(ctx context.Context, request api.GetActiveTurnRequestObject) (api.GetActiveTurnResponseObject, error) {
	result, err := handler.service.activeTurn(requestContext(ctx), request.SessionId)
	if err != nil {
		return nil, err
	}
	return api.GetActiveTurn200JSONResponse(result), nil
}

func (handler *apiHandlers) UpdateSession(ctx context.Context, request api.UpdateSessionRequestObject) (api.UpdateSessionResponseObject, error) {
	if request.Body == nil || (request.Body.Model == nil && request.Body.DisplayName == nil) {
		// minProperties:1 is not enforced by the generated validator.
		return nil, fail(http.StatusBadRequest, errorCodeEmptyPatch, "at least one of model or displayName is required")
	}
	result, err := handler.service.updateSession(requestContext(ctx), request.SessionId, *request.Body)
	if err != nil {
		return nil, err
	}
	return api.UpdateSession200JSONResponse(result), nil
}

func (handler *apiHandlers) EndSession(ctx context.Context, request api.EndSessionRequestObject) (api.EndSessionResponseObject, error) {
	result, err := handler.service.endSession(requestContext(ctx), request.SessionId)
	if err != nil {
		return nil, err
	}
	return api.EndSession200JSONResponse(result), nil
}

func (handler *apiHandlers) AbortTurn(ctx context.Context, request api.AbortTurnRequestObject) (api.AbortTurnResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "an abort body is required")
	}
	targetTurnID := uuid.UUID(request.Body.TurnId)
	idempotencyKey := uuid.UUID(request.Body.IdempotencyKey)
	if targetTurnID == uuid.Nil || idempotencyKey == uuid.Nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidRequest, "turnId and idempotencyKey are required")
	}
	result, err := handler.service.abortTurn(requestContext(ctx), request.SessionId, targetTurnID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return api.AbortTurn200JSONResponse(result), nil
}

func (handler *apiHandlers) ListTranscript(ctx context.Context, request api.ListTranscriptRequestObject) (api.ListTranscriptResponseObject, error) {
	result, err := handler.service.transcript(requestContext(ctx), request.SessionId, request.Params)
	if err != nil {
		return nil, err
	}
	return api.ListTranscript200JSONResponse(result), nil
}

func (handler *apiHandlers) ListSessionRequests(ctx context.Context, request api.ListSessionRequestsRequestObject) (api.ListSessionRequestsResponseObject, error) {
	result, err := handler.service.sessionRequests(requestContext(ctx), request.SessionId)
	if err != nil {
		return nil, err
	}
	return api.ListSessionRequests200JSONResponse(result), nil
}

func (handler *apiHandlers) ResolveSessionRequest(ctx context.Context, request api.ResolveSessionRequestRequestObject) (api.ResolveSessionRequestResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "a decision body is required")
	}
	decision, err := decodeResolveSessionRequest(*request.Body)
	if err != nil {
		return nil, err
	}
	result, err := handler.service.resolveSessionRequest(requestContext(ctx), request.SessionId, request.RequestId, decision)
	if err != nil {
		return nil, err
	}
	return api.ResolveSessionRequest200JSONResponse(result), nil
}

func (handler *apiHandlers) SubmitPrompt(ctx context.Context, request api.SubmitPromptRequestObject) (api.SubmitPromptResponseObject, error) {
	ctx = requestContext(ctx)
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "a prompt body is required")
	}
	idempotencyKey := uuid.UUID(request.Body.IdempotencyKey)
	if idempotencyKey == uuid.Nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidRequest, "idempotencyKey must be a non-zero UUID")
	}
	author := ""
	if request.Body.Author != nil {
		author = *request.Body.Author
	}
	turn, _, err := handler.service.submitPrompt(ctx, request.SessionId, promptSubmission{
		Text: request.Body.Text, Mode: request.Body.Mode, Author: author,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	return api.SubmitPrompt202JSONResponse(views.Turn(turn)), nil
}
func (handler *apiHandlers) StreamSessionEvents(ctx context.Context, request api.StreamSessionEventsRequestObject) (api.StreamSessionEventsResponseObject, error) {
	if err := handler.service.requireSession(requestContext(ctx), request.SessionId); err != nil {
		return nil, err
	}
	afterSeq := int64(0)
	if request.Params.LastEventID != nil {
		afterSeq = *request.Params.LastEventID
	}
	ginContext, ok := ctx.(*gin.Context)
	if !ok {
		return nil, fail(http.StatusInternalServerError, errorCodeStreamUnavailable, "the event stream needs the Gin request")
	}
	handler.streamSessionEvents(ginContext, request.SessionId, afterSeq)
	// The stream already wrote the response; a nil response object tells the
	// generated wrapper there is nothing left to write.
	return nil, nil
}
func renderFailures(next api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
	return func(context *gin.Context, request interface{}) (interface{}, error) {
		response, err := next(context, request)
		var f failure
		if errors.As(err, &f) {
			context.AbortWithStatusJSON(f.status, errorBody(f.code, f.message))
			return nil, nil
		}
		return response, err
	}
}
func errorBody(code string, message string) api.Error {
	return api.Error{Code: code, Message: message}
}
