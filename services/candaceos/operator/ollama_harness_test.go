package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/fleet"
)

type ollamaTestAPI struct {
	mu           sync.Mutex
	capabilities []string
	modelDigest  string
	showStatus   int
	requests     []ollamaChatRequest
	chat         func(response http.ResponseWriter, request *http.Request, call int)
}

func (api *ollamaTestAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(response http.ResponseWriter, request *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{"version": "0.20.4"})
	})
	mux.HandleFunc("POST /api/show", func(response http.ResponseWriter, request *http.Request) {
		if api.showStatus != 0 {
			writeJSON(response, api.showStatus, map[string]string{"error": "model unavailable"})
			return
		}
		capabilities := api.capabilities
		if capabilities == nil {
			capabilities = []string{"completion", "tools", "thinking"}
		}
		writeJSON(response, http.StatusOK, map[string]any{"capabilities": capabilities})
	})
	mux.HandleFunc("GET /api/tags", func(response http.ResponseWriter, request *http.Request) {
		digest := api.modelDigest
		if digest == "" {
			digest = strings.Repeat("a", 64)
		}
		writeJSON(response, http.StatusOK, map[string]any{"models": []map[string]string{{
			"name": "qwen3:8b", "model": "qwen3:8b", "digest": digest,
		}}})
	})
	mux.HandleFunc("POST /api/chat", func(response http.ResponseWriter, request *http.Request) {
		var chatRequest ollamaChatRequest
		if err := json.NewDecoder(request.Body).Decode(&chatRequest); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		api.mu.Lock()
		call := len(api.requests)
		api.requests = append(api.requests, chatRequest)
		chat := api.chat
		api.mu.Unlock()
		if chat == nil {
			writeJSON(response, http.StatusInternalServerError, map[string]string{"error": "missing test chat behavior"})
			return
		}
		chat(response, request, call)
	})
	return mux
}

func (api *ollamaTestAPI) capturedRequests() []ollamaChatRequest {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]ollamaChatRequest(nil), api.requests...)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeOllamaStream(response http.ResponseWriter, chunks ...ollamaChatChunk) {
	response.Header().Set("Content-Type", "application/x-ndjson")
	encoder := json.NewEncoder(response)
	for _, chunk := range chunks {
		_ = encoder.Encode(chunk)
	}
}

func ollamaConfig(url string) *candaceosv1.CoreConfig {
	return &candaceosv1.CoreConfig{
		HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA,
		Workspace:       GinkgoT().TempDir(),
		ApprovalTimeout: int64(time.Minute),
		Ollama: &candaceosv1.OllamaConfig{
			Url: url, Model: "qwen3:8b", ModelDigest: strings.Repeat("a", 64),
			ContextTokens: 4096, MaxToolCalls: 4, TurnTimeout: int64(time.Minute),
		},
	}
}

func toolCall(id string, name string, arguments any) ollamaToolCall {
	encoded, err := json.Marshal(arguments)
	Expect(err).NotTo(HaveOccurred())
	index := 0
	return ollamaToolCall{ID: id, Type: "function", Function: ollamaToolFunction{Name: name, Arguments: encoded, Index: &index}}
}

func configuredFleetClient() *fleet.Client {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		writeJSON(response, http.StatusOK, map[string]any{
			"view": map[string]any{
				"self": "control", "role": "leader", "term": 7, "leader_id": "control", "authoritative": true,
				"updated_at": "2026-08-20T00:00:00Z",
				"membership": map[string]any{
					"version": 1, "created_in_term": 7,
					"voters": []map[string]string{{"id": "control", "addr": "10.0.0.1:7717"}},
				},
				"peers": []map[string]any{{
					"node":   map[string]string{"id": "control", "addr": "10.0.0.1:7717"},
					"status": "alive", "last_seen": "2026-08-20T00:00:00Z", "member": "voter",
				}},
			},
		})
	}))
	DeferCleanup(server.Close)
	client, err := fleet.NewWardenClient(server.URL, server.Client())
	Expect(err).NotTo(HaveOccurred())
	_, err = client.Refresh(context.Background())
	Expect(err).NotTo(HaveOccurred())
	return client
}

