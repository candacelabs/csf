// Copyright 2026 Candace Labs

package relay_test

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/net/model/copilot"
	ionet "github.com/candacelabs/csf/io/net"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/relay"
)

const (
	alpha      relay.AgentID = "alpha"
	beta       relay.AgentID = "beta"
	reviewer   relay.AgentID = "reviewer"
	question                 = "can you review the scope change?"
	answer                   = "yes; it joins every inbox"
	socketPath               = "/run/csf/agents.sock"
	procFDs                  = "/proc/self/fd"
	socketLink               = "socket:"
)

// readyBudget bounds how long a spec waits for a host runtime to start its
// services; it is generous because a loaded CI machine starts slowly.
var readyBudget = eventually.Budget{Within: 10 * time.Second}

// inboxIdle is short so an inbox retires inside a spec; heldBudget watches a
// busy inbox across many of those timeouts to show it does not.
const inboxIdle = 20 * time.Millisecond

var heldBudget = eventually.Budget{Within: 15 * inboxIdle, Interval: inboxIdle / 4}

// note is a typed message whose pointer field shows an in-process handoff is
// by value: the receiver gets the sender's pointer, not a decoded copy.
type note struct {
	Text      string
	Reference *string
}

func inProcessAddress(session string) copilot.InProcessAddress {
	address, err := copilot.NewInProcessAddress(uuid.MustParse(session))
	Expect(err).NotTo(HaveOccurred())
	return address
}

// socketCount reads how many sockets this process holds, or -1 where the
// kernel does not expose its descriptor table.
func socketCount() int {
	entries, err := os.ReadDir(procFDs)
	if err != nil {
		return -1
	}
	sockets := 0
	for _, entry := range entries {
		target, err := os.Readlink(procFDs + "/" + entry.Name())
		if err == nil && strings.HasPrefix(target, socketLink) {
			sockets++
		}
	}
	return sockets
}

func startRelay[Body any](options ...relay.Option) (*relay.Relay[Body], *runtime.Scope) {
	core, err := relay.NewRelay[Body](options...)
	Expect(err).NotTo(HaveOccurred())
	scope := runtime.NewScope(context.Background(), "relay spec")
	Expect(core.Start(scope)).To(Succeed())
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
	return core, scope
}

