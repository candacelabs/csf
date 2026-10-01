package operator

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/fleet"
	harnesssdk "github.com/candacelabs/csf/services/candaceos/harness"
)

func newTestController(cfg *candaceosv1.CoreConfig, fleetClient *fleet.Client, reconciler IReconciler) *Controller {
	if cfg == nil {
		cfg = &candaceosv1.CoreConfig{}
	}
	if cfg.HarnessBackend == candaceosv1.HarnessBackend_HARNESS_BACKEND_UNSPECIFIED {
		cfg.HarnessBackend = candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO
	}
	controller, err := NewController(cfg, fleetClient, reconciler)
	Expect(err).NotTo(HaveOccurred())
	return controller
}

func testCopilotHarness(controller *Controller) *copilotHarness {
	harness := newCopilotHarness(controller.config, controller)
	DeferCleanup(harness.Close)
	return harness
}

var _ = Describe("agent harness selection", func() {
	It("preserves Copilot lifecycle sentinel errors", func() {
		Expect(copilotRunnerError(harnesssdk.ErrRunnerClosed)).To(Equal(errCopilotHarnessClosed))
		Expect(copilotRunnerError(harnesssdk.ErrRunnerStarted)).To(Equal(errCopilotHarnessStarted))
		Expect(copilotRunnerError(harnesssdk.ErrRuntimeUnavailable)).To(Equal(errCopilotSessionUnavailable))
		Expect(copilotRunnerError(context.Canceled)).To(Equal(context.Canceled))
	})

	It("constructs only the configured backend", func() {
		demo, err := configureHarness(&candaceosv1.CoreConfig{HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO}, &Controller{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(demo.runtime).To(BeAssignableToTypeOf(&demoHarness{}))
		Expect(demo.identity.GetCapabilities()).To(BeEmpty())

		cli, err := configureHarness(&candaceosv1.CoreConfig{HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI}, &Controller{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cli.runtime).To(BeAssignableToTypeOf(&copilotHarness{}))
		Expect(cli.identity.GetCapabilities()).To(ConsistOf(
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE,
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_RECONCILE,
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING,
		))
		DeferCleanup(cli.runtime.Close)

		ollama, err := configureHarness(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA,
			Ollama: &candaceosv1.OllamaConfig{
				Url: "http://127.0.0.1:11434", Model: "qwen3:8b",
				ModelDigest: strings.Repeat("a", 64), ContextTokens: 16384,
				MaxToolCalls: 16, TurnTimeout: int64(10 * time.Minute),
			},
		}, &Controller{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(ollama.runtime).To(BeAssignableToTypeOf(&ollamaHarness{}))
		Expect(ollama.identity.GetCapabilities()).To(ConsistOf(
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_RECONCILE,
		))
		DeferCleanup(ollama.runtime.Close)

		opencode, err := configureHarness(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE,
			Workspace:      "/workspace",
			Opencode: &candaceosv1.OpenCodeConfig{
				Url: "http://127.0.0.1:4096", Username: "opencode", Password: "secret",
				RequestTimeout: int64(10 * time.Second), PollInterval: int64(time.Second), QueueCapacity: 32,
				Model: "openrouter/openai/gpt-5.4-nano",
			},
		}, &Controller{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(opencode.runtime).NotTo(BeNil())
		Expect(opencode.identity.GetImplementation()).To(Equal("opencode"))
		Expect(opencode.identity.GetModel()).To(Equal("openrouter/openai/gpt-5.4-nano"))
		Expect(opencode.identity.GetCapabilities()).To(Equal([]candaceosv1.HarnessCapability{
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE,
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING,
		}))
		DeferCleanup(opencode.runtime.Close)

		unsupported, err := configureHarness(&candaceosv1.CoreConfig{HarnessBackend: candaceosv1.HarnessBackend(99)}, &Controller{}, nil)
		Expect(err).To(MatchError(ContainSubstring("unsupported agent harness backend")))
		Expect(unsupported.runtime).To(BeNil())
	})

	It("cancels delayed demo completion when closed", func(ctx SpecContext) {
		controller := newTestController(&candaceosv1.CoreConfig{HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO}, nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		_, err := controller.Send(ctx, "do not finish after close", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Expect(controller.Close()).To(Succeed())

		Consistently(func() []TimelineEntry { return controller.Run().Entries }, 120*time.Millisecond).
			ShouldNot(ContainElement(WithTransform(func(entry TimelineEntry) string { return entry.Role }, Equal("assistant"))))
		Expect(controller.Status()).To(Equal("stopped"))
	})

	It("ignores late harness events after the controller stops", func(ctx SpecContext) {
		controller := newTestController(&candaceosv1.CoreConfig{HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO}, nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		Expect(controller.Close()).To(Succeed())
		before := controller.Run().Entries

		controller.ingest(eventRecord{
			ID: "late-message", Type: "assistant.message", Timestamp: time.Now().UTC(),
			Data: map[string]any{"messageId": "late", "content": "too late"},
		})
		controller.ingest(eventRecord{
			ID: "late-idle", Type: "session.idle", Timestamp: time.Now().UTC(), Data: map[string]any{},
		})

		Expect(controller.Status()).To(Equal("stopped"))
		Expect(controller.Run().Entries).To(Equal(before))
	})

	It("cancels delayed demo completion when aborted", func(ctx SpecContext) {
		controller := newTestController(&candaceosv1.CoreConfig{HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO}, nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		_, err := controller.Send(ctx, "do not finish after abort", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Expect(controller.Abort(context.Background())).To(Succeed())

		Consistently(func() []TimelineEntry { return controller.Run().Entries }, 120*time.Millisecond).
			ShouldNot(ContainElement(WithTransform(func(entry TimelineEntry) string { return entry.Role }, Equal("assistant"))))
		Expect(controller.Run().Status).To(Equal("aborted"))
		Eventually(controller.Status).Should(Equal("idle"))
	})
})
