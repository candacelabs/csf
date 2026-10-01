package copilotbridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

var _ = Describe("model capability translation", func() {
	DescribeTable("maps every pinned SDK support-flag combination into the OpenAPI enum",
		func(supports copilot.ModelSupports, expected []api.ModelCapability) {
			capabilities := supportedCapabilities(rpc.ModelCapabilities{Supports: &rpc.ModelCapabilitiesSupports{Vision: &supports.Vision, ReasoningEffort: &supports.ReasoningEffort}})

			Expect(capabilities).To(Equal(expected))
			for _, capability := range capabilities {
				Expect(capability.Valid()).To(BeTrue(), "unsupported public capability %q", capability)
			}
		},
		Entry("no optional support", copilot.ModelSupports{}, []api.ModelCapability{}),
		Entry("vision", copilot.ModelSupports{Vision: true}, []api.ModelCapability{api.Vision}),
		Entry("reasoning effort", copilot.ModelSupports{ReasoningEffort: true}, []api.ModelCapability{api.Reasoning}),
		Entry("vision and reasoning effort", copilot.ModelSupports{Vision: true, ReasoningEffort: true}, []api.ModelCapability{api.Vision, api.Reasoning}),
	)
})

var _ = Describe("session resume classification", func() {
	It("selects MCP transports from the durable session identity", func() {
		identifier := uuid.New()
		observed := copilotadapter.BridgeSessionSpec{}
		selected := map[string]copilot.MCPServerConfig{
			"csf": copilot.MCPHTTPServerConfig{URL: "http://runtime/mcp"},
		}
		bridge := &CopilotBridge{
			mcpServers: map[string]copilot.MCPServerConfig{
				"fallback": copilot.MCPHTTPServerConfig{URL: "http://fallback/mcp"},
			},
			resolveMCPServers: func(_ context.Context, spec copilotadapter.BridgeSessionSpec) (map[string]copilot.MCPServerConfig, error) {
				observed = spec
				return selected, nil
			},
		}

		servers, err := bridge.mcpServersFor(context.Background(), copilotadapter.BridgeSessionSpec{
			SessionID: identifier, AgentID: "repository-maintainer",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(observed.SessionID).To(Equal(identifier))
		Expect(observed.AgentID).To(Equal("repository-maintainer"))
		Expect(servers).To(Equal(selected))
		Expect(servers).NotTo(HaveKey("fallback"))
	})

	It("rejects an unavailable session-specific MCP configuration", func() {
		resolverErr := errors.New("agent configuration not found")
		bridge := &CopilotBridge{
			resolveMCPServers: func(_ context.Context, _ copilotadapter.BridgeSessionSpec) (map[string]copilot.MCPServerConfig, error) {
				return nil, resolverErr
			},
		}

		_, err := bridge.mcpServersFor(context.Background(), copilotadapter.BridgeSessionSpec{SessionID: uuid.New()})

		Expect(err).To(MatchError(ContainSubstring("agent configuration not found")))
	})

	It("classifies only a metadata-confirmed missing session", func() {
		identifier := uuid.New()
		var resumeCalls atomic.Int32
		bridge := &CopilotBridge{
			getSessionMetadata: func(_ context.Context, _ string) (*copilot.SessionMetadata, error) {
				return nil, nil
			},
			resumeSession: func(_ context.Context, _ string, _ *copilot.ResumeSessionConfig) (*copilot.Session, error) {
				resumeCalls.Add(1)
				return nil, errors.New("must not resume")
			},
		}

		_, err := bridge.ResumeSession(context.Background(), copilotadapter.BridgeSessionSpec{SessionID: identifier})

		Expect(errors.Is(err, copilotadapter.ErrBridgeSessionMissing)).To(BeTrue())
		Expect(resumeCalls.Load()).To(BeZero())
	})

	It("keeps a metadata lookup failure retryable", func() {
		dependencyErr := errors.New("metadata transport unavailable")
		bridge := &CopilotBridge{
			getSessionMetadata: func(_ context.Context, _ string) (*copilot.SessionMetadata, error) {
				return nil, dependencyErr
			},
		}

		_, err := bridge.ResumeSession(context.Background(), copilotadapter.BridgeSessionSpec{SessionID: uuid.New()})

		Expect(errors.Is(err, dependencyErr)).To(BeTrue())
		Expect(errors.Is(err, copilotadapter.ErrBridgeSessionMissing)).To(BeFalse())
	})

	It("classifies a deletion race after resume fails", func() {
		identifier := uuid.New()
		resumeErr := errors.New("session.resume failed")
		var metadataCalls atomic.Int32
		bridge := &CopilotBridge{
			getSessionMetadata: func(_ context.Context, _ string) (*copilot.SessionMetadata, error) {
				if metadataCalls.Add(1) == 1 {
					return &copilot.SessionMetadata{SessionID: identifier.String()}, nil
				}
				return nil, nil
			},
			resumeSession: func(_ context.Context, _ string, _ *copilot.ResumeSessionConfig) (*copilot.Session, error) {
				return nil, resumeErr
			},
		}

		_, err := bridge.ResumeSession(context.Background(), copilotadapter.BridgeSessionSpec{SessionID: identifier})

		Expect(errors.Is(err, resumeErr)).To(BeTrue())
		Expect(errors.Is(err, copilotadapter.ErrBridgeSessionMissing)).To(BeTrue())
		Expect(metadataCalls.Load()).To(Equal(int32(2)))
	})

	It("does not classify a resume failure while metadata still exists", func() {
		identifier := uuid.New()
		resumeErr := errors.New("resume temporarily unavailable")
		var observed *copilot.ResumeSessionConfig
		spec := copilotadapter.BridgeSessionSpec{
			SessionID: identifier, Model: "gpt-5", WorkingDirectory: "/workspace/project",
			SystemInstructions: "Follow the repository contract.",
		}
		bridge := &CopilotBridge{
			mcpServers: map[string]copilot.MCPServerConfig{"csf": copilot.MCPHTTPServerConfig{URL: "http://localhost/mcp"}},
			getSessionMetadata: func(_ context.Context, _ string) (*copilot.SessionMetadata, error) {
				return &copilot.SessionMetadata{SessionID: identifier.String()}, nil
			},
			resumeSession: func(_ context.Context, _ string, config *copilot.ResumeSessionConfig) (*copilot.Session, error) {
				observed = config
				return nil, resumeErr
			},
		}

		_, err := bridge.ResumeSession(context.Background(), spec)

		Expect(errors.Is(err, resumeErr)).To(BeTrue())
		Expect(errors.Is(err, copilotadapter.ErrBridgeSessionMissing)).To(BeFalse())
		Expect(observed).NotTo(BeNil())
		Expect(observed.Model).To(Equal(spec.Model))
		Expect(observed.WorkingDirectory).To(Equal(spec.WorkingDirectory))
		Expect(observed.Streaming).NotTo(BeNil())
		Expect(*observed.Streaming).To(BeTrue())
		Expect(observed.SystemMessage).To(Equal(&copilot.SystemMessageConfig{Mode: systemMessageModeAppend, Content: spec.SystemInstructions}))
		Expect(observed.ContinuePendingWork).NotTo(BeNil())
		Expect(*observed.ContinuePendingWork).To(BeFalse())
		Expect(observed.OnPermissionRequest).NotTo(BeNil())
		Expect(observed.OnEvent).NotTo(BeNil())
		Expect(observed.MCPServers).To(HaveKey("csf"))
	})
})

var _ = Describe("session SDK configuration", func() {
	It("builds a new session configuration from the adapter specification", func() {
		spec := copilotadapter.BridgeSessionSpec{
			SessionID: uuid.New(), Model: "gpt-5", WorkingDirectory: "/workspace/project",
			SystemInstructions: "Follow the repository contract.",
		}
		bridge := &CopilotBridge{mcpServers: map[string]copilot.MCPServerConfig{
			"csf": copilot.MCPHTTPServerConfig{URL: "http://localhost/mcp"},
		}}
		lifecycle := newSessionLifecycle(spec.SessionID, newTurnCorrelator())
		DeferCleanup(lifecycle.stop)

		configuration := bridge.createSessionConfig(spec, bridge.mcpServers, lifecycle)

		Expect(configuration.SessionID).To(Equal(spec.SessionID.String()))
		Expect(configuration.Model).To(Equal(spec.Model))
		Expect(configuration.WorkingDirectory).To(Equal(spec.WorkingDirectory))
		Expect(configuration.Streaming).NotTo(BeNil())
		Expect(*configuration.Streaming).To(BeTrue())
		Expect(configuration.SystemMessage).To(Equal(&copilot.SystemMessageConfig{Mode: systemMessageModeAppend, Content: spec.SystemInstructions}))
		Expect(configuration.OnPermissionRequest).NotTo(BeNil())
		Expect(configuration.OnEvent).NotTo(BeNil())
		Expect(configuration.MCPServers).To(HaveKey("csf"))
	})
})

var permissionToolCallIDFixture = "call-a"

var _ = Describe("permission event subscription", func() {
	DescribeTable("extracts the tool call id from every SDK permission variant",
		func(request copilot.PermissionRequest) {
			Expect(permissionToolCallID(request)).To(Equal(copilotadapter.ToolCallID(permissionToolCallIDFixture)))
		},
		Entry("custom tool", &copilot.PermissionRequestCustomTool{ToolCallID: &permissionToolCallIDFixture}),
		Entry("extension management", &copilot.PermissionRequestExtensionManagement{ToolCallID: &permissionToolCallIDFixture}),
		Entry("extension permission access", &copilot.PermissionRequestExtensionPermissionAccess{ToolCallID: &permissionToolCallIDFixture}),
		Entry("factory", &copilot.PermissionRequestFactory{ToolCallID: &permissionToolCallIDFixture}),
		Entry("hook", &copilot.PermissionRequestHook{ToolCallID: &permissionToolCallIDFixture}),
		Entry("MCP", &copilot.PermissionRequestMCP{ToolCallID: &permissionToolCallIDFixture}),
		Entry("memory", &copilot.PermissionRequestMemory{ToolCallID: &permissionToolCallIDFixture}),
		Entry("read", &copilot.PermissionRequestRead{ToolCallID: &permissionToolCallIDFixture}),
		Entry("shell pointer", &copilot.PermissionRequestShell{ToolCallID: &permissionToolCallIDFixture}),
		Entry("shell value", copilot.PermissionRequestShell{ToolCallID: &permissionToolCallIDFixture}),
		Entry("URL", &copilot.PermissionRequestURL{ToolCallID: &permissionToolCallIDFixture}),
		Entry("write", &copilot.PermissionRequestWrite{ToolCallID: &permissionToolCallIDFixture}),
		Entry("future raw variant", &copilot.RawPermissionRequest{
			Discriminator: copilot.PermissionRequestKind("future"),
			Raw:           json.RawMessage(`{"kind":"future","toolCallId":"call-a"}`),
		}),
	)

	It("subscribes without resolving the permission callback", func() {
		decision, err := keepPermissionPending(nil, copilot.PermissionInvocation{})

		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionNoResult{}))
	})
})

