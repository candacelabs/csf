// Package httpserver owns CandaceOS Core's single configured Gin engine.
//
// The package exists so that every Core process serves from one router with
// one security posture. Callers may rely on NewEngine returning a caller-owned
// engine that applies the restrictive Content-Security-Policy and the
// accompanying hardening headers to every response, that disables
// trailing-slash and fixed-path redirects, and that reports an unsupported
// method rather than falling through to a not-found. Components mount routes
// onto that engine through gin.IRouter and never build an engine, a
// standard-library mux, or a catch-all proxy of their own.
package httpserver

import "github.com/gin-gonic/gin"

// NewEngine returns the caller-owned router shared by every Core HTTP
// component. Components mount routes through gin.IRouter and never create an
// engine, standard-library mux, or catch-all proxy of their own.
func NewEngine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	router.Use(securityHeaders())
	return router
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
	}
}
