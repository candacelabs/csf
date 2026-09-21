package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OpenCode SDK adapter transport", func() {
	It("authenticates and workspace-scopes every request it makes", func(ctx SpecContext) {
		server := newWireServer()
		adapter := newWireAdapter(server)

		_, _, err := adapter.health(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = adapter.createSession(ctx, "contract")
		Expect(err).NotTo(HaveOccurred())
		_, err = adapter.session(ctx, fixtureSessionID)
		Expect(err).NotTo(HaveOccurred())
		_, _, _, err = adapter.rehydrate(ctx, fixtureSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(adapter.promptAsync(
			ctx, fixtureSessionID, "msg_wire", "wire prompt", "system rules", fixtureModel,
		)).To(Succeed())
		Expect(adapter.abort(ctx, fixtureSessionID)).To(Succeed())

		Expect(server.requestPaths()).NotTo(BeEmpty())
		Expect(server.authFailures()).To(BeZero(), "a request omitted its credentials")
		Expect(server.scopeFailures()).To(BeZero(), "a request omitted its workspace scope")
	})

	It("reads the health endpoint the pinned SDK does not generate", func(ctx SpecContext) {
		server := newWireServer()
		server.version = "9.9.9"
		adapter := newWireAdapter(server)

		healthy, version, err := adapter.health(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(healthy).To(BeTrue())
		Expect(version).To(Equal("9.9.9"))
		Expect(server.requestPaths()).To(ContainElement("/global/health"))
	})

	It("reads an absent session from the status endpoint as idle", func(ctx SpecContext) {
		server := newWireServer()
		adapter := newWireAdapter(server)

		phase, err := adapter.status(ctx, fixtureSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(phase).To(Equal(phaseIdle))
	})

	It("reads a running session's phase from the status endpoint", func(ctx SpecContext) {
		server := newWireServer()
		server.phases = []sessionPhase{phaseBusy}
		adapter := newWireAdapter(server)

		phase, err := adapter.status(ctx, fixtureSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(phase).To(Equal(phaseBusy))
	})

	It("brackets the transcript read with status reads and reports coherence", func(ctx SpecContext) {
		server := newWireServer()
		server.messages = []providerMessage{transcriptMessage(userMessage("msg_wire", "hello"))}
		adapter := newWireAdapter(server)

		messages, phase, coherent, err := adapter.rehydrate(ctx, fixtureSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(coherent).To(BeTrue())
		Expect(phase).To(Equal(phaseIdle))
		Expect(messages).To(HaveLen(1))
		Expect(partsText(messages[0].Parts)).To(Equal("hello"))
		Expect(server.requestPaths()).To(Equal([]string{
			"/session/status", "/session/" + fixtureSessionID + "/message", "/session/status",
		}))
	})

	It("reports an incoherent read when the phase moves across the transcript", func(ctx SpecContext) {
		server := newWireServer()
		server.phases = []sessionPhase{phaseBusy, phaseIdle}
		adapter := newWireAdapter(server)

		_, _, coherent, err := adapter.rehydrate(ctx, fixtureSessionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(coherent).To(BeFalse())
	})

	It("posts the async prompt endpoint the pinned SDK does not generate", func(ctx SpecContext) {
		server := newWireServer()
		adapter := newWireAdapter(server)

		Expect(adapter.promptAsync(
			ctx, fixtureSessionID, "msg_wire", "inspect the workspace", "system rules", fixtureModel,
		)).To(Succeed())

		Expect(server.requestPaths()).To(ContainElement("/session/" + fixtureSessionID + "/prompt_async"))
		bodies := server.submittedBodies()
		Expect(bodies).To(HaveLen(1))
		var submitted struct {
			MessageID string      `json:"messageID"`
			System    string      `json:"system"`
			Model     promptModel `json:"model"`
			Parts     []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
		}
		Expect(json.Unmarshal(bodies[0], &submitted)).To(Succeed())
		Expect(submitted.MessageID).To(Equal("msg_wire"))
		Expect(submitted.System).To(Equal("system rules"))
		Expect(submitted.Model).To(Equal(fixtureModel))
		Expect(submitted.Parts).To(HaveLen(1))
		Expect(submitted.Parts[0].Type).To(Equal("text"))
		Expect(submitted.Parts[0].Text).To(Equal("inspect the workspace"))
	})

	It("reports an abort the server declined to apply", func(ctx SpecContext) {
		server := newWireServer()
		server.abortApplied = false
		adapter := newWireAdapter(server)

		Expect(adapter.abort(ctx, fixtureSessionID)).To(MatchError(ErrAbortRejected))
	})

	It("surfaces the transport status when an abort fails", func(ctx SpecContext) {
		server := newWireServer()
		server.abortStatus = http.StatusInternalServerError
		adapter := newWireAdapter(server)

		Expect(adapter.abort(ctx, fixtureSessionID)).
			To(MatchError(ContainSubstring("500 Internal Server Error")))
	})

	It("decodes the event stream and reports a clean end of stream", func(ctx SpecContext) {
		server := newWireServer()
		adapter := newWireAdapter(server)
		received := make(chan json.RawMessage, 4)
		streamContext, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)

		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- adapter.streamEvents(streamContext, func(event json.RawMessage) {
				received <- event
			})
		}()
		server.publish(`{"type":"session.status","properties":{"sessionID":"` + fixtureSessionID + `"}}`)

		var event json.RawMessage
		Eventually(received).Should(Receive(&event))
		Expect(eventAppliesToSession(event, fixtureSessionID)).To(BeTrue())

		close(server.events)
		Eventually(done).Should(Receive(MatchError(io.EOF)))
	})

	It("stops streaming when its context ends", func(ctx SpecContext) {
		server := newWireServer()
		adapter := newWireAdapter(server)
		streamContext, cancel := context.WithCancel(ctx)

		done := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- adapter.streamEvents(streamContext, func(event json.RawMessage) {})
		}()
		cancel()

		var err error
		Eventually(done).Should(Receive(&err))
		Expect(err).To(HaveOccurred())
	})

	It("rejects a URL it cannot parse", func() {
		_, err := newSDKAdapter(nil, "/workspace", nil)
		Expect(err).To(MatchError(ErrConfigRequired))
	})
})

var _ = Describe("OpenCode event filtering", func() {
	// The pinned SDK's generated event union has no session.status or
	// message.part.delta variant, so the runtime reads the envelope itself.
	// These cases pin that hand-rolled decode.
	DescribeTable("decides whether a provider event invalidates the attached session",
		func(payload string, applies bool) {
			Expect(eventAppliesToSession(json.RawMessage(payload), fixtureSessionID)).To(Equal(applies))
		},
		Entry("a direct session property",
			`{"type":"session.status","properties":{"sessionID":"ses_exact"}}`, true),
		Entry("a message info property",
			`{"type":"message.updated","properties":{"info":{"sessionID":"ses_exact"}}}`, true),
		Entry("a message part property",
			`{"type":"message.part.delta","properties":{"part":{"sessionID":"ses_exact"}}}`, true),
		Entry("any server lifecycle event",
			`{"type":"server.connected","properties":{}}`, true),
		Entry("a session error with no properties",
			`{"type":"session.error"}`, true),
		Entry("another session's event",
			`{"type":"session.status","properties":{"sessionID":"ses_other"}}`, false),
		Entry("an event with no properties at all",
			`{"type":"message.updated"}`, false),
		Entry("a payload that is not an event", `not json`, false),
	)
})

var _ = Describe("OpenCode adapter construction", func() {
	It("rejects a malformed server URL", func() {
		_, err := newSDKAdapter(testConfigWithURL("://bad"), "/workspace", nil)
		Expect(err).To(HaveOccurred())
	})

	It("floors a non-positive request timeout so a call cannot expire instantly", func() {
		adapter, err := newSDKAdapter(testConfigWithURL("http://opencode.invalid"), "/workspace", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(adapter.requestTimeout).To(BeNumerically(">", time.Duration(0)))
	})

	It("reports a runtime built without a provider", func() {
		host, _ := newRecordingHost(nil)
		_, err := newRuntime(testConfig("", 2), "/workspace", host, nil)
		Expect(err).To(MatchError(ErrProviderRequired))
	})

	It("reports a runtime built without configuration", func() {
		host, _ := newRecordingHost(nil)
		_, err := newRuntime(nil, "/workspace", host, scriptedProvider(newProviderScript()))
		Expect(err).To(MatchError(ErrConfigRequired))
	})
})
