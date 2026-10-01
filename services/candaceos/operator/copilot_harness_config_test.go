package operator

import (
	"encoding/json"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

var _ = Describe("Copilot client transport credentials", func() {
	const token = "ghp-fixture-token"

	It("keeps an external runtime credential off the client so NewClient does not panic", func() {
		cfg := &candaceosv1.CoreConfig{
			HarnessBackend:         candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI,
			Workspace:              "/workspace",
			CopilotCli:             "/usr/local/bin/copilot",
			CopilotUrl:             "http://copilot:4321",
			CopilotConnectionToken: "connection-token",
			GithubToken:            token,
		}

		options := copilotClientOptions(cfg)
		Expect(options.GitHubToken).To(BeEmpty())
		Expect(options.Connection).To(Equal(copilot.URIConnection{
			URL: "http://copilot:4321", ConnectionToken: "connection-token",
		}))
		Expect(func() { copilot.NewClient(options) }).NotTo(Panic())
		Expect(copilotSessionGitHubToken(cfg)).To(Equal(token))

		// The SDK rejects a client-level credential on this transport by
		// panicking, so the routing above is the only safe shape.
		rejected := *options
		rejected.GitHubToken = token
		Expect(func() { copilot.NewClient(&rejected) }).To(PanicWith(
			ContainSubstring("cannot be used with URIConnection"),
		))
	})

	It("gives a spawned CLI the client credential and no per-session token", func() {
		cfg := &candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI,
			Workspace:      "/workspace",
			CopilotCli:     "/usr/local/bin/copilot",
			GithubToken:    token,
		}

		options := copilotClientOptions(cfg)
		Expect(options.GitHubToken).To(Equal(token))
		Expect(options.Connection).To(Equal(copilot.StdioConnection{Path: "/usr/local/bin/copilot"}))
		Expect(func() { copilot.NewClient(options) }).NotTo(Panic())
		Expect(copilotSessionGitHubToken(cfg)).To(BeEmpty())
	})

	It("carries the credential into both the create and resume session configs", func() {
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend:         candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI,
			Workspace:              "/workspace",
			CopilotUrl:             "http://copilot:4321",
			CopilotConnectionToken: "connection-token",
			GithubToken:            token,
		}, nil, nil)
		harness := testCopilotHarness(controller)

		Expect(harness.sessionConfig().GitHubToken).To(Equal(token))
		Expect(harness.resumeConfig().GitHubToken).To(Equal(token))
	})

	It("re-supplies the managed policy on resume so the injected layer is not cleared", func() {
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI,
			Workspace:      "/workspace",
			CopilotCli:     "/usr/local/bin/copilot",
		}, nil, nil)
		harness := testCopilotHarness(controller)

		Expect(harness.resumeConfig().ManagedSettings).To(Equal(harness.sessionConfig().ManagedSettings))
		Expect(harness.resumeConfig().ManagedSettings).NotTo(BeNil())
		Expect(harness.resumeConfig().ContinuePendingWork).To(Equal(copilot.Bool(false)))
	})
})

var _ = Describe("Copilot event projection", func() {
	It("reports a projection panic as a session error instead of losing the turn", func(ctx SpecContext) {
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO,
		}, nil, nil)
		harness := testCopilotHarness(controller)
		controller.OnRunStatus = func(runID, status string, at time.Time) { panic("durable run status failed") }
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		_, err := controller.Send(ctx, "start a turn", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		Expect(func() {
			harness.project(eventRecord{
				ID: "idle-1", Type: eventKindSessionIdle,
				Timestamp: time.Now().UTC(), Data: map[string]any{},
			})
		}).NotTo(Panic())

		Expect(controller.Run().Entries).To(ContainElement(SatisfyAll(
			HaveField("Kind", "error"),
			HaveField("Text", ContainSubstring("durable run status failed")),
			HaveField("Text", ContainSubstring("idle-1")),
		)))
	})
})

var _ = Describe("Copilot tool results", func() {
	It("encodes a protobuf result with the protobuf JSON mapping", func() {
		encoded, err := copilotToolResult(&candaceosv1.ReconcileEvidence{
			DeploymentId: "deployment-1", RevisionId: "revision-1",
			RunIds: []string{"run-1"}, NodeIds: []string{"node-1"},
			ReceiptIds: []int64{7}, DryRun: true,
		})
		Expect(err).NotTo(HaveOccurred())

		var decoded map[string]any
		Expect(json.Unmarshal([]byte(encoded), &decoded)).To(Succeed())
		Expect(decoded).To(HaveKeyWithValue("deployment_id", "deployment-1"))
		// Protobuf JSON spells 64-bit integers as strings; encoding/json would
		// emit a bare number and drift from every other Candace surface.
		Expect(decoded).To(HaveKeyWithValue("receipt_ids", []any{"7"}))
		Expect(decoded).To(HaveKeyWithValue("dry_run", true))
	})
})

var _ = Describe("Copilot prompt delivery", func() {
	DescribeTable("maps each delivery onto the runtime send mode",
		func(delivery candaceosv1.HarnessDelivery, expected string) {
			mode, err := copilotDeliveryMode(delivery)
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(expected))
		},
		// "immediate" is the runtime's active-turn steering injection, not an
		// abort and resubmit; "enqueue" defers to its own agentic loop.
		Entry("immediate steers the active turn",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE, "immediate"),
		Entry("enqueue waits behind the active turn",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE, "enqueue"),
	)

	It("refuses a delivery the runtime has no send mode for", func() {
		_, err := copilotDeliveryMode(candaceosv1.HarnessDelivery_HARNESS_DELIVERY_UNSPECIFIED)
		Expect(err).To(MatchError(ContainSubstring("unsupported Copilot prompt delivery")))
	})
})
