package copilotadapter

import (
	"context"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func (handler *apiHandlers) GetTelemetry(ctx context.Context, request api.GetTelemetryRequestObject) (api.GetTelemetryResponseObject, error) {
	snapshot, err := handler.service.Telemetry(requestContext(ctx))
	if err != nil {
		return nil, storeFailure(err)
	}
	return api.GetTelemetry200JSONResponse(snapshot), nil
}
