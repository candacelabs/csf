// Copyright 2026 Candace Labs

package relay

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/collections"
	"github.com/candacelabs/csf/pkg/mailbox"
	"github.com/candacelabs/csf/runtime"
)

// mailboxOwner names the goroutine that owns an inbox's state during a run.
const mailboxOwner = "mailbox"

// inboxState is owned by the inbox's mailbox goroutine while a run is live; no
// field is touched anywhere else. Its envelopes carry the relay's one message
// type, so nothing is erased on the way in or asserted on the way out. It
// belongs to the inbox, not to the run, so a later run resumes exactly where
// the retired one stopped.
type inboxState[Body any] struct {
	// pending is first in, first out. An envelope a host or network agent
	// has fetched stays here until it acknowledges it, so a fetch that dies
	// in transit is redelivered by the next one.
	pending []Envelope[Body]
	// waiters are in-process receivers blocked in Receive, oldest first.
	// Each channel has room for exactly one envelope.
	waiters []chan Envelope[Body]
}

// inbox is one agent's message queue. It is a lazily started runtime service
// (see [runtime.LazyService]): each run serializes the state onto one mailbox
// goroutine, and the relay retires the run once the inbox has been idle — no
// lease, nothing pending, no receiver waiting — for the configured timeout.
type inbox[Body any] struct {
	agent AgentID
	state *inboxState[Body]
	// mailbox is the current run's; a new run publishes a new one before any
	// caller can hold a lease on it.
	mailbox atomic.Pointer[mailbox.Mailbox[inboxState[Body]]]
	// backlog is pending plus waiters, published by the owning goroutine
	// after every command so [inbox.Idle] can read it from outside.
	backlog atomic.Int64
}

var (
	_ runtime.IService      = (*inbox[string])(nil)
	_ runtime.IIdleReporter = (*inbox[string])(nil)
)

func newInbox[Body any](agent AgentID) *inbox[Body] {
	return &inbox[Body]{agent: agent, state: &inboxState[Body]{}}
}

// Start runs the inbox's mailbox on scope and retires the mailbox when the
// scope is canceled. A blocked receiver then observes the mailbox stopping.
func (box *inbox[Body]) Start(scope *runtime.Scope) error {
	owner := mailbox.New[inboxState[Body]]()
	box.mailbox.Store(owner)
	if err := scope.GoOwner(mailboxOwner, func(_ context.Context) error {
		owner.Run(box.state)
		return nil
	}); err != nil {
		return err
	}
	return scope.Go(func(ctx context.Context) error {
		<-ctx.Done()
		owner.Submit(func(_ *inboxState[Body]) bool { return true })
		return nil
	})
}

// Idle reports that nothing is queued and no receiver is waiting, so a
// retirement loses nothing.
func (box *inbox[Body]) Idle() bool { return box.backlog.Load() == 0 }

// settle publishes the backlog; only the owning goroutine calls it.
func (box *inbox[Body]) settle(state *inboxState[Body]) {
	box.backlog.Store(int64(len(state.pending) + len(state.waiters)))
}

// call runs command on the owning goroutine and waits for its result.
func call[Body any, Result any](ctx context.Context, box *inbox[Body], command func(state *inboxState[Body]) Result) (Result, error) {
	reply := make(chan Result, 1)
	var zero Result
	if !box.mailbox.Load().SubmitContext(ctx, nil, func(state *inboxState[Body]) bool {
		result := command(state)
		// Published before the reply, so the caller's lease outlasts it.
		box.settle(state)
		reply <- result
		return false
	}) {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return zero, ErrStopped
	}
	select {
	case result := <-reply:
		return result, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// deliver hands envelope to the oldest waiting receiver, or queues it.
func (box *inbox[Body]) deliver(ctx context.Context, envelope Envelope[Body]) error {
	_, err := call(ctx, box, func(state *inboxState[Body]) struct{} {
		if len(state.waiters) > 0 {
			waiter := state.waiters[0]
			state.waiters = state.waiters[1:]
			waiter <- envelope
			return struct{}{}
		}
		state.pending = append(state.pending, envelope)
		return struct{}{}
	})
	return err
}

// receive takes the oldest envelope, waiting for one if the inbox is empty.
// The handoff is the delivery: an in-process receiver does not acknowledge.
func (box *inbox[Body]) receive(ctx context.Context) (Envelope[Body], error) {
	owner := box.mailbox.Load()
	waiter := make(chan Envelope[Body], 1)
	taken, err := call(ctx, box, func(state *inboxState[Body]) *Envelope[Body] {
		if len(state.pending) > 0 {
			head := state.pending[0]
			state.pending = state.pending[1:]
			return &head
		}
		state.waiters = append(state.waiters, waiter)
		return nil
	})
	if err != nil {
		return Envelope[Body]{}, err
	}
	if taken != nil {
		return *taken, nil
	}
	select {
	case envelope := <-waiter:
		return envelope, nil
	case <-owner.Stopped():
		return Envelope[Body]{}, ErrStopped
	case <-ctx.Done():
		box.withdraw(owner, waiter)
		return Envelope[Body]{}, ctx.Err()
	}
}

// withdraw removes an abandoned waiter. If an envelope reached it first, the
// envelope goes back to the head of the queue rather than being lost.
func (box *inbox[Body]) withdraw(owner *mailbox.Mailbox[inboxState[Body]], waiter chan Envelope[Body]) {
	owner.Submit(func(state *inboxState[Body]) bool {
		defer box.settle(state)
		if remaining, removed := collections.Set[chan Envelope[Body]](state.waiters).Remove(waiter); removed {
			state.waiters = remaining
			return false
		}
		select {
		case envelope := <-waiter:
			state.pending = append([]Envelope[Body]{envelope}, state.pending...)
		default:
		}
		return false
	})
}

// fetch returns up to limit of the oldest unacknowledged envelopes without
// removing them.
func (box *inbox[Body]) fetch(ctx context.Context, limit int) ([]Envelope[Body], error) {
	return call(ctx, box, func(state *inboxState[Body]) []Envelope[Body] {
		return slices.Clone(state.pending[:min(limit, len(state.pending))])
	})
}

// acknowledge removes the named envelopes and returns the ones it removed.
func (box *inbox[Body]) acknowledge(ctx context.Context, identifiers []uuid.UUID) ([]Envelope[Body], error) {
	return call(ctx, box, func(state *inboxState[Body]) []Envelope[Body] {
		var removed []Envelope[Body]
		state.pending = slices.DeleteFunc(state.pending, func(envelope Envelope[Body]) bool {
			if slices.Contains(identifiers, envelope.ID) {
				removed = append(removed, envelope)
				return true
			}
			return false
		})
		return removed
	})
}
