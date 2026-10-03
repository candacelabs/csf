// Copyright 2026 Candace Labs

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/pkg/httpserver"
	agentv1 "github.com/candacelabs/csf/proto/candace/agent/v1"
	"github.com/candacelabs/csf/runtime"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/adaptertest"
	"github.com/candacelabs/csf/services/copilot-adapter/copilotbridge"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/cron/crontest"
	"github.com/candacelabs/csf/services/relay"
)

const (
	messagingModel      = "gpt-5"
	messagingRepository = "candace"
	messagingRoot       = "/tmp/agent-messaging"
	messagingBase       = "http://workbench.invalid"
	messagingText       = "please review the relay's shutdown order"
	messagingAgentA     = "alpha"
	messagingAgentB     = "beta"
	agentKey            = "agent"
	toKey               = "to"
	textKey             = "text"
	messageKey          = "message"
	kindKey             = "kind"
)

// messagingBudget bounds the wait for the host runtime to start and for a
// delivery goroutine to queue a prompt.
var messagingBudget = eventually.Budget{Within: 10 * time.Second}

// inProcessDoer serves the generated client's requests by calling the router
// directly, so creating the spec's sessions opens no socket either.
type inProcessDoer struct{ handler http.Handler }

func (doer inProcessDoer) Do(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	doer.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func invokeTool(tool copilot.Tool, arguments map[string]any) copilot.ToolResult {
	GinkgoHelper()
	result, err := tool.Handler(copilot.ToolInvocation{ToolName: tool.Name, Arguments: arguments})
	Expect(err).NotTo(HaveOccurred())
	return result
}

var _ = Describe("Resident agent messaging between Workbench sessions", func() {
	BeforeEach(func() {
		baseline := goleak.IgnoreCurrent()
		DeferCleanup(func() {
			Expect(goleak.Find(baseline)).To(Succeed(), "delivery goroutines must join with the host runtime")
		})
	})

	It("queues one session's message as a prompt in another resident session, in-process", func(ctx SpecContext) {
		controller := gomock.NewController(GinkgoT())
		bridge := NewMockICopilotBridge(controller)
		worktrees := NewMockIWorktreeManager(controller)
		terminals := NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil).AnyTimes()
		persistence := adaptertest.OpenStore(GinkgoT())
		adapter, err := copilotadapter.NewCopilotAdapter(
			copilotadapter.WithBridge(bridge), copilotadapter.WithStore(persistence),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals),
			copilotadapter.WithScheduleStore(crontest.OpenStore(GinkgoT())),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(adapter.Close)
		router := httpserver.NewEngine("agent-messaging")
		Expect(adapter.Register(router)).To(Succeed())
		client, err := api.NewClientWithResponses(messagingBase, api.WithHTTPClient(inProcessDoer{handler: router}))
		Expect(err).NotTo(HaveOccurred())

		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: messagingRepository, Root: messagingRoot, DefaultRef: "HEAD"},
			Path:       messagingRoot, BaseRef: "HEAD",
		}
		bridge.EXPECT().ListModels(gomock.Any()).Return([]copilotadapter.BridgeModel{{ID: messagingModel}}, nil).AnyTimes()
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(prepared, nil).AnyTimes()
		worktrees.EXPECT().Reuse(gomock.Any(), gomock.Any(), gomock.Any()).Return(prepared, nil).AnyTimes()
		prompts := map[uuid.UUID]chan copilotadapter.BridgePrompt{}
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (copilotadapter.BridgeSession, error) {
				sent := make(chan copilotadapter.BridgePrompt, 4)
				prompts[spec.SessionID] = sent
				return copilotadapter.BridgeSession{
					Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
						sent <- prompt
						return copilotadapter.BridgePromptDeliveryAccepted, nil
					},
					Close: func(ctx context.Context) error { return nil },
				}, nil
			}).Times(2)
		sessions := make([]uuid.UUID, 0, 2)
		for range 2 {
			created, err := client.CreateSessionWithResponse(ctx, newWorktreeSessionBody(messagingModel, messagingRepository))
			Expect(err).NotTo(HaveOccurred())
			Expect(created.JSON201).NotTo(BeNil(), string(created.Body))
			sessions = append(sessions, *created.JSON201.Id)
		}

		registry := prometheus.NewRegistry()
		core, err := relay.NewRelay[*agentv1.AgentMessage](relay.WithMetrics(registry))
		Expect(err).NotTo(HaveOccurred())
		messaging, err := copilotadapter.NewSessionMessaging(adapter, core)
		Expect(err).NotTo(HaveOccurred())
		host, err := runtime.NewHostRuntime(runtime.WithHostName("workbench messaging spec"))
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("relay", core)).To(Succeed())
		Expect(host.Mount("session messaging", messaging)).To(Succeed())
		owner := runtime.NewScope(ctx, "spec")
		Expect(owner.Go(host.Run)).To(Succeed())
		DeferCleanup(func() { Expect(owner.Close()).To(Succeed()) })
		eventually.Await(GinkgoT(), "the host runtime to start the relay and session messaging", messagingBudget, host.Ready, func(ready bool) bool { return ready })

		// Each session's tools are bound to that session by the bridge's
		// resolver; the spec invokes them as the Copilot CLI would.
		alphaTools := copilotbridge.NewAgentMessagingTools(messaging, sessions[0])
		betaTools := copilotbridge.NewAgentMessagingTools(messaging, sessions[1])
		Expect(invokeTool(betaTools[0], map[string]any{agentKey: messagingAgentB}).TextResultForLLM).To(ContainSubstring("in_process tier"))
		Expect(invokeTool(alphaTools[0], map[string]any{agentKey: messagingAgentA}).TextResultForLLM).To(ContainSubstring(messagingAgentA))
		sent := invokeTool(alphaTools[1], map[string]any{toKey: messagingAgentB, messageKey: map[string]any{
			kindKey: int32(agentv1.AgentMessageKind_AGENT_MESSAGE_KIND_REQUEST), textKey: messagingText,
		}})
		Expect(sent.TextResultForLLM).To(ContainSubstring("in_process tier"))

		var prompt copilotadapter.BridgePrompt
		Eventually(prompts[sessions[1]]).WithTimeout(messagingBudget.Within).Should(Receive(&prompt))
		Expect(prompt.Text).To(ContainSubstring(messagingText))
		Expect(prompt.Text).To(ContainSubstring(`agent "alpha"`))
		Expect(prompt.Author).To(Equal(messagingAgentA))
		Expect(prompt.Mode).To(Equal(string(api.Queue)))
		Expect(prompts[sessions[0]]).To(BeEmpty(), "the sender's session receives nothing")

		sentInProcess := counterValue(registry, relay.MetricEnvelopesSent, ipc.TierInProcess)
		Expect(sentInProcess).To(Equal(1.0))
		Expect(counterValue(registry, relay.MetricEnvelopesDelivered, ipc.TierInProcess)).To(Equal(1.0))
		Expect(counterValue(registry, relay.MetricEnvelopesSent, ipc.TierHost)).To(BeZero())
		Expect(counterValue(registry, relay.MetricEnvelopesSent, ipc.TierNetwork)).To(BeZero())
	})
})

func counterValue(registry *prometheus.Registry, name string, tier ipc.Tier) float64 {
	GinkgoHelper()
	families, err := registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == relay.MetricTierLabel && label.GetValue() == tier.String() {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	Fail(name + " has no " + tier.String() + " series")
	return 0
}
