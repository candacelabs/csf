// Package httpserver provides the shared Gin HTTP surface used by Candace
// services. Applications register routes; this package owns the behavior that
// must remain identical across service binaries.
package httpserver

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/core"
)

const requestIDHeader = "X-Request-ID"

type engineConfig struct {
	requestLogging       bool
	strictBrowserHeaders bool
}

// EngineOption enables optional shared middleware.
type EngineOption func(config *engineConfig)

// WithRequestLogging adds structured completion logs and request IDs.
func WithRequestLogging() EngineOption {
	return func(config *engineConfig) {
		config.requestLogging = true
	}
}

// WithStrictBrowserSecurity applies the shared locked-down browser policy.
func WithStrictBrowserSecurity() EngineOption {
	return func(config *engineConfig) {
		config.strictBrowserHeaders = true
	}
}

// NewEngine returns a quiet production Gin engine with explicit 404/405
// behavior and panic recovery through core.Logger.
func NewEngine(service string, options ...EngineOption) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	config := engineConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	if config.requestLogging {
		engine.Use(RequestLogger(service))
	}
	if config.strictBrowserHeaders {
		engine.Use(StrictBrowserSecurity())
	}
	engine.Use(Recovery(service))
	return engine
}

// RequestLogger assigns or preserves a request ID and emits one structured log
// event after the request completes.
func RequestLogger(service string) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		requestID := c.GetHeader(requestIDHeader)
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Header(requestIDHeader, requestID)
		c.Set("request_id", requestID)

		c.Next()

		if isSuccessfulProbe(c.Request.URL.Path, c.Writer.Status()) {
			return
		}
		if core.Logger != nil {
			core.Logger.Info().
				Str("service", service).
				Str("request_id", requestID).
				Str("method", c.Request.Method).
				Str("path", c.Request.URL.Path).
				Int("status", c.Writer.Status()).
				Int64("duration_ms", time.Since(started).Milliseconds()).
				Msg("http request")
		}
	}
}

func isSuccessfulProbe(path string, status int) bool {
	return (path == "/healthz" || path == "/readyz") &&
		status >= http.StatusOK && status < http.StatusMultipleChoices
}

// Recovery converts a panic into a 500 response and records it with the shared
// logger. It deliberately does not include Gin's default text logger.
func Recovery(service string) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if core.Logger != nil {
					event := core.Logger.Error().
						Str("service", service).
						Interface("panic", recovered).
						Str("method", c.Request.Method).
						Str("path", c.Request.URL.Path)
					if requestID, ok := c.Get("request_id"); ok {
						event = event.Interface("request_id", requestID)
					}
					event.Msg("http panic recovered")
				}
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
