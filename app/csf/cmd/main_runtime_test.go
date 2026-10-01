package main

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	copilot "github.com/github/copilot-sdk/go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
)

var _ = Describe("CSF command adapters", func() {
	It("discovers Copilot history IDs through the configured MCP tool", func() {
		service, err := csf.New(withCopilotHistorySessionsTool(copilotHistorySessionLister(func(_ context.Context, filter *copilot.SessionListFilter) ([]copilot.SessionMetadata, error) {
			Expect(filter.Repository).To(Equal("synthetic/example"))
			return []copilot.SessionMetadata{{SessionID: "synthetic-session"}}, nil
		})))
		Expect(err).NotTo(HaveOccurred())

		handler := service.MCPHandler()
		list := callMCP(handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		Expect(list.Code).To(Equal(http.StatusOK), list.Body.String())
		Expect(list.Body.String()).To(ContainSubstring(copilotHistorySessionsToolName))
		Expect(list.Body.String()).To(ContainSubstring(`"readOnlyHint":true`))

		called := callMCP(handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ListCopilotHistorySessions","arguments":{"repository":"synthetic/example"}}}`)
		Expect(called.Code).To(Equal(http.StatusOK), called.Body.String())
		Expect(called.Body.String()).To(ContainSubstring("synthetic-session"))

		malformed := callMCP(handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ListCopilotHistorySessions","arguments":{"repository":7}}}`)
		Expect(malformed.Code).To(Equal(http.StatusOK), malformed.Body.String())
		Expect(malformed.Body.String()).To(ContainSubstring(`"isError":true`))
	})

	It("returns an empty session list when the configured history contains no sessions", func() {
		service, err := csf.New(withCopilotHistorySessionsTool(func(_ context.Context, _ *copilot.SessionListFilter) ([]copilot.SessionMetadata, error) {
			return nil, nil
		}))
		Expect(err).NotTo(HaveOccurred())
		response := callMCP(service.MCPHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ListCopilotHistorySessions","arguments":{}}}`)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(ContainSubstring(`"sessions":[]`))
		Expect(response.Body.String()).NotTo(ContainSubstring(`"isError":true`))
	})

	It("calls a generated operation with an empty JSON request", func() {
		service, err := csf.New()
		Expect(err).NotTo(HaveOccurred())
		router := httpserver.NewEngine("csf-command-call")
		service.Register(router)
		server := httptest.NewServer(router)
		DeferCleanup(server.Close)
		output := &bytes.Buffer{}
		Expect(callWithStreams([]string{"--endpoint", server.URL, "GetSnapshot"}, bytes.NewReader(nil), output)).To(Succeed())
		response := &pb.GetSnapshotResponse{}
		Expect(protojson.Unmarshal(output.Bytes(), response)).To(Succeed())
		Expect(response.Snapshot.Issues).To(ContainElement(ContainSubstring("no event source configured")))
	})

	It("writes one JSON response for each JSONL runtime request", func() {
		request, err := protojson.Marshal(&pb.RuntimeRequest{
			Kind:        pb.RequestKind_REQUEST_KIND_STEP,
			Observation: &pb.Observation{Epoch: 1, Sequence: 1, Tick: 1, Features: []int64{0, 0, 0, 0}},
			Tick:        1,
		})
		Expect(err).NotTo(HaveOccurred())
		input := bytes.NewBuffer(append([]byte("not JSON\n"), append(request, '\n')...))
		output := &bytes.Buffer{}
		Expect(runWithStreams(input, output)).To(Succeed())
		scanner := bufio.NewScanner(output)
		Expect(scanner.Scan()).To(BeTrue())
		invalid := &pb.RuntimeResponse{}
		Expect(protojson.Unmarshal(scanner.Bytes(), invalid)).To(Succeed())
		Expect(invalid.Error).NotTo(BeEmpty())
		Expect(scanner.Scan()).To(BeTrue())
		fallback := &pb.RuntimeResponse{}
		Expect(protojson.Unmarshal(scanner.Bytes(), fallback)).To(Succeed())
		Expect(fallback.Action.Fallback).To(BeTrue())
		Expect(fallback.Action.Reason).To(Equal("no_active_controller_or_observation"))
		Expect(scanner.Scan()).To(BeFalse())
		Expect(scanner.Err()).NotTo(HaveOccurred())
	})
})

func callMCP(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-03-26")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