var _ = Describe("session permission policy", func() {
	It("keeps ask and provider-managed restrictions pending", func() {
		read := &copilot.PermissionRequestRead{Path: "/tmp/a"}
		for _, policy := range []copilotadapter.PermissionPolicy{
			{Mode: api.Ask},
			{Mode: api.ApproveAll},
		} {
			decision, err := permissionHandlerFor(policy)(read, copilot.PermissionInvocation{ManagedSettingsEnabled: policy.Mode == api.ApproveAll})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionNoResult{}))
		}
	})

	It("uses the provider auto-approval handler when approveAll is permitted", func() {
		decision, err := permissionHandlerFor(copilotadapter.PermissionPolicy{Mode: api.ApproveAll})(
			&copilot.PermissionRequestRead{Path: "/tmp/a"},
			copilot.PermissionInvocation{},
		)

		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionApproveOnce{}))
	})

	It("approves only trusted exact names and complete shell commands in allowlist mode", func() {
		policy := copilotadapter.PermissionPolicy{
			Mode: api.Allowlist, ToolAllowlist: []string{"deploy"}, ShellAllowlist: []string{"go test ./..."},
		}
		handler := permissionHandlerFor(policy)
		fixtures := []struct {
			name    string
			request copilot.PermissionRequest
			approve bool
		}{
			{name: "exact MCP tool", request: &copilot.PermissionRequestMCP{ToolName: "deploy"}, approve: true},
			{name: "case changed MCP tool", request: &copilot.PermissionRequestMCP{ToolName: "Deploy"}},
			{name: "custom tool", request: &copilot.PermissionRequestCustomTool{ToolName: "deploy"}, approve: true},
			{name: "hook tool", request: &copilot.PermissionRequestHook{ToolName: "deploy"}, approve: true},
			{name: "native read has no trusted name", request: &copilot.PermissionRequestRead{Path: "/tmp/a"}},
			{name: "native write has no trusted name", request: &copilot.PermissionRequestWrite{FileName: "/tmp/a"}},
			{name: "complete shell command including slash", request: &copilot.PermissionRequestShell{FullCommandText: "go test ./..."}, approve: true},
			{name: "shell command prefix is insufficient", request: &copilot.PermissionRequestShell{FullCommandText: "go test ./pkg/..."}},
		}
		for _, fixture := range fixtures {
			By(fixture.name)
			decision, err := handler(fixture.request, copilot.PermissionInvocation{})
			Expect(err).NotTo(HaveOccurred())
			if fixture.approve {
				Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionApproveOnce{}))
			} else {
				Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionNoResult{}))
			}
		}
	})

	It("uses slash-sensitive path.Match shell globs against the full command", func() {
		policy := copilotadapter.PermissionPolicy{Mode: api.Allowlist, ShellAllowlist: []string{"go test *"}}
		decision, err := permissionHandlerFor(policy)(&copilot.PermissionRequestShell{FullCommandText: "go test ./..."}, copilot.PermissionInvocation{})
		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionNoResult{}))
	})
})

