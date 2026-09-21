package opencode

import (
	"context"
	"encoding/json"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	opencodesdk "github.com/sst/opencode-sdk-go"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// Live-contract budgets are generous on purpose. This spec talks to an external
// OpenCode server; a tight wall-clock budget flakes red under CI load (observed
// on CandaceOS acceptance, 2026-09-03) — the CS-9 pattern the house style gate
// does not yet reach under services/. Widen, never race. Native Ginkgo
// Eventually is kept: this is a Gomega suite where matcher composition earns it.
const (
	liveContractRequestTimeout = 30 * time.Second
	liveContractStreamBudget   = 30 * time.Second
	liveContractSpecTimeout    = 3 * time.Minute
)

var _ = Describe("OpenCode SDK live contract", func() {
	It("connects v0.19.2 to the pinned 1.18.21 server", func(ctx SpecContext) {
		endpoint := os.Getenv("CANDACEOS_OPENCODE_CONTRACT_URL")
		if endpoint == "" {
			Skip("set CANDACEOS_OPENCODE_CONTRACT_URL to run the pinned live-server contract")
		}
		password := os.Getenv("CANDACEOS_OPENCODE_CONTRACT_PASSWORD")
		Expect(password).NotTo(BeEmpty(), "CANDACEOS_OPENCODE_CONTRACT_PASSWORD is required")
		username := os.Getenv("CANDACEOS_OPENCODE_CONTRACT_USERNAME")
		if username == "" {
			username = "opencode"
		}

		adapter, err := newSDKAdapter(&candaceosv1.OpenCodeConfig{
			Url: endpoint, Username: username, Password: password,
			RequestTimeout: int64(liveContractRequestTimeout),
		}, "/workspace", nil)
		Expect(err).NotTo(HaveOccurred())

		healthy, version, err := adapter.health(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(healthy).To(BeTrue())
		Expect(version).To(Equal(PinnedServerVersion))
		assertEventStreamContract(ctx, adapter)

		session, err := adapter.createSession(ctx, "OpenCode SDK contract")
		Expect(err).NotTo(HaveOccurred())
		Expect(session.ID).NotTo(BeEmpty())
		Expect(session.Directory).To(Equal("/workspace"))
		Expect(session.Version).To(Equal(PinnedServerVersion))
		read, err := adapter.session(ctx, session.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(read.ID).To(Equal(session.ID))
		status, err := adapter.status(ctx, session.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal(phaseIdle))

		messageID := newMessageID()
		Expect(adapter.promptAsync(
			ctx,
			session.ID,
			messageID,
			"SDK contract probe",
			"Reply briefly.",
			promptModel{ProviderID: "openrouter", ModelID: "openai/gpt-5.4-nano"},
		)).To(Succeed())
		assertGeneratedMessageUnions(ctx, adapter, session.ID, messageID)
		Expect(adapter.abort(ctx, session.ID)).To(Succeed())
	}, SpecTimeout(liveContractSpecTimeout))
})

func assertEventStreamContract(ctx context.Context, adapter *sdkAdapter) {
	GinkgoHelper()
	streamContext, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan json.RawMessage, 1)
	done := make(chan error, 1)
	go func() {
		done <- adapter.streamEvents(streamContext, func(event json.RawMessage) {
			select {
			case events <- event:
				cancel()
			default:
			}
		})
	}()
	var event json.RawMessage
	Eventually(events).WithContext(ctx).WithTimeout(liveContractStreamBudget).Should(Receive(&event))
	var envelope struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	Expect(json.Unmarshal(event, &envelope)).To(Succeed())
	Expect(envelope.ID).NotTo(BeEmpty())
	Expect(envelope.Type).To(Equal(string(opencodesdk.EventListResponseTypeServerConnected)))
	Eventually(done).WithContext(ctx).WithTimeout(liveContractStreamBudget).Should(Receive())
}

func assertGeneratedMessageUnions(
	ctx context.Context,
	adapter *sdkAdapter,
	sessionID, messageID string,
) {
	GinkgoHelper()
	Eventually(func(g Gomega) bool {
		messages, err := adapter.messages(ctx, sessionID)
		g.Expect(err).NotTo(HaveOccurred())
		for _, message := range messages {
			user, ok := message.Info.AsUnion().(opencodesdk.UserMessage)
			if !ok || user.ID != messageID {
				continue
			}
			for _, part := range message.Parts {
				text, ok := part.AsUnion().(opencodesdk.TextPart)
				if ok && text.Text == "SDK contract probe" {
					return true
				}
			}
			GinkgoWriter.Printf("generated message parts=%d raw=%s\n", len(message.Parts), message.JSON.RawJSON())
		}
		return false
	}).WithContext(ctx).WithTimeout(liveContractStreamBudget).WithPolling(50 * time.Millisecond).Should(BeTrue())
}
