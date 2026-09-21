package agentclient_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/agentclient"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAgentClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "candaceos-core agent client suite")
}

var _ = Describe("Client", func() {
	It("authenticates both calls, derives the node endpoint, and uses canonical ProtoJSON", func() {
		var healthCalls atomic.Int32
		var assignmentCalls atomic.Int32
		request := validRequest()

		server := newIPv4Server(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer node-secret"))
			switch r.URL.Path {
			case "/healthz":
				healthCalls.Add(1)
				writeProto(w, &candaceosv1.HealthResponse{Status: "ok", NodeId: "node-a", DryRun: true})
			case "/v1/assignment":
				assignmentCalls.Add(1)
				Expect(r.Method).To(Equal(http.MethodPut))
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
				body, err := io.ReadAll(r.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(body)).To(ContainSubstring(`"term":"7"`))
				Expect(string(body)).To(ContainSubstring(`"leader_id":"warden-a"`))
				Expect(string(body)).To(ContainSubstring(`"desired_state":"DESIRED_STATE_RUNNING"`))
				Expect(string(body)).NotTo(ContainSubstring("leaderId"))

				var decoded candaceosv1.ReconcileRequest
				Expect((protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, &decoded)).To(Succeed())
				Expect(proto.Equal(&decoded, request)).To(BeTrue())
				writeProto(w, validResponse(request))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		port, err := serverPort(server)
		Expect(err).NotTo(HaveOccurred())
		client, err := agentclient.NewNodeAgentClient("", " node-secret ", port, server.Client())
		Expect(err).NotTo(HaveOccurred())

		result, err := client.Reconcile(context.Background(), "node-a", "127.0.0.1:7717", request)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(result.GetFence(), request.GetFence())).To(BeTrue())
		Expect(proto.Equal(result.GetAssignment(), request.GetAssignment())).To(BeTrue())
		Expect(healthCalls.Load()).To(Equal(int32(1)))
		Expect(assignmentCalls.Load()).To(Equal(int32(1)))
	})

	It("stops before mutation when the endpoint reports a different node identity", func() {
		var mutated atomic.Bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				writeProto(w, &candaceosv1.HealthResponse{Status: "ok", NodeId: "node-b"})
				return
			}
			mutated.Store(true)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		client, err := agentclient.NewNodeAgentClient(server.URL, "", 7718, server.Client())
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Reconcile(context.Background(), "node-a", "ignored:1234", validRequest())
		Expect(err).To(MatchError(ContainSubstring(`identity mismatch: Warden selected "node-a" but endpoint reports "node-b"`)))
		Expect(mutated.Load()).To(BeFalse())
	})

	DescribeTable("rejects invalid configuration before making a request",
		func(endpoint string, port int, expected string) {
			client, err := agentclient.NewNodeAgentClient(endpoint, "", port, nil)
			Expect(client).To(BeNil())
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("unsupported URL scheme", "ftp://node-a:7718", 7718, "invalid CandaceOS agent URL"),
		Entry("relative URL", "node-a:7718", 7718, "invalid CandaceOS agent URL"),
		Entry("zero port", "", 0, "invalid CandaceOS agent port 0"),
		Entry("oversized port", "", 65536, "invalid CandaceOS agent port 65536"),
	)

	It("rejects a Warden address that cannot identify a host", func() {
		client, err := agentclient.NewNodeAgentClient("", "", 7718, nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = client.Reconcile(context.Background(), "node-a", "not-a-host-port", validRequest())
		Expect(err).To(MatchError(ContainSubstring(`deriving agent endpoint from Warden address "not-a-host-port"`)))
	})

	DescribeTable("strictly validates successful reconciliation responses",
		func(response func(request *candaceosv1.ReconcileRequest) string, expected string) {
			request := validRequest()
			server := httptest.NewServer(agentHandler("node-a", func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response(request))
			}))
			defer server.Close()

			client, err := agentclient.NewNodeAgentClient(server.URL, "", 7718, server.Client())
			Expect(err).NotTo(HaveOccurred())
			_, err = client.Reconcile(context.Background(), "node-a", "ignored:1234", request)
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("unknown ProtoJSON field", func(request *candaceosv1.ReconcileRequest) string {
			body := marshalProto(validResponse(request))
			return strings.TrimSuffix(body, "}") + `,"surprise":true}`
		}, "unknown field"),
		Entry("different assignment", func(request *candaceosv1.ReconcileRequest) string {
			response := validResponse(request)
			response.Assignment = proto.Clone(request.GetAssignment()).(*candaceosv1.Assignment)
			response.Assignment.App = "other-app"
			return marshalProto(response)
		}, "different assignment or fence"),
		Entry("different fence", func(request *candaceosv1.ReconcileRequest) string {
			response := validResponse(request)
			response.Fence = proto.Clone(request.GetFence()).(*candaceosv1.Fence)
			response.Fence.Term++
			return marshalProto(response)
		}, "different assignment or fence"),
		Entry("missing completion timestamp", func(request *candaceosv1.ReconcileRequest) string {
			response := validResponse(request)
			response.UpdatedAt = nil
			return marshalProto(response)
		}, "no valid completion timestamp"),
	)

	It("bounds a successful health response", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, strings.Repeat("x", (1<<20)+1))
		}))
		defer server.Close()
		client, err := agentclient.NewNodeAgentClient(server.URL, "", 7718, server.Client())
		Expect(err).NotTo(HaveOccurred())

		_, err = client.Reconcile(context.Background(), "node-a", "ignored:1234", validRequest())
		Expect(err).To(MatchError(ContainSubstring("response exceeds 1048576 bytes")))
	})

	It("returns a bounded agent error without attempting to decode it as ProtoJSON", func() {
		server := httptest.NewServer(agentHandler("node-a", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, "stale election fence")
		}))
		defer server.Close()
		client, err := agentclient.NewNodeAgentClient(server.URL, "", 7718, server.Client())
		Expect(err).NotTo(HaveOccurred())

		_, err = client.Reconcile(context.Background(), "node-a", "ignored:1234", validRequest())
		Expect(err).To(MatchError("agent reconciliation returned 409 Conflict: stale election fence"))
	})

	It("does not echo an oversized upstream error body", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, strings.Repeat("sensitive", 150_000))
		}))
		defer server.Close()
		client, err := agentclient.NewNodeAgentClient(server.URL, "", 7718, server.Client())
		Expect(err).NotTo(HaveOccurred())

		_, err = client.Reconcile(context.Background(), "node-a", "ignored:1234", validRequest())
		Expect(err).To(MatchError("agent health returned 502 Bad Gateway"))
	})
})

