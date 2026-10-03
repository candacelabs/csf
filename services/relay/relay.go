// Copyright 2026 Candace Labs

package relay

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/ipc"
	"github.com/candacelabs/csf/ipc/model"
	"github.com/candacelabs/csf/pkg/mailbox"
	"github.com/candacelabs/csf/runtime"
)

const (
	directoryOwner = "relay directory"
	inboxOwner     = "relay inbox "
	// defaultInboxIdle is how long an inbox with nothing queued and nobody
	// waiting keeps its goroutine before the relay retires it.
	defaultInboxIdle = time.Minute
)

// Option configures a [Relay] before [NewRelay] builds anything.
type Option func(configuration *relayConfiguration) error

type relayConfiguration struct {
	registry   IRegistry
	registerer prometheus.Registerer
	now        func() time.Time
	inboxIdle  time.Duration
}

// WithRegistry supplies the registration store. Optional; the default is a
// [MemoryRegistry].
func WithRegistry(registry IRegistry) Option {
	return func(configuration *relayConfiguration) error {
		if registry == nil {
			return errors.New("relay: WithRegistry needs a registry")
		}
		configuration.registry = registry
		return nil
	}
}

// WithMetrics registers the per-tier envelope counters with registerer.
func WithMetrics(registerer prometheus.Registerer) Option {
	return func(configuration *relayConfiguration) error {
		if registerer == nil {
			return errors.New("relay: WithMetrics needs a registerer")
		}
		configuration.registerer = registerer
		return nil
	}
}

// WithClock replaces the clock that stamps envelopes.
func WithClock(now func() time.Time) Option {
	return func(configuration *relayConfiguration) error {
		if now == nil {
			return errors.New("relay: WithClock needs a clock")
		}
		configuration.now = now
		return nil
	}
}

// WithInboxIdleTimeout is how long an inbox with nothing queued, no receiver
// waiting and no call in progress keeps its goroutine. The inbox and
// everything it holds outlive the goroutine; the next message starts it
// again. Optional; the default is one minute.
func WithInboxIdleTimeout(idle time.Duration) Option {
	return func(configuration *relayConfiguration) error {
		if idle <= 0 {
			return fmt.Errorf("relay: WithInboxIdleTimeout needs a positive duration, got %s", idle)
		}
		configuration.inboxIdle = idle
		return nil
	}
}

// lazyInbox is an inbox that runs only while it is in use.
type lazyInbox[Body any] = runtime.LazyService[*inbox[Body]]

// directoryState maps each registered agent to its inbox. It is owned by the
// directory goroutine.
type directoryState[Body any] struct {
	inboxes map[AgentID]*lazyInbox[Body]
	scope   *runtime.Scope
}

// Relay is the core every [Messenger] shares, typed by the one message
// contract it carries: Body is the type every sender, inbox and receiver on
// this relay agrees on, so nothing is erased or asserted between them. It is
// a runtime service: mount it into the host runtime before anything that
// sends.
type Relay[Body any] struct {
	registry  IRegistry
	metrics   *relayMetrics
	now       func() time.Time
	inboxIdle time.Duration
	directory *mailbox.Mailbox[directoryState[Body]]
	started   atomic.Bool
}

var _ runtime.IService = (*Relay[string])(nil)

// NewRelay validates its options and builds a relay that has not started.
func NewRelay[Body any](options ...Option) (*Relay[Body], error) {
	configuration := relayConfiguration{now: time.Now, inboxIdle: defaultInboxIdle}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("relay: option %d is nil", index)
		}
		if err := option(&configuration); err != nil {
			return nil, err
		}
	}
	if configuration.registry == nil {
		configuration.registry = NewMemoryRegistry()
	}
	metrics, err := newRelayMetrics(configuration.registerer)
	if err != nil {
		return nil, err
	}
	return &Relay[Body]{
		registry:  configuration.registry,
		metrics:   metrics,
		now:       configuration.now,
		inboxIdle: configuration.inboxIdle,
		directory: mailbox.New[directoryState[Body]](),
	}, nil
}

// Start runs the directory on the relay's scope, and a stopper that retires
// the directory when the scope is canceled. Each inbox is a lazy service
// mounted on the same scope when its agent first registers: its goroutine
// starts on a child scope when the inbox is first used and is retired after
// the inbox idle timeout, and the scope's cancellation stops every inbox.
func (relay *Relay[Body]) Start(scope *runtime.Scope) error {
	if !relay.started.CompareAndSwap(false, true) {
		return errors.New("relay: already started")
	}
	if err := scope.GoOwner(directoryOwner, func(ctx context.Context) error {
		relay.directory.Run(&directoryState[Body]{inboxes: map[AgentID]*lazyInbox[Body]{}, scope: scope})
		return nil
	}); err != nil {
		return err
	}
	return scope.Go(func(ctx context.Context) error {
		<-ctx.Done()
		relay.directory.Submit(func(_ *directoryState[Body]) bool { return true })
		return nil
	})
}

// Register opens the agent's inbox if it has none and then binds the agent to
// its address. The inbox comes first so a committed registration always has
// an inbox to deliver to: if the registry write fails, the agent is left with
// an inbox and no registration, which nothing routes to and which the next
// successful Register reuses. Re-registering after a restart keeps the inbox
// and everything queued in it.
func (relay *Relay[Body]) Register(ctx context.Context, registration Registration) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	if !relay.started.Load() {
		return ErrNotStarted
	}
	if _, err := relay.inbox(ctx, registration.Agent, true); err != nil {
		return err
	}
	return relay.registry.Register(ctx, registration)
}