var _ = Describe("Ollama agent harness", func() {
	It("accumulates streamed tool calls and returns native results before the assistant answer", func(ctx SpecContext) {
		api := &ollamaTestAPI{}
		api.chat = func(response http.ResponseWriter, request *http.Request, call int) {
			switch call {
			case 0:
				writeOllamaStream(response,
					ollamaChatChunk{Message: ollamaMessage{Role: "assistant", Thinking: "I need fleet truth.", ToolCalls: []ollamaToolCall{
						toolCall("fleet-a", "candace_fleet_status", map[string]any{}),
					}}},
					ollamaChatChunk{Message: ollamaMessage{Role: "assistant", ToolCalls: []ollamaToolCall{
						toolCall("fleet-b", "candace_fleet_status", map[string]any{}),
					}}, Done: true},
				)
			default:
				writeOllamaStream(response,
					ollamaChatChunk{Message: ollamaMessage{Role: "assistant", Content: "Fleet "}},
					ollamaChatChunk{Message: ollamaMessage{Role: "assistant", Content: "ready."}, Done: true},
				)
			}
		}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		cfg := ollamaConfig(server.URL)
		cfg.NodeLabels = []*candaceosv1.Node{{Id: "control", Labels: map[string]string{"role": "control"}}}
		controller := newTestController(cfg, configuredFleetClient(), nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		runID, err := controller.Send(ctx, "Tell me whether the fleet is ready", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Expect(runID).NotTo(BeEmpty())
		Eventually(func() string { return controller.Run().Status }).Should(Equal("succeeded"))

		requests := api.capturedRequests()
		Expect(requests).To(HaveLen(2))
		Expect(requests[0].Options.ContextTokens).To(Equal(int32(4096)))
		Expect(requests[0].Tools).To(HaveLen(1))
		Expect(requests[0].Tools[0].Function.Name).To(Equal("candace_fleet_status"))
		Expect(requests[1].Messages).To(ContainElement(And(
			WithTransform(func(message ollamaMessage) string { return message.Role }, Equal("assistant")),
			WithTransform(func(message ollamaMessage) int { return len(message.ToolCalls) }, Equal(2)),
		)))
		assistantWithTools := requests[1].Messages[2]
		Expect(assistantWithTools.ToolCalls[0].Type).To(Equal("function"))
		Expect(assistantWithTools.ToolCalls[0].Function.Index).NotTo(BeNil())
		Expect(*assistantWithTools.ToolCalls[0].Function.Index).To(Equal(0))
		Expect(requests[1].Messages).To(HaveLen(5))
		Expect(requests[1].Messages[3].Role).To(Equal("tool"))
		Expect(requests[1].Messages[3].Content).To(ContainSubstring(`"leader_id":"control"`))

		entries := controller.Run().Entries
		Expect(entries).To(HaveLen(4))
		Expect(entries).To(ContainElement(And(
			WithTransform(func(entry TimelineEntry) string { return entry.Kind }, Equal("message")),
			WithTransform(func(entry TimelineEntry) string { return entry.Text }, Equal("Fleet ready.")),
		)))
		Expect(entries).To(ContainElements(
			WithTransform(func(entry TimelineEntry) string { return entry.Status }, Equal("complete")),
			WithTransform(func(entry TimelineEntry) string { return entry.Status }, Equal("complete")),
		))
	})

	It("routes reconcile through the existing one-time operator approval", func(ctx SpecContext) {
		input := reconcileToolInput{
			App: "status", Project: "status", Path: "apps/status",
			DesiredState: "running", PlacementMode: "leader",
		}
		api := &ollamaTestAPI{}
		api.chat = func(response http.ResponseWriter, request *http.Request, call int) {
			if call == 0 {
				writeOllamaStream(response, ollamaChatChunk{
					Message: ollamaMessage{Role: "assistant", ToolCalls: []ollamaToolCall{
						toolCall("reconcile-1", "candace_reconcile_app", input),
					}}, Done: true,
				})
				return
			}
			writeOllamaStream(response, ollamaChatChunk{
				Message: ollamaMessage{Role: "assistant", Content: "The approved revision was reconciled."}, Done: true,
			})
		}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		reconciler := &mutableReconciler{revision: testReconcileRevision("a", "1")}
		controller := newTestController(ollamaConfig(server.URL), nil, reconciler)
		requested := make(chan Approval, 1)
		controller.OnApprovalRequested = func(approval Approval) error {
			requested <- approval
			return nil
		}
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		_, err := controller.Send(ctx, "Deploy status", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		var approval Approval
		Eventually(requested).Should(Receive(&approval))
		Expect(approval.ToolCallID).To(Equal("reconcile-1"))
		Expect(approval.RequiresFleetQuorum).To(BeTrue())
		Eventually(func() bool {
			_, exists := controller.ApprovalQueue().Get(approval.ID)
			return exists
		}).Should(BeTrue())
		_, err = controller.ApprovalQueue().Resolve(approval.ID, DecisionApprove, ApprovalActorState().Operator)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string { return controller.Run().Status }).Should(Equal("succeeded"))
		Expect(reconciler.prepareCalls()).To(Equal(1))
		Expect(reconciler.reconcileCalls()).To(Equal(1))
		requests := api.capturedRequests()
		Expect(requests).To(HaveLen(2))
		Expect(requests[0].Tools).To(HaveLen(2))
		Expect(requests[1].Messages).To(ContainElement(And(
			WithTransform(func(message ollamaMessage) string { return message.Role }, Equal("tool")),
			WithTransform(func(message ollamaMessage) string { return message.Content }, ContainSubstring(`"revision_id":"status-`)),
		)))
	})

	It("marks an asynchronous model API failure on the run", func(ctx SpecContext) {
		api := &ollamaTestAPI{chat: func(response http.ResponseWriter, request *http.Request, call int) {
			writeJSON(response, http.StatusInternalServerError, map[string]string{"error": "GPU worker unavailable"})
		}}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		controller := newTestController(ollamaConfig(server.URL), nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		_, err := controller.Send(ctx, "answer this", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred(), "Send only starts the asynchronous turn")
		Eventually(func() string { return controller.Run().Status }).Should(Equal("failed"))
		Expect(controller.Run().Entries).To(ContainElement(And(
			WithTransform(func(entry TimelineEntry) string { return entry.Kind }, Equal("error")),
			WithTransform(func(entry TimelineEntry) string { return entry.Text }, ContainSubstring("GPU worker unavailable")),
		)))
	})

	It("accepts a new turn immediately when the prior terminal event advertises idle", func(ctx SpecContext) {
		api := &ollamaTestAPI{chat: func(response http.ResponseWriter, request *http.Request, call int) {
			writeOllamaStream(response, ollamaChatChunk{
				Message: ollamaMessage{Role: "assistant", Content: fmt.Sprintf("answer-%d", call)}, Done: true,
			})
		}}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		controller := newTestController(ollamaConfig(server.URL), nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		_, err := controller.Send(ctx, "first", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string { return controller.Run().Status }).Should(Equal("succeeded"))
		Eventually(controller.Status).Should(Equal("idle"))

		_, err = controller.Send(ctx, "second", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string { return controller.Run().Status }).Should(Equal("succeeded"))
		Expect(api.capturedRequests()).To(HaveLen(2))
	})

	It("rejects a selected model without native tool support at startup", func(ctx SpecContext) {
		api := &ollamaTestAPI{capabilities: []string{"completion"}}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		controller := newTestController(ollamaConfig(server.URL), nil, nil)
		DeferCleanup(controller.Close)

		Expect(controller.Start(ctx)).To(MatchError(ContainSubstring("does not advertise native tool support")))
	})

	It("cancels an in-flight HTTP stream when the operator aborts", func(ctx SpecContext) {
		started := make(chan struct{})
		canceled := make(chan struct{})
		var startedOnce sync.Once
		var canceledOnce sync.Once
		api := &ollamaTestAPI{chat: func(response http.ResponseWriter, request *http.Request, call int) {
			startedOnce.Do(func() { close(started) })
			<-request.Context().Done()
			canceledOnce.Do(func() { close(canceled) })
		}}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		controller := newTestController(ollamaConfig(server.URL), nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		_, err := controller.Send(ctx, "wait forever", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Eventually(started).Should(BeClosed())

		Expect(controller.Abort(context.Background())).To(Succeed())
		Eventually(canceled).Should(BeClosed())
		Expect(controller.Run().Status).To(Equal("aborted"))
		Eventually(controller.Status).Should(Equal("idle"))
		Expect(controller.Run().Entries).NotTo(ContainElement(
			WithTransform(func(entry TimelineEntry) string { return entry.Kind }, Equal("error")),
		))
	})

	It("cancels an in-flight HTTP stream when the harness closes", func(ctx SpecContext) {
		started := make(chan struct{})
		canceled := make(chan struct{})
		api := &ollamaTestAPI{chat: func(response http.ResponseWriter, request *http.Request, call int) {
			close(started)
			<-request.Context().Done()
			close(canceled)
		}}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		controller := newTestController(ollamaConfig(server.URL), nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		_, err := controller.Send(ctx, "wait forever", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Eventually(started).Should(BeClosed())

		Expect(controller.Close()).To(Succeed())
		Eventually(canceled).Should(BeClosed())
		Expect(controller.Status()).To(Equal("stopped"))
	})

	It("executes none when one response exceeds the total native tool-call bound", func(ctx SpecContext) {
		api := &ollamaTestAPI{}
		api.chat = func(response http.ResponseWriter, request *http.Request, call int) {
			writeOllamaStream(response, ollamaChatChunk{
				Message: ollamaMessage{Role: "assistant", ToolCalls: []ollamaToolCall{
					toolCall("fleet-0", "candace_fleet_status", map[string]any{}),
					toolCall("fleet-1", "candace_fleet_status", map[string]any{}),
				}}, Done: true,
			})
		}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		cfg := ollamaConfig(server.URL)
		cfg.Ollama.MaxToolCalls = 1
		controller := newTestController(cfg, configuredFleetClient(), nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		_, err := controller.Send(ctx, "loop", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string { return controller.Run().Status }).Should(Equal("failed"))
		Expect(api.capturedRequests()).To(HaveLen(1))
		entries := controller.Run().Entries
		Expect(entries).To(ContainElement(And(
			WithTransform(func(entry TimelineEntry) string { return entry.Kind }, Equal("error")),
			WithTransform(func(entry TimelineEntry) string { return entry.Text }, ContainSubstring("1 native-tool-call limit")),
		)))
		toolEntries := 0
		for _, entry := range entries {
			if entry.Kind == "tool" {
				toolEntries++
			}
		}
		Expect(toolEntries).To(BeZero())
	})

	It("fails a stalled turn at its configured deadline", func(ctx SpecContext) {
		canceled := make(chan struct{})
		api := &ollamaTestAPI{chat: func(response http.ResponseWriter, request *http.Request, call int) {
			<-request.Context().Done()
			close(canceled)
		}}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		cfg := ollamaConfig(server.URL)
		cfg.Ollama.TurnTimeout = int64(time.Second)
		controller := newTestController(cfg, nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		_, err := controller.Send(ctx, "stall", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		Eventually(canceled, 2*time.Second).Should(BeClosed())
		Eventually(func() string { return controller.Run().Status }, 2*time.Second).Should(Equal("failed"))
		Expect(controller.Run().Entries).To(ContainElement(And(
			WithTransform(func(entry TimelineEntry) string { return entry.Kind }, Equal("error")),
			WithTransform(func(entry TimelineEntry) string { return entry.Text }, ContainSubstring("deadline exceeded")),
		)))
	})

	It("surfaces model lookup failures before opening a session", func(ctx SpecContext) {
		api := &ollamaTestAPI{showStatus: http.StatusNotFound}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		controller := newTestController(ollamaConfig(server.URL), nil, nil)
		DeferCleanup(controller.Close)

		err := controller.Start(ctx)
		Expect(err).To(MatchError(And(
			ContainSubstring(`verifying Ollama model "qwen3:8b"`),
			ContainSubstring("model unavailable"),
		)))
	})

	It("rejects a mutable model tag whose digest no longer matches the resolved identity", func(ctx SpecContext) {
		api := &ollamaTestAPI{modelDigest: strings.Repeat("b", 64)}
		server := httptest.NewServer(api.handler())
		DeferCleanup(server.Close)
		controller := newTestController(ollamaConfig(server.URL), nil, nil)
		DeferCleanup(controller.Close)

		err := controller.Start(ctx)
		Expect(err).To(MatchError(And(
			ContainSubstring(`Ollama model "qwen3:8b" digest`),
			ContainSubstring(strings.Repeat("a", 64)),
			ContainSubstring(strings.Repeat("b", 64)),
		)))
	})
})

var _ = Describe("Ollama HTTP bounds", func() {
	It("rejects an oversized NDJSON stream", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			writeOllamaStream(response, ollamaChatChunk{
				Message: ollamaMessage{Role: "assistant", Content: strings.Repeat("x", 2048)}, Done: true,
			})
		}))
		DeferCleanup(server.Close)
		client := &ollamaClient{baseURL: server.URL, httpClient: server.Client(), streamByteCap: 1024}

		_, err := client.chat(ctx, ollamaChatRequest{
			Model: "qwen3:8b", Messages: []ollamaMessage{{Role: "user", Content: "hello"}},
		}, func(delta string) error { return nil })
		Expect(err).To(MatchError(ContainSubstring("chat stream exceeded 1024 bytes")))
	})

	It("rejects an oversized non-streaming response even after a valid JSON prefix", func(ctx SpecContext) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"version":"0.20.4"}`))
			_, _ = response.Write([]byte(strings.Repeat(" ", ollamaErrorResponseBytes+1)))
		}))
		DeferCleanup(server.Close)
		client := &ollamaClient{baseURL: server.URL, httpClient: server.Client(), streamByteCap: 1 << 20}
		var output map[string]any

		err := client.doJSON(ctx, http.MethodGet, "/api/version", nil, &output)
		Expect(err).To(MatchError(ContainSubstring("response exceeded")))
	})

	It("rejects an outbound transcript above its context-derived cap before sending", func(ctx SpecContext) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			requests++
		}))
		DeferCleanup(server.Close)
		client := &ollamaClient{baseURL: server.URL, httpClient: server.Client(), streamByteCap: 64}

		_, err := client.chat(ctx, ollamaChatRequest{
			Model: "qwen3:8b", Messages: []ollamaMessage{{Role: "user", Content: strings.Repeat("x", 128)}},
		}, func(delta string) error { return nil })
		Expect(err).To(MatchError(ContainSubstring("chat request exceeded 64 bytes")))
		Expect(requests).To(BeZero())
	})
})

var _ = Describe("Ollama tool schema", func() {
	It("exposes only fleet truth until a reconciler is installed", func() {
		withoutReconciler := ollamaTools(false)
		Expect(withoutReconciler).To(HaveLen(1))
		Expect(withoutReconciler[0].Function.Name).To(Equal("candace_fleet_status"))
		Expect(withoutReconciler[0].Function.Parameters).To(HaveKeyWithValue("additionalProperties", false))

		withReconciler := ollamaTools(true)
		Expect(withReconciler).To(HaveLen(2))
		Expect(withReconciler[1].Function.Name).To(Equal("candace_reconcile_app"))
		required := withReconciler[1].Function.Parameters["required"].([]string)
		Expect(strings.Join(required, ",")).To(Equal("app,project,path,desired_state,placement_mode"))
	})
})
