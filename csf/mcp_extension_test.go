package csf_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/candacelabs/csf/csf"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	consumerMCPToolName        = "ConsumerHealth"
	consumerMCPToolDescription = "Return a consumer-owned health value."
	consumerMCPToolValue       = "consumer-ready"
	consumerMCPToolCall        = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ConsumerHealth","arguments":{"probe":"ready"}}}`
	consumerMCPToolMalformed   = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ConsumerHealth","arguments":{"probe":7}}}`
	consumerMCPToolList        = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
)

type consumerMCPInput struct {
	Probe string `json:"probe"`
}

type consumerMCPOutput struct {
	Status string `json:"status"`
}

var _ = Describe("consumer MCP extension", func() {
	It("discovers and calls a consumer tool on the same server", func() {
		service, err := csf.New(csf.WithMCPTool[consumerMCPInput, consumerMCPOutput](mcp.Tool{
			Name:        consumerMCPToolName,
			Title:       consumerMCPToolDescription,
			Description: consumerMCPToolDescription,
		}, consumerMCPHandler))
		Expect(err).NotTo(HaveOccurred())

		handler := service.MCPHandler()
		listResponse := callMCP(handler, consumerMCPToolList)
		Expect(listResponse.Code).To(Equal(http.StatusOK), listResponse.Body.String())
		Expect(listResponse.Body.String()).To(ContainSubstring(`"name":"` + consumerMCPToolName + `"`))
		Expect(listResponse.Body.String()).To(ContainSubstring(`"name":"Search"`))

		callResponse := callMCP(handler, consumerMCPToolCall)
		Expect(callResponse.Code).To(Equal(http.StatusOK), callResponse.Body.String())
		Expect(callResponse.Body.String()).To(ContainSubstring(consumerMCPToolValue))

		malformedResponse := callMCP(handler, consumerMCPToolMalformed)
		Expect(malformedResponse.Code).To(Equal(http.StatusOK), malformedResponse.Body.String())
		Expect(malformedResponse.Body.String()).To(ContainSubstring(`"isError":true`))
	})

	DescribeTable("rejects invalid or colliding registrations", func(tool mcp.Tool, expected error) {
		_, err := csf.New(csf.WithMCPTool[consumerMCPInput, consumerMCPOutput](tool, consumerMCPHandler))
		Expect(err).To(MatchError(ContainSubstring(expected.Error())))
	},
		Entry("invalid name", mcp.Tool{Name: "consumer health"}, csf.ErrInvalidMCPTool),
		Entry("CSF collision", mcp.Tool{Name: "Search"}, csf.ErrMCPToolConflict),
	)

	It("rejects a duplicate consumer name before the server starts", func() {
		tool := mcp.Tool{Name: consumerMCPToolName}
		_, err := csf.New(csf.WithMCPTool[consumerMCPInput, consumerMCPOutput](tool, consumerMCPHandler), csf.WithMCPTool[consumerMCPInput, consumerMCPOutput](tool, consumerMCPHandler))
		Expect(errors.Is(err, csf.ErrMCPToolConflict)).To(BeTrue(), err)
	})

	It("rejects a missing typed handler before the server starts", func() {
		var handler mcp.ToolHandlerFor[consumerMCPInput, consumerMCPOutput]
		_, err := csf.New(csf.WithMCPTool[consumerMCPInput, consumerMCPOutput](mcp.Tool{Name: "MissingHandler"}, handler))
		Expect(errors.Is(err, csf.ErrInvalidMCPTool)).To(BeTrue(), err)
	})
})

func consumerMCPHandler(_ context.Context, _ *mcp.CallToolRequest, input consumerMCPInput) (*mcp.CallToolResult, consumerMCPOutput, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: consumerMCPToolValue}}}, consumerMCPOutput{Status: input.Probe}, nil
}

func callMCP(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-03-26")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