// Resolve returns the agent's current registration.
func (relay *Relay[Body]) Resolve(ctx context.Context, agent AgentID) (Registration, error) {
	return relay.registry.Resolve(ctx, agent)
}

// inbox finds the agent's inbox, opening it when create is set. Opening it
// mounts it as a lazy service; no goroutine starts until it is used.
func (relay *Relay[Body]) inbox(ctx context.Context, agent AgentID, create bool) (*lazyInbox[Body], error) {
	if !relay.started.Load() {
		return nil, ErrNotStarted
	}
	type found struct {
		box *lazyInbox[Body]
		err error
	}
	reply := make(chan found, 1)
	if !relay.directory.SubmitContext(ctx, nil, func(state *directoryState[Body]) bool {
		box, open := state.inboxes[agent]
		if open || !create {
			reply <- found{box: box}
			return false
		}
		box, err := runtime.NewLazyService(newInbox[Body](agent),
			runtime.WithIdleTimeout(relay.inboxIdle), runtime.WithLazyName(inboxOwner+string(agent)))
		if err == nil {
			err = box.Start(state.scope)
		}
		if err != nil {
			reply <- found{err: err}
			return false
		}
		state.inboxes[agent] = box
		reply <- found{box: box}
		return false
	}) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrStopped
	}
	result := <-reply
	if result.err != nil {
		return nil, result.err
	}
	if result.box == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAgent, agent)
	}
	return result.box, nil
}

// send routes body from the agent registered at from to the agent registered
// at to. Both addresses must be current registrations: a sender cannot speak
// for an address it does not hold, and an address replaced by a
// re-registration no longer receives.
func (relay *Relay[Body]) send(ctx context.Context, from model.IAgentAddress, to model.IAgentAddress, body Body) (Envelope[Body], error) {
	if from == nil || to == nil {
		return Envelope[Body]{}, fmt.Errorf("%w: sender and recipient addresses are required", ErrUnknownAddress)
	}
	sender, err := relay.registry.Locate(ctx, from.Key())
	if err != nil {
		return Envelope[Body]{}, fmt.Errorf("sender: %w", err)
	}
	recipient, err := relay.registry.Locate(ctx, to.Key())
	if err != nil {
		return Envelope[Body]{}, fmt.Errorf("recipient: %w", err)
	}
	box, err := relay.inbox(ctx, recipient.Agent, false)
	if err != nil {
		return Envelope[Body]{}, err
	}
	envelope := Envelope[Body]{
		ID:        uuid.New(),
		FromAgent: sender.Agent,
		From:      sender.Address,
		ToAgent:   recipient.Agent,
		To:        recipient.Address,
		Tier:      ipc.Widest(sender.Address.Tier(), recipient.Address.Tier()),
		Body:      body,
		SentAt:    relay.now(),
	}
	if err := use(ctx, box, func(open *inbox[Body]) error { return open.deliver(ctx, envelope) }); err != nil {
		return Envelope[Body]{}, err
	}
	relay.metrics.sent(envelope.Tier)
	return envelope, nil
}

// receive takes the agent's oldest envelope, waiting if there is none.
func (relay *Relay[Body]) receive(ctx context.Context, agent AgentID) (Envelope[Body], error) {
	box, err := relay.inbox(ctx, agent, false)
	if err != nil {
		return Envelope[Body]{}, err
	}
	var envelope Envelope[Body]
	if err := use(ctx, box, func(open *inbox[Body]) error {
		envelope, err = open.receive(ctx)
		return err
	}); err != nil {
		return Envelope[Body]{}, err
	}
	relay.metrics.delivered(envelope.Tier)
	return envelope, nil
}

// fetch returns up to limit unacknowledged envelopes without removing them.
func (relay *Relay[Body]) fetch(ctx context.Context, agent AgentID, limit int) ([]Envelope[Body], error) {
	if limit <= 0 {
		return nil, fmt.Errorf("relay: fetch limit must be positive, got %d", limit)
	}
	box, err := relay.inbox(ctx, agent, false)
	if err != nil {
		return nil, err
	}
	var fetched []Envelope[Body]
	err = use(ctx, box, func(open *inbox[Body]) error {
		fetched, err = open.fetch(ctx, limit)
		return err
	})
	return fetched, err
}

// acknowledge removes fetched envelopes; each removal is one delivery.
func (relay *Relay[Body]) acknowledge(ctx context.Context, agent AgentID, identifiers []uuid.UUID) (int, error) {
	box, err := relay.inbox(ctx, agent, false)
	if err != nil {
		return 0, err
	}
	var removed []Envelope[Body]
	if err := use(ctx, box, func(open *inbox[Body]) error {
		removed, err = open.acknowledge(ctx, identifiers)
		return err
	}); err != nil {
		return 0, err
	}
	for _, envelope := range removed {
		relay.metrics.delivered(envelope.Tier)
	}
	return len(removed), nil
}

// use runs call on the agent's inbox, starting the inbox's goroutine if it
// was retired, and holds the inbox running until call returns. An inbox whose
// relay has stopped reports [ErrStopped].
func use[Body any](ctx context.Context, box *lazyInbox[Body], call func(open *inbox[Body]) error) error {
	err := box.Use(ctx, call)
	if errors.Is(err, runtime.ErrLazyServiceStopped) {
		return fmt.Errorf("%w: %w", ErrStopped, err)
	}
	return err
}
