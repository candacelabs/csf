// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/relay"
)

const (
	pollerOwner     = "intake poller"
	dispatcherOwner = "intake dispatcher"
)

var (
	// ErrMissingOption reports a required grant that was not supplied.
	ErrMissingOption = errors.New("intake: a required option is missing")
	// ErrAlreadyStarted reports a second Start.
	ErrAlreadyStarted = errors.New("intake: already started")
)

// Status is a snapshot of what the intake has done since it started. Every
// counter only grows.
type Status struct {
	// Polls counts requests sent, NotModified the 304s among them,
	// RateLimited the responses that started a backoff, and PollFailures the
	// requests that failed or answered something unusable.
	Polls, NotModified, RateLimited, PollFailures int64
	// Accepted counts events queued, Duplicates events the queue had already
	// accepted, and Skipped events dropped as backlog, ignored actors or
	// invalid.
	Accepted, Duplicates, Skipped int64
	// Delivered counts events handed to their agent's inbox, Unrouted events
	// no route owns, and Undeliverable events whose agent could not be
	// resolved or reached.
	Delivered, Unrouted, Undeliverable int64
	// BackoffUntil is when the latest rate-limit backoff ends; zero if none.
	BackoffUntil time.Time
}

// counters are written by the poller and the dispatcher and read by Status.
type counters struct {
	polls, notModified, rateLimited, pollFailures atomic.Int64
	accepted, duplicates, skipped                 atomic.Int64
	delivered, unrouted, undeliverable            atomic.Int64
	backoffUntil                                  atomic.Pointer[time.Time]
}

// EventIntake polls GitHub and delivers actionable events to their owning
// agents through the relay. It is a runtime service: mount it after the relay.
type EventIntake struct {
	configuration
	source   SourceAddress
	counters counters
	started  atomic.Bool
}

var _ runtime.IService = (*EventIntake)(nil)

// NewEventIntake validates its options and builds an intake that has not
// started. WithHTTPClient, WithRelay, WithRoutes and WithRepositories are
// required.
func NewEventIntake(options ...Option) (*EventIntake, error) {
	configured, err := configure(options)
	if err != nil {
		return nil, err
	}
	source, err := NewSourceAddress(string(configured.sourceAgent))
	if err != nil {
		return nil, err
	}
	return &EventIntake{configuration: configured, source: source}, nil
}

// Source is the address the intake registers and sends from.
func (intake *EventIntake) Source() SourceAddress { return intake.source }

// Start registers the intake with the relay — which must already be started —
// and starts the poller and the dispatcher on scope. Both return when scope is
// canceled; neither outlives it.
func (intake *EventIntake) Start(scope *runtime.Scope) error {
	if scope == nil {
		return errors.New("intake: Start needs a scope")
	}
	if !intake.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	if intake.since.IsZero() {
		intake.since = intake.clock.Now()
	}
	registration := relay.Registration{Agent: intake.sourceAgent, Address: intake.source}
	if err := intake.directory.Register(scope.Context(), registration); err != nil {
		return fmt.Errorf("intake: register %q with the relay: %w", intake.sourceAgent, err)
	}
	if err := scope.GoOwner(pollerOwner, intake.poll); err != nil {
		return err
	}
	return scope.GoOwner(dispatcherOwner, intake.dispatch)
}

// Status returns a snapshot of the counters.
func (intake *EventIntake) Status() Status {
	status := Status{
		Polls:         intake.counters.polls.Load(),
		NotModified:   intake.counters.notModified.Load(),
		RateLimited:   intake.counters.rateLimited.Load(),
		PollFailures:  intake.counters.pollFailures.Load(),
		Accepted:      intake.counters.accepted.Load(),
		Duplicates:    intake.counters.duplicates.Load(),
		Skipped:       intake.counters.skipped.Load(),
		Delivered:     intake.counters.delivered.Load(),
		Unrouted:      intake.counters.unrouted.Load(),
		Undeliverable: intake.counters.undeliverable.Load(),
	}
	if until := intake.counters.backoffUntil.Load(); until != nil {
		status.BackoffUntil = *until
	}
	return status
}

// offer filters one normalized event and queues it. It returns the queue's
// error only when ctx ended; anything else is counted and logged.
func (intake *EventIntake) offer(ctx context.Context, event *intakev1.Event) error {
	if event.GetOccurredAt().AsTime().Before(intake.since) {
		intake.counters.skipped.Add(1)
		return nil
	}
	if _, ignored := intake.ignoredActors[event.GetActor()]; ignored {
		intake.counters.skipped.Add(1)
		return nil
	}
	if err := errors.Join(intakev1.ValidateEvent(event), intakev1.ValidateSubject(event.GetSubject())); err != nil {
		intake.counters.skipped.Add(1)
		intake.logger.Warn("intake: dropped an invalid event", "event", event.GetId(), "error", err)
		return nil
	}
	accepted, err := intake.queue.Offer(ctx, event)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		intake.counters.skipped.Add(1)
		intake.logger.Warn("intake: queue refused an event", "event", event.GetId(), "error", err)
		return nil
	}
	if accepted {
		intake.counters.accepted.Add(1)
	} else {
		intake.counters.duplicates.Add(1)
	}
	return nil
}

// dispatch is the dispatcher goroutine: it takes queued events and sends each
// to its owning agent until ctx ends.
func (intake *EventIntake) dispatch(ctx context.Context) error {
	for {
		event, err := intake.queue.Take(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("intake: take from queue: %w", err)
		}
		intake.deliver(ctx, event)
	}
}

// deliver routes one event and sends it; failures are counted and logged,
// never retried (see the package's limits).
func (intake *EventIntake) deliver(ctx context.Context, event *intakev1.Event) {
	agent, err := intake.routes.Route(ctx, event.GetSubject())
	if err != nil {
		intake.counters.unrouted.Add(1)
		intake.logger.Info("intake: no agent owns the event's subject", "event", event.GetId(), "error", err)
		return
	}
	registration, err := intake.directory.Resolve(ctx, agent)
	if err == nil {
		_, err = intake.messenger.Send(ctx, intake.source, registration.Address, event)
	}
	if err != nil {
		intake.counters.undeliverable.Add(1)
		intake.logger.Warn("intake: event not delivered", "event", event.GetId(), "agent", string(agent), "error", err)
		return
	}
	intake.counters.delivered.Add(1)
}

// Structured log keys.
const (
	logRepository = "repository"
	logFeed       = "feed"
	logStatus     = "status"
	logUntil      = "until"
	logError      = "error"
)

// logAttributes is the context every poll log record carries.
func logAttributes(repository string, feed feed) []any {
	return []any{slog.String(logRepository, repository), slog.String(logFeed, feed.name)}
}
