// Copyright 2026 Candace Labs

package relaytest

import (
	"context"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/relay"
)

const (
	conformanceSender    relay.AgentID = "conformance-sender"
	conformanceRecipient relay.AgentID = "conformance-recipient"
	conformanceStranger  relay.AgentID = "conformance-stranger"
	conformanceFetchMax                = 8
)

// relayFixture is one started relay over a fresh registry, with a sender and
// a recipient registered, and messages to send.
type relayFixture[Body any] struct {
	messenger *relay.Messenger[Body]
	core      *relay.Relay[Body]
	newBody   func(sequence int) Body
}

// relayConformanceSpec is one messaging requirement: a name and a check run
// against a fresh fixture.
type relayConformanceSpec[Body any] struct {
	name  string
	check func(ctx context.Context, fixture relayFixture[Body])
}

// relayConformanceSpecs is the messaging contract, in data, for one Body.
func relayConformanceSpecs[Body any]() []relayConformanceSpec[Body] {
	return []relayConformanceSpec[Body]{
		{"hands an in-process recipient exactly the body that was sent", handsOverBody[Body]},
		{"redelivers fetched envelopes until they are acknowledged", redeliversUntilAcknowledged[Body]},
		{"refuses a sender that holds no registered address", refusesUnregisteredSender[Body]},
	}
}

// DescribeRelayConformance registers the messaging conformance specs for a
// relay carrying Body over the registries newRegistry builds. newBody makes
// distinct messages of the relay's contract; the specs compare what arrives
// with gomega.Equal, so Body must compare by value or be a type gomega's
// equality understands (protobuf messages included). Call it at package level
// in a Ginkgo suite:
//
//	var _ = relaytest.DescribeRelayConformance("Relay[string]", relay.NewMemoryRegistry,
//		func(sequence int) string { return fmt.Sprint("message ", sequence) })
func DescribeRelayConformance[Body any, Registry relay.IRegistry](name string, newRegistry func() Registry, newBody func(sequence int) Body) bool {
	return ginkgo.Describe(name+" conforms to the relay messaging contract", func() {
		var fixture relayFixture[Body]
		ginkgo.BeforeEach(func() {
			core, err := relay.NewRelay[Body](relay.WithRegistry(newRegistry()))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			scope := runtime.NewScope(context.Background(), name+" conformance")
			gomega.Expect(core.Start(scope)).To(gomega.Succeed())
			ginkgo.DeferCleanup(func() { gomega.Expect(scope.Close()).To(gomega.Succeed()) })
			messenger, err := relay.NewMessenger[Body](core)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			fixture = relayFixture[Body]{messenger: messenger, core: core, newBody: newBody}
		})
		for _, spec := range relayConformanceSpecs[Body]() {
			ginkgo.It(spec.name, func(ctx ginkgo.SpecContext) { spec.check(ctx, fixture) })
		}
	})
}

func register[Body any](ctx context.Context, core *relay.Relay[Body], agent relay.AgentID) relay.Registration {
	registration := relay.Registration{Agent: agent, Address: inProcessAddress()}
	gomega.Expect(core.Register(ctx, registration)).To(gomega.Succeed())
	return registration
}

func handsOverBody[Body any](ctx context.Context, fixture relayFixture[Body]) {
	sender := register(ctx, fixture.core, conformanceSender)
	recipient := register(ctx, fixture.core, conformanceRecipient)
	body := fixture.newBody(1)

	sent, err := fixture.messenger.Send(ctx, sender.Address, recipient.Address, body)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	received, err := fixture.messenger.Receive(ctx, conformanceRecipient)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(received.ID).To(gomega.Equal(sent.ID))
	gomega.Expect(received.FromAgent).To(gomega.Equal(conformanceSender))
	gomega.Expect(received.Body).To(gomega.Equal(body))
}

func redeliversUntilAcknowledged[Body any](ctx context.Context, fixture relayFixture[Body]) {
	sender := register(ctx, fixture.core, conformanceSender)
	recipient := register(ctx, fixture.core, conformanceRecipient)
	first, second := fixture.newBody(1), fixture.newBody(2)
	_, err := fixture.messenger.Send(ctx, sender.Address, recipient.Address, first)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	_, err = fixture.messenger.Send(ctx, sender.Address, recipient.Address, second)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	fetched, err := fixture.messenger.Fetch(ctx, conformanceRecipient, conformanceFetchMax)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(fetched).To(gomega.HaveLen(2))
	gomega.Expect(fetched[0].Body).To(gomega.Equal(first), "oldest first")
	again, err := fixture.messenger.Fetch(ctx, conformanceRecipient, conformanceFetchMax)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(again).To(gomega.HaveLen(2), "a fetch does not consume")

	removed, err := fixture.messenger.Acknowledge(ctx, conformanceRecipient, []uuid.UUID{fetched[0].ID, fetched[0].ID})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(removed).To(gomega.Equal(1))
	left, err := fixture.messenger.Fetch(ctx, conformanceRecipient, conformanceFetchMax)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(left).To(gomega.HaveLen(1))
	gomega.Expect(left[0].Body).To(gomega.Equal(second))
}

func refusesUnregisteredSender[Body any](ctx context.Context, fixture relayFixture[Body]) {
	recipient := register(ctx, fixture.core, conformanceRecipient)
	_, err := fixture.messenger.Send(ctx, inProcessAddress(), recipient.Address, fixture.newBody(1))
	gomega.Expect(err).To(gomega.MatchError(relay.ErrUnknownAddress))
	_, err = fixture.messenger.Fetch(ctx, conformanceStranger, conformanceFetchMax)
	gomega.Expect(err).To(gomega.MatchError(relay.ErrUnknownAgent))
}
