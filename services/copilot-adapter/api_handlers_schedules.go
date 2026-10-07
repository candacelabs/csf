package copilotadapter

import (
	"context"
	"net/http"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func (handler *apiHandlers) ListChatSchedules(ctx context.Context, request api.ListChatSchedulesRequestObject) (api.ListChatSchedulesResponseObject, error) {
	page, err := handler.service.listChatSchedules(requestContext(ctx))
	if err != nil {
		return nil, err
	}
	return api.ListChatSchedules200JSONResponse(page), nil
}

func (handler *apiHandlers) CreateChatSchedule(ctx context.Context, request api.CreateChatScheduleRequestObject) (api.CreateChatScheduleResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "a schedule body is required")
	}
	schedule, err := handler.service.createChatSchedule(requestContext(ctx), *request.Body)
	if err != nil {
		return nil, err
	}
	return api.CreateChatSchedule201JSONResponse(schedule), nil
}

func (handler *apiHandlers) GetChatSchedule(ctx context.Context, request api.GetChatScheduleRequestObject) (api.GetChatScheduleResponseObject, error) {
	schedule, err := handler.service.getChatSchedule(requestContext(ctx), request.ScheduleId)
	if err != nil {
		return nil, err
	}
	return api.GetChatSchedule200JSONResponse(schedule), nil
}

func (handler *apiHandlers) UpdateChatSchedule(ctx context.Context, request api.UpdateChatScheduleRequestObject) (api.UpdateChatScheduleResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidBody, "a schedule patch is required")
	}
	schedule, err := handler.service.updateChatSchedule(requestContext(ctx), request.ScheduleId, *request.Body)
	if err != nil {
		return nil, err
	}
	return api.UpdateChatSchedule200JSONResponse(schedule), nil
}

func (handler *apiHandlers) DeleteChatSchedule(ctx context.Context, request api.DeleteChatScheduleRequestObject) (api.DeleteChatScheduleResponseObject, error) {
	if err := handler.service.deleteChatSchedule(requestContext(ctx), request.ScheduleId); err != nil {
		return nil, err
	}
	return api.DeleteChatSchedule204Response{}, nil
}
