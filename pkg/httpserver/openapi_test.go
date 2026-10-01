package httpserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/httpserver"
)

const validationContract = `openapi: 3.0.3
info: {title: Validation fixture, version: 1.0.0}
paths:
  /profiles/{name}:
    post:
      parameters:
        - name: name
          in: path
          required: true
          schema: {type: string, pattern: '^[a-z]+\.conf$'}
        - name: limit
          in: query
          required: true
          schema: {type: integer, minimum: 1}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [url]
              properties:
                url: {type: string, format: uri}
      responses:
        '200': {description: Accepted}
      security: [{token: []}]
components:
  securitySchemes:
    token: {type: http, scheme: bearer}
`

var _ = Describe("OpenAPI request validation on Gin", func() {
	var document *openapi3.T
	var options openapi3filter.Options
	var engine *gin.Engine
	var called bool

	BeforeEach(func() {
		var err error
		document, err = openapi3.NewLoader().LoadFromData([]byte(validationContract))
		Expect(err).NotTo(HaveOccurred())
		options = openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			SchemaValidationOptions: []openapi3.SchemaValidationOption{
				openapi3.WithStringFormatValidator("uri", openapi3.NewCallbackValidator(func(value string) error {
					if !strings.HasPrefix(value, "https://") {
						return errors.New("URI must use HTTPS")
					}
					return nil
				})),
			},
		}
		engine = httpserver.NewEngine("validation-test")
		engine.UseRawPath = true
		engine.UnescapePathValues = true
		called = false
	})

	JustBeforeEach(func() {
		validator, err := httpserver.ValidateOpenAPIRequests(document, options, func(c *gin.Context, message string, status int) {
			c.JSON(status, gin.H{"code": "invalid_request", "message": message})
		})
		Expect(err).NotTo(HaveOccurred())
		engine.POST("/profiles/:name", validator, func(c *gin.Context) {
			called = true
			body, err := io.ReadAll(c.Request.Body)
			Expect(err).NotTo(HaveOccurred())
			c.Data(http.StatusOK, "application/json", body)
		})
		engine.GET("/unrelated", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		engine.GET("/undeclared", validator)
	})

	It("preserves the request body for the handler and leaves unrelated routes alone", func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/profiles/a.conf?limit=1", strings.NewReader(`{"url":"https://example.invalid"}`))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(Equal(`{"url":"https://example.invalid"}`))
		Expect(called).To(BeTrue())

		response = httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/unrelated", nil))
		Expect(response.Code).To(Equal(http.StatusNoContent))
	})

	DescribeTable("rejects invalid values and aborts even when the error callback does not",
		func(target string, body string) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(response, request)
			Expect(response.Code).To(Equal(http.StatusBadRequest))
			Expect(response.Body.String()).To(ContainSubstring(`"code":"invalid_request"`))
			Expect(called).To(BeFalse())
		},
		Entry("path schema", "/profiles/bad?limit=1", `{"url":"https://example.invalid"}`),
		Entry("encoded trailing slash", "/profiles/a.conf%2F?limit=1", `{"url":"https://example.invalid"}`),
		Entry("query schema", "/profiles/a.conf?limit=0", `{"url":"https://example.invalid"}`),
		Entry("required query", "/profiles/a.conf", `{"url":"https://example.invalid"}`),
		Entry("required body field", "/profiles/a.conf?limit=1", `{}`),
		Entry("custom format option", "/profiles/a.conf?limit=1", `{"url":"http://example.invalid"}`),
		Entry("malformed JSON", "/profiles/a.conf?limit=1", `{`),
	)

	It("returns the caller's 404 error for an undeclared route", func() {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/undeclared", nil))
		Expect(response.Code).To(Equal(http.StatusNotFound))
		Expect(response.Body.String()).To(ContainSubstring(`"code":"invalid_request"`))
	})

	When("authentication needs the request's context", func() {
		BeforeEach(func() {
			options.AuthenticationFunc = func(ctx context.Context, _ *openapi3filter.AuthenticationInput) error {
				return ctx.Err()
			}
		})
		It("passes cancellation through to validation", func() {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			request := httptest.NewRequest(http.MethodPost, "/profiles/a.conf?limit=1", strings.NewReader(`{"url":"https://example.invalid"}`)).WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			Expect(response.Code).To(Equal(http.StatusBadRequest))
			Expect(response.Body.String()).To(ContainSubstring("context canceled"))
			Expect(called).To(BeFalse())
		})
	})

	It("reports an invalid contract during registration", func() {
		document.Info = nil
		_, err := httpserver.ValidateOpenAPIRequests(document, options, func(_ *gin.Context, _ string, _ int) {})
		Expect(err).To(HaveOccurred())
	})
})
