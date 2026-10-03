package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/ros"
	"github.com/candacelabs/csf/pkg/httpserver"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	copilot "github.com/github/copilot-sdk/go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../../../ipc/ros/spine.go -destination=mock_spine_test.go -package=main

// Unit specs of the JSONL adapter's spine paths the stub cannot reach.
var _ = Describe("JSONL spine adapter", func() {
	var spine *MockISpine
	step := func() *pb.RuntimeRequest {
		return &pb.RuntimeRequest{Kind: pb.RequestKind_REQUEST_KIND_STEP, Observation: &pb.Observation{Epoch: 2, Sequence: 9}}
	}

	BeforeEach(func() { spine = NewMockISpine(gomock.NewController(GinkgoT())) })

	It("proposes the fail-closed action and leaves the reason empty when a spine accepts it", func() {
		spine.EXPECT().Submit(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, action *pb.Action) error {
			Expect(action.Fallback).To(BeTrue())
			Expect(action.Acceleration).To(Equal(fallbackAcceleration))
			Expect(action.Sequence).To(Equal(uint64(9)))
			return nil
		})
		response := answerSpineRequest(context.Background(), spine, step())
		Expect(response.Error).To(BeEmpty())
		Expect(response.Action.Reason).To(BeEmpty())
		Expect(response.Epoch).To(Equal(uint64(2)))
	})

	It("reports a spine transport failure as an error, not as a missing spine", func() {
		spine.EXPECT().Submit(gomock.Any(), gomock.Any()).Return(errors.New("ros bridge closed"))
		response := answerSpineRequest(context.Background(), spine, step())
		Expect(response.Action).To(BeNil())
		Expect(response.Error).To(Equal("ros bridge closed"))
	})

	It("never reaches the spine for a step without an observation or a program request", func() {
		spine.EXPECT().Submit(gomock.Any(), gomock.Any()).Times(0)
		missing := answerSpineRequest(context.Background(), spine, &pb.RuntimeRequest{Kind: pb.RequestKind_REQUEST_KIND_STEP})
		Expect(missing.Error).To(Equal("observation is required"))
		for _, kind := range []pb.RequestKind{pb.RequestKind_REQUEST_KIND_UNSPECIFIED, pb.RequestKind_REQUEST_KIND_COMPILE, pb.RequestKind_REQUEST_KIND_ACTIVATE, pb.RequestKind_REQUEST_KIND_RESET, pb.RequestKind_REQUEST_KIND_EVALUATE} {
			response := answerSpineRequest(context.Background(), spine, &pb.RuntimeRequest{Kind: kind})
			Expect(response.Error).To(Equal(noSpineProgramError), kind.String())
			Expect(response.Program).To(BeNil())
		}
	})
})

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

	It("reports no spine connected in the snapshot the binary's service serves", func() {
		service, err := csf.New(csf.WithSpine(ros.NewDisconnectedSpine()))
		Expect(err).NotTo(HaveOccurred())
		response, err := service.GetSnapshot(context.Background(), &pb.GetSnapshotRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Snapshot.Issues).To(ContainElement(ros.NotConnectedStatus))
	})

	It("answers each JSONL step with the fail-closed action and no spine connected", func() {
		step, err := protojson.Marshal(&pb.RuntimeRequest{
			Kind:        pb.RequestKind_REQUEST_KIND_STEP,
			Observation: &pb.Observation{Epoch: 1, Sequence: 7, Tick: 1, Features: []int64{0, 0, 0, 0}},
			Tick:        1,
		})
		Expect(err).NotTo(HaveOccurred())
		compile, err := protojson.Marshal(&pb.RuntimeRequest{Kind: pb.RequestKind_REQUEST_KIND_COMPILE})
		Expect(err).NotTo(HaveOccurred())
		input := &bytes.Buffer{}
		for _, line := range [][]byte{[]byte("not JSON"), step, compile} {
			input.Write(append(line, '\n'))
		}
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
		Expect(fallback.Error).To(BeEmpty())
		Expect(fallback.Action.Fallback).To(BeTrue())
		Expect(fallback.Action.Sequence).To(Equal(uint64(7)))
		Expect(fallback.Action.Reason).To(Equal(ros.NotConnectedReason))
		Expect(scanner.Scan()).To(BeTrue())
		program := &pb.RuntimeResponse{}
		Expect(protojson.Unmarshal(scanner.Bytes(), program)).To(Succeed())
		Expect(program.Program).To(BeNil())
		Expect(program.Error).To(HavePrefix(ros.NotConnectedStatus))
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
