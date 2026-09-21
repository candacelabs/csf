package httpserver

import (
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/gin-gonic/gin"
)

// ValidateOpenAPIRequests adapts kin-openapi validation to a caller-owned Gin
// router. Callers own authentication options and the validation error response.
// The returned middleware must be scoped to routes declared in the contract.
func ValidateOpenAPIRequests(document *openapi3.T, options openapi3filter.Options, onError func(context *gin.Context, message string, statusCode int)) (gin.HandlerFunc, error) {
	router, err := legacy.NewRouter(document)
	if err != nil {
		return nil, fmt.Errorf("create OpenAPI request validator: %w", err)
	}
	return func(context *gin.Context) {
		route, parameters, err := router.FindRoute(context.Request)
		if err != nil {
			status := http.StatusBadRequest
			if routeError, ok := err.(*routers.RouteError); ok && routeError.Reason == routers.ErrPathNotFound.Error() {
				status = http.StatusNotFound
			}
			onError(context, err.Error(), status)
			context.Abort()
			return
		}
		// Gin already matched the request. Preserve its complete parameter values;
		// the schema router can normalize a trailing slash in an encoded value.
		for _, parameter := range context.Params {
			parameters[parameter.Key] = parameter.Value
		}
		err = openapi3filter.ValidateRequest(context.Request.Context(), &openapi3filter.RequestValidationInput{
			Request:    context.Request,
			PathParams: parameters,
			Route:      route,
			Options:    &options,
		})
		if err != nil {
			onError(context, err.Error(), http.StatusBadRequest)
			context.Abort()
			return
		}
		context.Next()
	}, nil
}