var _ = Describe("bounded SDK shutdown", func() {
	It("invokes one tracked disconnect across deadline-bound retries", func() {
		tracker := newDisconnectTracker()
		release := make(chan struct{})
		var invocations atomic.Int32
		operation := newDisconnectOperation(tracker, func() error {
			invocations.Add(1)
			<-release
			return nil
		})
		callContext, cancelCall := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancelCall()

		Expect(operation.wait(callContext)).To(MatchError(context.DeadlineExceeded))
		Expect(invocations.Load()).To(Equal(int32(1)))
		close(release)
		Eventually(func() int { return tracker.activeCount() }).Should(BeZero())
		Expect(operation.wait(context.Background())).To(Succeed())
		Expect(invocations.Load()).To(Equal(int32(1)))
	})

	It("force-stops the process and returns when a tracked SDK worker cannot drain", func() {
		tracker := newDisconnectTracker()
		release := make(chan struct{})
		operation := newDisconnectOperation(tracker, func() error {
			<-release
			return nil
		})
		started := make(chan error, 1)
		callContext, cancelCall := context.WithTimeout(context.Background(), time.Second)
		defer cancelCall()
		go func() { started <- operation.wait(callContext) }()
		Eventually(func() int { return tracker.activeCount() }).Should(Equal(1))
		var forceStops atomic.Int32
		bridge := &CopilotBridge{
			shutdownTimeout: 20 * time.Millisecond,
			forceStop:       func() { forceStops.Add(1) },
			disconnects:     tracker,
		}

		err := bridge.Close()

		Expect(err).To(MatchError(And(
			ContainSubstring("1 SDK shutdown worker(s) did not drain"),
			ContainSubstring(context.DeadlineExceeded.Error()),
		)))
		Expect(forceStops.Load()).To(Equal(int32(1)))
		Expect(bridge.Close()).To(MatchError(err))
		close(release)
		Eventually(started).Should(Receive(Succeed()))
		Expect(tracker.activeCount()).To(BeZero())
	})

	It("bounds an SDK ForceStop call that does not return", func() {
		tracker := newDisconnectTracker()
		release := make(chan struct{})
		bridge := &CopilotBridge{
			shutdownTimeout: 20 * time.Millisecond,
			forceStop:       func() { <-release },
			disconnects:     tracker,
		}

		err := bridge.Close()

		Expect(err).To(MatchError(ContainSubstring("1 SDK shutdown worker(s) did not drain")))
		Expect(tracker.activeCount()).To(Equal(1))
		close(release)
		Eventually(func() int { return tracker.activeCount() }).Should(BeZero())
	})

	It("rejects a disconnect first requested after process shutdown starts", func() {
		tracker := newDisconnectTracker()
		tracker.stop()
		operation := newDisconnectOperation(tracker, func() error {
			return errors.New("must not run")
		})

		Expect(operation.wait(context.Background())).To(MatchError(errBridgeClosing))
		Expect(tracker.activeCount()).To(BeZero())
	})
})

var _ = Describe("live model catalog", func() {
	It("observes an updated catalog after an early Auto-only response", func() {
		calls := 0
		bridge := &CopilotBridge{listModels: func(ctx context.Context, params *rpc.ModelsListRequest) (*rpc.ModelList, error) {
			calls++
			if calls == 1 {
				return &rpc.ModelList{Models: []rpc.Model{{ID: "auto", Name: "Auto"}}}, nil
			}
			return &rpc.ModelList{Models: []rpc.Model{{ID: "model-current", Name: "Current model"}}}, nil
		}}
		first, err := bridge.ListModels(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(first[0].ID).To(Equal("auto"))
		refreshed, err := bridge.ListModels(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(refreshed[0].ID).To(Equal("model-current"))
		Expect(refreshed[0].Capabilities).To(BeEmpty())
	})
	It("reports catalog failures instead of inventing fallback options", func() {
		unavailable := errors.New("catalog unavailable")
		bridge := &CopilotBridge{listModels: func(ctx context.Context, params *rpc.ModelsListRequest) (*rpc.ModelList, error) {
			return nil, unavailable
		}}
		models, err := bridge.ListModels(context.Background())
		Expect(err).To(MatchError(unavailable))
		Expect(models).To(BeNil())
	})
})
