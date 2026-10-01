package csf

import (
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("shared MCP HTTP transport", func() {
	DescribeTable("lists tools with legacy and per-request protocol metadata",
		func(version string, metadata string) {
			service, err := New()
			Expect(err).NotTo(HaveOccurred())
			handler := service.MCPHandler()
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":` + metadata + `}`
			request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("MCP-Protocol-Version", version)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			Expect(response.Body.String()).To(ContainSubstring(`"name":"Search"`))
			Expect(response.Body.String()).NotTo(ContainSubstring(`"error"`))
		},
		Entry("legacy client", "2025-03-26", `{}`),
		Entry("Copilot per-request metadata", "2025-11-25", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25"}}`),
	)
})