var _ = Describe("Relay", func() {
	BeforeEach(func() {
		baseline := goleak.IgnoreCurrent()
		// Registered first, so it runs after every cleanup a spec defers —
		// including the scope that owns the relay's goroutines.
		DeferCleanup(func() {
			Expect(goleak.Find(baseline)).To(Succeed(), "every inbox goroutine must join when the relay's scope ends")
		})
	})

	It("exchanges in-process envelopes between two resident agents with zero sockets", func(ctx SpecContext) {
		// The binary's socket capabilities are a double that fails the spec
		// on any listen or dial. The relay is granted nothing at all: no
		// constructor option takes a capability, so the in-process route
		// cannot reach the kernel even if it tried.
		controller := gomock.NewController(GinkgoT())
		var listener ionet.IListener = NewMockIListener(controller)
		var dialer ionet.IDialer = NewMockIDialer(controller)
		Expect(listener).NotTo(BeNil())
		Expect(dialer).NotTo(BeNil())
		socketsBefore := socketCount()

		registry := prometheus.NewRegistry()
		core, err := relay.NewRelay[note](relay.WithMetrics(registry))
		Expect(err).NotTo(HaveOccurred())
		messenger, err := relay.NewMessenger[note](core)
		Expect(err).NotTo(HaveOccurred())
		alphaAddress := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a01")
		betaAddress := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a02")

		replies := make(chan relay.Envelope[note], 1)
		host, err := runtime.NewHostRuntime(runtime.WithHostName("resident spec"))
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Mount("relay", core)).To(Succeed())
		Expect(host.Mount("agents", runtime.ServiceFunc(func(scope *runtime.Scope) error {
			for _, registration := range []relay.Registration{{Agent: alpha, Address: alphaAddress}, {Agent: beta, Address: betaAddress}} {
				if err := core.Register(scope.Context(), registration); err != nil {
					return err
				}
			}
			// Beta's agent loop: answer the first message, then wait out
			// the scope. Alpha's loop forwards its reply to the spec.
			if err := scope.GoOwner("beta", func(ctx context.Context) error {
				received, err := messenger.Receive(ctx, beta)
				if err != nil {
					return err
				}
				reference := answer
				_, err = messenger.Send(ctx, received.To, received.From, note{Text: answer, Reference: &reference})
				if err != nil {
					return err
				}
				<-ctx.Done()
				return nil
			}); err != nil {
				return err
			}
			return scope.GoOwner("alpha", func(ctx context.Context) error {
				received, err := messenger.Receive(ctx, alpha)
				if err != nil {
					return err
				}
				replies <- received
				<-ctx.Done()
				return nil
			})
		}))).To(Succeed())

		owner := runtime.NewScope(ctx, "spec")
		Expect(owner.Go(host.Run)).To(Succeed())
		eventually.Await(GinkgoT(), "the resident host runtime to be ready", readyBudget, host.Ready, func(ready bool) bool { return ready })

		reference := question
		sent, err := messenger.Send(ctx, alphaAddress, betaAddress, note{Text: question, Reference: &reference})
		Expect(err).NotTo(HaveOccurred())
		Expect(sent.Tier).To(Equal(io.TierInProcess))
		Expect(sent.Body.Reference).To(BeIdenticalTo(&reference), "an in-process envelope is handed over, not encoded")

		var reply relay.Envelope[note]
		Eventually(replies).WithContext(ctx).Should(Receive(&reply))
		Expect(reply.Body.Text).To(Equal(answer))
		Expect(reply.FromAgent).To(Equal(beta))
		Expect(reply.ToAgent).To(Equal(alpha))
		Expect(reply.Tier).To(Equal(io.TierInProcess))
		Expect(owner.Close()).To(Succeed())

		Expect(sentCounter(registry, io.TierInProcess)).To(Equal(2.0))
		Expect(deliveredCounter(registry, io.TierInProcess)).To(Equal(2.0))
		for _, tier := range []io.Tier{io.TierIpc, io.TierNet} {
			Expect(sentCounter(registry, tier)).To(BeZero(), tier.String())
		}
		if socketsBefore >= 0 {
			Expect(socketCount()).To(Equal(socketsBefore), "no socket was opened by the exchange")
		}
	})

	It("never commits a registration whose inbox could not be opened", func() {
		// The registry double commits the registration and cancels the
		// caller's context in the same step, so any inbox work left after the
		// registry write fails. Whatever Register returns, an agent that
		// resolves must have an inbox.
		memory := relay.NewMemoryRegistry()
		registry := NewMockIRegistry(gomock.NewController(GinkgoT()))
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		registry.EXPECT().Register(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, registration relay.Registration) error {
				cancel()
				return memory.Register(context.Background(), registration)
			}).AnyTimes()
		registry.EXPECT().Resolve(gomock.Any(), gomock.Any()).DoAndReturn(memory.Resolve).AnyTimes()
		core, _ := startRelay[string](relay.WithRegistry(registry))
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())

		_ = core.Register(ctx, relay.Registration{Agent: reviewer, Address: inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a07")})
		if _, resolveErr := core.Resolve(context.Background(), reviewer); resolveErr == nil {
			_, fetchErr := messenger.Fetch(context.Background(), reviewer, 1)
			Expect(fetchErr).NotTo(HaveOccurred(), "a registered agent must have an inbox")
		}
	})

	It("records the widest tier between a host-tier sender and an in-process recipient", func(ctx SpecContext) {
		registry := prometheus.NewRegistry()
		core, _ := startRelay[string](relay.WithMetrics(registry))
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())
		claude, err := claudecode.NewHostAddress(uuid.New(), "unix:"+socketPath)
		Expect(err).NotTo(HaveOccurred())
		resident := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a03")
		Expect(core.Register(ctx, relay.Registration{Agent: reviewer, Address: claude})).To(Succeed())
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: resident})).To(Succeed())

		sent, err := messenger.Send(ctx, claude, resident, question)
		Expect(err).NotTo(HaveOccurred())
		Expect(sent.Tier).To(Equal(io.TierIpc))
		received, err := messenger.Receive(ctx, alpha)
		Expect(err).NotTo(HaveOccurred())
		Expect(received.Body).To(Equal(question))
		Expect(sentCounter(registry, io.TierIpc)).To(Equal(1.0))
		Expect(sentCounter(registry, io.TierInProcess)).To(BeZero())
	})

	It("redelivers fetched envelopes until they are acknowledged", func(ctx SpecContext) {
		registry := prometheus.NewRegistry()
		core, _ := startRelay[string](relay.WithMetrics(registry))
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())
		claude, err := claudecode.NewHostAddress(uuid.New(), "127.0.0.1:14111")
		Expect(err).NotTo(HaveOccurred())
		resident := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a04")
		Expect(core.Register(ctx, relay.Registration{Agent: reviewer, Address: claude})).To(Succeed())
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: resident})).To(Succeed())
		first, err := messenger.Send(ctx, resident, claude, question)
		Expect(err).NotTo(HaveOccurred())
		_, err = messenger.Send(ctx, resident, claude, answer)
		Expect(err).NotTo(HaveOccurred())

		fetched, err := messenger.Fetch(ctx, reviewer, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetched).To(HaveLen(1))
		Expect(fetched[0].ID).To(Equal(first.ID))
		again, err := messenger.Fetch(ctx, reviewer, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(HaveLen(2), "an unacknowledged envelope is fetched again")

		removed, err := messenger.Acknowledge(ctx, reviewer, []uuid.UUID{first.ID, first.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(Equal(1))
		remaining, err := messenger.Fetch(ctx, reviewer, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(remaining).To(HaveLen(1))
		Expect(remaining[0].Body).To(Equal(answer))
		Expect(deliveredCounter(registry, io.TierIpc)).To(Equal(1.0))
	})

	It("keeps an agent's inbox across re-registration and retires its old address", func(ctx SpecContext) {
		core, _ := startRelay[string]()
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())
		sender := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a05")
		before, err := claudecode.NewHostAddress(uuid.New(), socketPath)
		Expect(err).NotTo(HaveOccurred())
		after, err := claudecode.NewHostAddress(uuid.New(), socketPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: sender})).To(Succeed())
		Expect(core.Register(ctx, relay.Registration{Agent: reviewer, Address: before})).To(Succeed())
		_, err = messenger.Send(ctx, sender, before, question)
		Expect(err).NotTo(HaveOccurred())

		Expect(core.Register(ctx, relay.Registration{Agent: reviewer, Address: after})).To(Succeed())

		_, err = messenger.Send(ctx, sender, before, answer)
		Expect(err).To(MatchError(relay.ErrUnknownAddress), "the session that restarted no longer receives")
		queued, err := messenger.Fetch(ctx, reviewer, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(queued).To(HaveLen(1))
		Expect(queued[0].Body).To(Equal(question))
		Expect(queued[0].To).To(Equal(before), "the envelope records the address it was sent to")
		registration, err := core.Resolve(ctx, reviewer)
		Expect(err).NotTo(HaveOccurred())
		Expect(registration.Address).To(Equal(after))
		Expect(registration.Kind()).To(Equal(io.TierIpc))
	})

	It("refuses an address held by another agent and a sender that holds none", func(ctx SpecContext) {
		core, _ := startRelay[string]()
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())
		shared := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a06")
		stranger := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a07")
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: shared})).To(Succeed())

		Expect(core.Register(ctx, relay.Registration{Agent: beta, Address: shared})).To(MatchError(relay.ErrAddressTaken))
		_, err = messenger.Send(ctx, stranger, shared, question)
		Expect(err).To(MatchError(relay.ErrUnknownAddress))
		Expect(core.Register(ctx, relay.Registration{Agent: "Not An Agent", Address: stranger})).To(MatchError(relay.ErrInvalidRegistration))
		Expect(core.Register(ctx, relay.Registration{Agent: beta})).To(MatchError(relay.ErrInvalidRegistration))
	})

	It("returns an envelope to the queue when its receiver gives up", func(ctx SpecContext) {
		core, _ := startRelay[string]()
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())
		address := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a08")
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: address})).To(Succeed())

		abandoned, cancel := context.WithCancel(ctx)
		cancel()
		_, err = messenger.Receive(abandoned, alpha)
		Expect(err).To(MatchError(context.Canceled))
		_, err = messenger.Send(ctx, address, address, question)
		Expect(err).NotTo(HaveOccurred())
		received, err := messenger.Receive(ctx, alpha)
		Expect(err).NotTo(HaveOccurred())
		Expect(received.Body).To(Equal(question))
	})

	It("retires an idle inbox's goroutines, keeps a busy one running, and loses nothing", func(ctx SpecContext) {
		_, err := relay.NewRelay[string](relay.WithInboxIdleTimeout(0))
		Expect(err).To(MatchError(ContainSubstring("positive duration")))
		core, scope := startRelay[string](relay.WithInboxIdleTimeout(inboxIdle))
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())
		address := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a0b")
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: address})).To(Succeed())
		resident := scope.Live()
		running := func(count int64) bool { return count > resident }
		retired := func(count int64) bool { return count == resident }

		_, err = messenger.Send(ctx, address, address, question)
		Expect(err).NotTo(HaveOccurred())
		eventually.Consistently(GinkgoT(), "an inbox holding a queued message to keep its goroutines", heldBudget, scope.Live, running)
		received, err := messenger.Receive(ctx, alpha)
		Expect(err).NotTo(HaveOccurred())
		Expect(received.Body).To(Equal(question), "the queued message survived every idle timeout")
		eventually.Await(GinkgoT(), "the drained inbox's goroutines to join", readyBudget, scope.Live, retired)

		replies := make(chan relay.Envelope[string], 1)
		Expect(scope.Go(func(ctx context.Context) error {
			reply, err := messenger.Receive(ctx, alpha)
			replies <- reply
			return err
		})).To(Succeed())
		eventually.Consistently(GinkgoT(), "an inbox with a waiting receiver to keep its goroutines", heldBudget, scope.Live, running)
		_, err = messenger.Send(ctx, address, address, answer)
		Expect(err).NotTo(HaveOccurred())
		Expect((<-replies).Body).To(Equal(answer), "the restarted inbox delivers to the waiting receiver")
		eventually.Await(GinkgoT(), "the inbox to retire again", readyBudget, scope.Live, retired)
	})

	It("is a runtime service: unusable before Start and stopped after its scope ends", func(ctx SpecContext) {
		core, err := relay.NewRelay[string]()
		Expect(err).NotTo(HaveOccurred())
		address := inProcessAddress("0b0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a0a")
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: address})).To(MatchError(relay.ErrNotStarted))

		scope := runtime.NewScope(ctx, "relay lifecycle spec")
		Expect(core.Start(scope)).To(Succeed())
		Expect(core.Start(scope)).NotTo(Succeed())
		Expect(core.Register(ctx, relay.Registration{Agent: alpha, Address: address})).To(Succeed())
		messenger, err := relay.NewMessenger[string](core)
		Expect(err).NotTo(HaveOccurred())
		Expect(scope.Close()).To(Succeed())

		_, err = messenger.Receive(ctx, alpha)
		Expect(err).To(MatchError(relay.ErrStopped))
	})
})

func sentCounter(registry *prometheus.Registry, tier io.Tier) float64 {
	return counterSeries(registry, relay.MetricEnvelopesSent, tier)
}

func deliveredCounter(registry *prometheus.Registry, tier io.Tier) float64 {
	return counterSeries(registry, relay.MetricEnvelopesDelivered, tier)
}

// counterSeries reads one tier's value from the registry the binary would
// scrape, so the spec checks the exported series rather than an internal.
func counterSeries(registry *prometheus.Registry, name string, tier io.Tier) float64 {
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
	Fail("series " + name + "{tier=" + tier.String() + "} is not exported")
	return 0
}
