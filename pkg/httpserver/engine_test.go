package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sharedhttp "github.com/candacelabs/csf/pkg/httpserver"
)

var _ = Describe("The shared Gin engine", func() {
	It("preserves explicit method and path behavior", func() {
		engine := sharedhttp.NewEngine("test", sharedhttp.WithRequestLogging())
		engine.GET("/known", func(c *gin.Context) { c.Status(http.StatusNoContent) })

		for _, test := range []struct {
			method string
			target string
			status int
		}{
			{http.MethodGet, "/known", http.StatusNoContent},
			{http.MethodPost, "/known", http.StatusMethodNotAllowed},
			{http.MethodGet, "/known/", http.StatusNotFound},
			{http.MethodGet, "/missing", http.StatusNotFound},
		} {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))
			Expect(response.Code).To(Equal(test.status))
			Expect(response.Header().Get("X-Request-ID")).NotTo(BeEmpty())
		}
	})

	It("applies the named locked-down browser policy", func() {
		engine := sharedhttp.NewEngine("test", sharedhttp.WithStrictBrowserSecurity())
		engine.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

		Expect(response.Header().Get("Content-Security-Policy")).To(Equal(sharedhttp.BrowserContentSecurityPolicy))
		Expect(response.Header().Get("Referrer-Policy")).To(Equal("no-referrer"))
		Expect(response.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(response.Header().Get("X-Frame-Options")).To(Equal("DENY"))
		Expect(response.Header().Get("Permissions-Policy")).To(ContainSubstring("camera=()"))
		Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
	})

	It("preserves a caller request ID", func() {
		engine := sharedhttp.NewEngine("test", sharedhttp.WithRequestLogging())
		engine.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("X-Request-ID", "upstream-request-id")
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		Expect(response.Header().Get("X-Request-ID")).To(Equal("upstream-request-id"))
	})

	It("recovers panics without Gin's default logger", func() {
		engine := sharedhttp.NewEngine("test", sharedhttp.WithRequestLogging())
		engine.GET("/panic", func(c *gin.Context) { panic("boom") })
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
	})

	It("owns the long-lived response server timeouts", func() {
		called := false
		handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			called = true
			writer.WriteHeader(http.StatusNoContent)
		})
		server := sharedhttp.NewStreamingServer(":8080", handler)

		Expect(server.Addr).To(Equal(":8080"))
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		Expect(called).To(BeTrue())
		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(server.ReadHeaderTimeout).To(Equal(5 * time.Second))
		Expect(server.ReadTimeout).To(BeZero())
		Expect(server.WriteTimeout).To(BeZero())
		Expect(server.IdleTimeout).To(Equal(60 * time.Second))
		Expect(server.MaxHeaderBytes).To(Equal(16 << 10))
	})
})