func validRequest() *candaceosv1.ReconcileRequest {
	return &candaceosv1.ReconcileRequest{
		Fence: &candaceosv1.Fence{Term: 7, LeaderId: "warden-a"},
		Assignment: &candaceosv1.Assignment{
			App:          "notes",
			Project:      "candace-notes",
			Path:         "apps/notes",
			DesiredState: candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
		},
	}
}

func validResponse(request *candaceosv1.ReconcileRequest) *candaceosv1.ReconcileResponse {
	return &candaceosv1.ReconcileResponse{
		Fence:      proto.Clone(request.GetFence()).(*candaceosv1.Fence),
		Assignment: proto.Clone(request.GetAssignment()).(*candaceosv1.Assignment),
		DryRun:     true,
		Commands:   []*candaceosv1.Command{{Argv: []string{"docker", "compose", "config"}}},
		UpdatedAt:  timestamppb.Now(),
	}
}

func agentHandler(nodeID string, assignment func(response http.ResponseWriter)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			writeProto(w, &candaceosv1.HealthResponse{Status: "ok", NodeId: nodeID})
		case "/v1/assignment":
			assignment(w)
		default:
			http.NotFound(w, r)
		}
	})
}

func writeProto(w http.ResponseWriter, message proto.Message) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, marshalProto(message))
}

func marshalProto(message proto.Message) string {
	body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(message)
	Expect(err).NotTo(HaveOccurred())
	return string(body)
}

func newIPv4Server(handler http.Handler) *httptest.Server {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	return server
}

func serverPort(server *httptest.Server) (int, error) {
	_, rawPort, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		return 0, fmt.Errorf("split test server address: %w", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		return 0, fmt.Errorf("parse test server port: %w", err)
	}
	return port, nil
}
