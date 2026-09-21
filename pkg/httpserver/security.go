package httpserver

import "github.com/gin-gonic/gin"

// BrowserContentSecurityPolicy permits only the same-origin assets and socket
// needed by a server-rendered Candace page.
const BrowserContentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"connect-src 'self'; " +
	"img-src 'self' data:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// StrictBrowserSecurity applies the shared browser-facing response policy.
func StrictBrowserSecurity() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", BrowserContentSecurityPolicy)
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
	}
}
