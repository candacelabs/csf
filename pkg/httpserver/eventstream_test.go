package httpserver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/httpserver"
)

// The specs run against a real httptest server rather than a bare
// gin.CreateTestContext: gin's Stream asks its writer for CloseNotify, which
// only a genuine net/http response writer implements.
var _ = Describe("EventStream", func() {
	var engine *gin.Engine
	var server *httptest.Server

	BeforeEach(func() {
		gin.SetMode(gin.TestMode)
		engine = gin.New()
	})

	AfterEach(func() {
		if server != nil {
			server.Close()
			server = nil
		}
	})

	get := func(path string) *http.Response {
		server = httptest.NewServer(engine)
		response, err := http.Get(server.URL + path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(response.Body.Close)
		return response
	}

	It("writes the server-sent-event headers no streaming handler should restate", func() {
		engine.GET("/events", func(context *gin.Context) {
			httpserver.EventStream(context, func(writer io.Writer) bool { return false })
		})

		response := get("/events")

		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
		Expect(response.Header.Get("Cache-Control")).To(Equal("no-cache"))
		Expect(response.Header.Get("Connection")).To(Equal("keep-alive"))
		Expect(response.Header.Get("X-Accel-Buffering")).To(Equal("no"))
	})

	It("runs the step until it asks to stop, encoding one frame per call", func() {
		remaining := 3
		engine.GET("/events", func(context *gin.Context) {
			httpserver.EventStream(context, func(writer io.Writer) bool {
				remaining--
				Expect(httpserver.EncodeEvent(writer, "7", map[string]string{"kind": "tick"})).To(Succeed())
				return remaining > 0
			})
		})

		body, err := io.ReadAll(get("/events").Body)

		Expect(err).NotTo(HaveOccurred())
		Expect(remaining).To(Equal(0))
		Expect(strings.Count(string(body), "id:7")).To(Equal(3))
		Expect(string(body)).To(ContainSubstring(`data:{"kind":"tick"}`))
	})

	It("sets the same headers as a middleware on a route group", func() {
		group := engine.Group("/stream", httpserver.EventStreamHeaders())
		group.GET("", func(context *gin.Context) { context.Status(http.StatusOK) })

		response := get("/stream")

		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
		Expect(response.Header.Get("X-Accel-Buffering")).To(Equal("no"))
	})
})
