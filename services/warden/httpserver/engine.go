// Package httpserver builds the single gin.Engine every warden node serves its
// HTTP surface from (dashboard + /api/status + /metrics). The node-to-node
// cluster RPCs are served by the gRPC WardenService on the same port (see
// services/warden/grpcmux), not by this engine.
//
// The engine configuration lives here, apart from cmd/main.go, so the package
// tests (dashboard, metrics) can exercise the *exact* production middleware,
// 405, and 404 behavior rather than a bare gin.New(). cmd/main.go's newRouter
// and every test build the router from NewEngine and then call the per-subsystem
// Register functions on it.
//
// NewEngine is therefore the definition of warden's HTTP edge semantics, not a
// convenience: callers may rely on it returning an engine that has no routes of
// its own, answers an unknown path with 404 and a known path with the wrong
// method with 405, and performs no implicit redirects. Anything that changes
// those answers belongs here, where one change reaches production and every
// test at once.
package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	core "github.com/candacelabs/csf/pkg/core"
)

// NewEngine returns a gin.Engine configured to match the semantics of the
// previous net/http ServeMux server:
//
//   - gin.ReleaseMode + gin.New() (no gin request logger noise; a minimal
//     panic-recovery middleware is installed below).
//   - HandleMethodNotAllowed: a request to a known path with the wrong method
//     yields 405 (with an Allow header) instead of 404, matching ServeMux.
//   - Redirects disabled: no implicit trailing-slash or fixed-path redirects are
//     introduced over the frozen wire contract; unmatched paths 404 outright.
func NewEngine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.HandleMethodNotAllowed = true
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.Use(recovery())
	return r
}

// recovery is a minimal panic-recovery middleware. gin.New() installs nothing,
// so this stands in for gin.Default()'s Recovery without also pulling in gin's
// request logger: it logs the panic via the shared core.Logger and returns 500.
func recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				if core.Logger != nil {
					core.Logger.Error().
						Interface("panic", rec).
						Str("method", c.Request.Method).
						Str("path", c.Request.URL.Path).
						Msg("warden http: recovered from panic")
				}
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
