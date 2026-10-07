// Copyright 2026 Candace Labs

// Package cron is the cron service: the durable in-process scheduler that
// mounts into a host runtime, fires each declared trigger on its schedule
// and records every occurrence through csfpg. A trigger is a named,
// human-readable schedule (candace/pkg/cron) with a catch-up and an overlap
// policy that invokes one operation of a mounted service; each firing is an
// occurrence, run under a fenced lease so that execution is at least once
// and survives a restart. The store, the clock and the triggers are granted
// through options; Start starts every goroutine through the scope the
// runtime hands it, and the scope's join is the service's cleanup.
package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/candacelabs/csf/io/kernel/clock"
	grammar "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/runtime"
)

const (
	// schedulerOwner names the scheduling goroutine on its scope.
	schedulerOwner = "scheduler"
	// completionTimeout bounds the record of an occurrence's end, which is
	// written even after the scope was canceled.
	completionTimeout = 5 * time.Second
	maxRecordedError  = 4 << 10

	skipReasonSuperseded      = "superseded"
	skipReasonCatchUpDisabled = "catch_up_disabled"
	leaseTokenPrefix          = "lease"
	leaseOwnerPrefix          = "runner"
)

// Operation is what a trigger invokes: one operation of a mounted service.
// It stops promptly when ctx is canceled. An error records a failed
// occurrence and never stops the scheduler.
type Operation func(ctx context.Context, occurrence Occurrence) error

// Occurrence identifies the firing an operation is running: its stable ID,
// the idempotency key for any external effect, and the attempt number.
type Occurrence struct {
	ID          string    `json:"id"`
	TriggerName string    `json:"trigger_name"`
	ScheduledAt time.Time `json:"scheduled_at"`
	StartedAt   time.Time `json:"started_at"`
	Attempt     uint32    `json:"attempt"`
}

// Scheduler is the cron service. It reconciles its declared triggers with
// the store when started, fires each due occurrence under a lease on its
// scope, and records every outcome; a failure to read or write the store
// safely fails the scope, and so the runtime.
type Scheduler struct {
	store         IStore
	clock         clock.IClock
	triggers      map[string]declaredTrigger
	names         []string
	leaseDuration time.Duration
	catchUpLimit  int
	leaseOwner    string
	started       atomic.Bool
}

var _ runtime.IService = (*Scheduler)(nil)

type activeTrigger struct {
	declared declaredTrigger
	schedule grammar.Schedule
	state    grammar.TriggerState
	startup  bool
}

type dueOccurrence struct {
	scheduledAt time.Time
	nextRunAt   time.Time
}

// NewScheduler validates the whole option set and returns a stopped
// scheduler: it persists nothing and starts no goroutine until mounted.
func NewScheduler(options ...Option) (*Scheduler, error) {
	configured := configuration{
		clock:         clock.NewSystemClock(),
		leaseDuration: DefaultLeaseDuration,
		catchUpLimit:  DefaultCatchUpLimit,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidConfiguration)
		}
		if err := option(&configured); err != nil {
			return nil, err
		}
	}
	if configured.store == nil {
		return nil, ErrStoreRequired
	}
	if len(configured.triggers) == 0 {
		return nil, ErrNoTriggers
	}
	if configured.leaseOwner == "" {
		identity, err := randomIdentity(leaseOwnerPrefix)
		if err != nil {
			return nil, fmt.Errorf("cron: generating lease owner: %w", err)
		}
		configured.leaseOwner = identity
	}
	triggers := make(map[string]declaredTrigger, len(configured.triggers))
	names := make([]string, 0, len(configured.triggers))
	for _, trigger := range configured.triggers {
		if _, duplicate := triggers[trigger.name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate trigger %q", ErrInvalidConfiguration, trigger.name)
		}
		triggers[trigger.name] = trigger
		names = append(names, trigger.name)
	}
	return &Scheduler{
		store:         configured.store,
		clock:         configured.clock,
		triggers:      triggers,
		names:         names,
		leaseDuration: configured.leaseDuration,
		catchUpLimit:  configured.catchUpLimit,
		leaseOwner:    configured.leaseOwner,
	}, nil
}

// Start reconciles the declared triggers with the store, so a store that
// cannot be read fails the mount, then starts the scheduling goroutine on
// scope. Every occurrence runs on a goroutine of the same scope: canceling
// the scope cancels the occurrences in flight, and joining it waits for
// each to record its end.
func (scheduler *Scheduler) Start(scope *runtime.Scope) error {
	if !scheduler.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	triggers, err := scheduler.reconcile(scope.Context(), scheduler.clock.Now().UTC())
	if err != nil {
		return err
	}
	return scope.GoOwner(schedulerOwner, func(ctx context.Context) error {
		return scheduler.run(ctx, scope, triggers)
	})
}

func (scheduler *Scheduler) reconcile(ctx context.Context, now time.Time) ([]*activeTrigger, error) {
	definitions := make([]grammar.TriggerDefinition, 0, len(scheduler.names))
	for _, name := range scheduler.names {
		trigger := scheduler.triggers[name]
		definition, err := trigger.schedule.Definition()
		if err != nil {
			return nil, fmt.Errorf("cron: trigger %q schedule projection: %w", name, err)
		}
		definitions = append(definitions, grammar.TriggerDefinition{
			Name:     name,
			Schedule: definition,
			CatchUp:  trigger.policies.catchUp,
			Overlap:  trigger.policies.overlap,
		})
	}
	states, err := scheduler.store.Reconcile(ctx, definitions, now)
	if err != nil {
		return nil, fmt.Errorf("cron: reconcile declared triggers: %w", err)
	}
	if len(states) != len(scheduler.triggers) {
		return nil, fmt.Errorf("cron: reconcile returned %d active triggers, want %d", len(states), len(scheduler.triggers))
	}
	seen := make(map[string]struct{}, len(states))
	triggers := make([]*activeTrigger, 0, len(states))
	for _, state := range states {
		name := state.Definition.Name
		declared, exists := scheduler.triggers[name]
		if !exists {
			return nil, fmt.Errorf("cron: reconcile returned unknown trigger %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("cron: reconcile returned duplicate trigger %q", name)
		}
		seen[name] = struct{}{}
		if state.NextRunAt.IsZero() {
			return nil, fmt.Errorf("cron: reconcile returned zero next occurrence for trigger %q", name)
		}
		schedule, err := grammar.ScheduleFromDefinition(state.Definition.Schedule)
		if err != nil {
			return nil, fmt.Errorf("cron: reconstruct reconciled trigger %q schedule: %w", name, err)
		}
		declared.schedule = schedule
		declared.policies = triggerPolicies{catchUp: state.Definition.CatchUp, overlap: state.Definition.Overlap}
		triggers = append(triggers, &activeTrigger{declared: declared, schedule: schedule, state: state, startup: true})
	}
	return triggers, nil
}

// run is the scheduling goroutine: it fires what is due, waits on the clock
// for the next occurrence or the next lease-recovery pass, and returns when
// its scope is canceled. The trigger cursors are its own data.
func (scheduler *Scheduler) run(ctx context.Context, scope *runtime.Scope, triggers []*activeTrigger) error {
	immediate, err := scheduler.cycle(ctx, scope, triggers, scheduler.clock.Now().UTC())
	if err != nil {
		return shutdownOrFailure(ctx, err)
	}
	for {
		wait := nextWait(triggers, scheduler.clock.Now().UTC(), scheduler.renewalInterval())
		if immediate {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-scheduler.clock.After(wait):
			immediate, err = scheduler.cycle(ctx, scope, triggers, scheduler.clock.Now().UTC())
			if err != nil {
				return shutdownOrFailure(ctx, err)
			}
		}
	}
}

func (scheduler *Scheduler) cycle(ctx context.Context, scope *runtime.Scope, triggers []*activeTrigger, now time.Time) (bool, error) {
	immediate, err := scheduler.recoverExpired(ctx, scope, triggers, now)
	if err != nil {
		return false, err
	}
	for _, trigger := range triggers {
		due, more, err := collectDue(trigger.schedule, trigger.state.NextRunAt, now, scheduler.catchUpLimit)
		if err != nil {
			return false, fmt.Errorf("cron: trigger %q: %w", trigger.declared.name, err)
		}
		if len(due) == 0 {
			trigger.startup = false
			continue
		}
		firstToRun := occurrenceRunIndex(trigger.declared.policies.catchUp, len(due), trigger.startup, more)
		for index, occurrence := range due {
			if index < firstToRun {
				reason := skipReasonSuperseded
				if trigger.startup && trigger.declared.policies.catchUp == grammar.CatchUpNone {
					reason = skipReasonCatchUpDisabled
				}
				if err := scheduler.skip(ctx, trigger, occurrence, reason); err != nil {
					return false, err
				}
				continue
			}
			if _, err := scheduler.claimAndRun(ctx, scope, trigger, occurrence); err != nil {
				return false, err
			}
		}
		immediate = immediate || more
		if !more {
			trigger.startup = false
		}
	}
	return immediate, nil
}

func (scheduler *Scheduler) recoverExpired(ctx context.Context, scope *runtime.Scope, triggers []*activeTrigger, now time.Time) (bool, error) {
	expired, err := scheduler.store.Expired(ctx, now, scheduler.catchUpLimit)
	if err != nil {
		return false, fmt.Errorf("cron: list expired occurrence leases: %w", err)
	}
	byName := make(map[string]*activeTrigger, len(triggers))
	for _, trigger := range triggers {
		byName[trigger.declared.name] = trigger
	}
	progressed := false
	for _, record := range expired {
		trigger, exists := byName[record.TriggerName]
		if !exists {
			return false, fmt.Errorf("cron: expired occurrence %s references inactive trigger %q", record.ID, record.TriggerName)
		}
		nextRunAt, err := trigger.schedule.Next(record.ScheduledAt)
		if err != nil {
			return false, fmt.Errorf("cron: expired trigger %q next occurrence: %w", record.TriggerName, err)
		}
		disposition, err := scheduler.claimAndRun(ctx, scope, trigger, dueOccurrence{
			scheduledAt: record.ScheduledAt,
			nextRunAt:   nextRunAt,
		})
		if err != nil {
			return false, err
		}
		progressed = progressed || disposition != ClaimLeaseHeld
	}
	return progressed && len(expired) == scheduler.catchUpLimit, nil
}

func (scheduler *Scheduler) skip(ctx context.Context, trigger *activeTrigger, occurrence dueOccurrence, reason string) error {
	now := scheduler.clock.Now().UTC()
	request := SkipRequest{
		OccurrenceID: grammar.OccurrenceID(trigger.declared.name, occurrence.scheduledAt),
		TriggerName:  trigger.declared.name,
		ScheduledAt:  occurrence.scheduledAt,
		NextRunAt:    occurrence.nextRunAt,
		SkippedAt:    now,
		Reason:       reason,
	}
	if err := scheduler.store.Skip(ctx, request); err != nil {
		return fmt.Errorf("cron: skip trigger %q occurrence %s: %w", trigger.declared.name, request.OccurrenceID, err)
	}
	trigger.state.NextRunAt = occurrence.nextRunAt
	trigger.state.UpdatedAt = now
	return nil
}

func (scheduler *Scheduler) claimAndRun(ctx context.Context, scope *runtime.Scope, trigger *activeTrigger, occurrence dueOccurrence) (ClaimDisposition, error) {
	now := scheduler.clock.Now().UTC()
	token, err := randomIdentity(leaseTokenPrefix)
	if err != nil {
		return "", fmt.Errorf("cron: trigger %q generate lease token: %w", trigger.declared.name, err)
	}
	request := ClaimRequest{
		OccurrenceID: grammar.OccurrenceID(trigger.declared.name, occurrence.scheduledAt),
		TriggerName:  trigger.declared.name,
		ScheduledAt:  occurrence.scheduledAt,
		NextRunAt:    occurrence.nextRunAt,
		LeaseOwner:   scheduler.leaseOwner,
		LeaseToken:   token,
		ClaimedAt:    now,
		LeaseUntil:   now.Add(scheduler.leaseDuration),
	}
	result, err := scheduler.store.Claim(ctx, request)
	if err != nil {
		return "", fmt.Errorf("cron: claim trigger %q occurrence %s: %w", trigger.declared.name, request.OccurrenceID, err)
	}
	if trigger.state.NextRunAt.Before(occurrence.nextRunAt) {
		trigger.state.NextRunAt = occurrence.nextRunAt
	}
	trigger.state.UpdatedAt = now
	if result.Disposition != ClaimAcquired {
		return result.Disposition, nil
	}
	started := Occurrence{
		ID:          result.Occurrence.ID,
		TriggerName: result.Occurrence.TriggerName,
		ScheduledAt: result.Occurrence.ScheduledAt,
		StartedAt:   result.Occurrence.StartedAt,
		Attempt:     result.Occurrence.Attempt,
	}
	if err := scheduler.startOccurrence(scope, trigger.declared.operation, started, token); err != nil {
		return "", fmt.Errorf("cron: start trigger %q occurrence %s: %w", trigger.declared.name, started.ID, err)
	}
	return result.Disposition, nil
}

// startOccurrence runs one claimed occurrence on the scope: the operation on
// one goroutine, its lease renewal on another, and the record of its end
// once both have returned. A failure to persist that record fails the
// scope; the operation's own failure is recorded, not raised.
func (scheduler *Scheduler) startOccurrence(scope *runtime.Scope, operation Operation, occurrence Occurrence, leaseToken string) error {
	return scope.Go(func(ctx context.Context) error {
		operationContext, cancel := context.WithCancel(ctx)
		defer cancel()
		renewed := make(chan error, 1)
		if err := scope.Go(func(_ context.Context) error {
			renewed <- scheduler.renew(operationContext, cancel, occurrence.ID, leaseToken)
			return nil
		}); err != nil {
			// The scope is already joining: the operation sees its canceled
			// context at once and its end is still recorded below.
			renewed <- nil
		}
		operationError := callOperation(operationContext, operation, occurrence)
		cancel()
		renewError := <-renewed

		status := grammar.OccurrenceSucceeded
		switch {
		case errors.Is(operationError, context.Canceled) || errors.Is(operationError, context.DeadlineExceeded):
			status = grammar.OccurrenceCanceled
		case operationError != nil:
			status = grammar.OccurrenceFailed
		}
		completionContext, completionCancel := context.WithTimeout(context.WithoutCancel(ctx), completionTimeout)
		defer completionCancel()
		completionError := scheduler.store.Complete(completionContext, Completion{
			OccurrenceID: occurrence.ID,
			LeaseToken:   leaseToken,
			Status:       status,
			FinishedAt:   scheduler.clock.Now().UTC(),
			Error:        boundedError(operationError),
		})
		if renewError != nil || completionError != nil {
			return fmt.Errorf("cron: persist trigger %q occurrence %s: %w", occurrence.TriggerName, occurrence.ID, errors.Join(renewError, completionError))
		}
		return nil
	})
}

// renew extends the occurrence's lease on the clock's cadence until ctx is
// canceled. A renewal the store refuses is a lost lease: the operation is
// canceled so that two owners never act on one occurrence.
func (scheduler *Scheduler) renew(ctx context.Context, cancel context.CancelFunc, occurrenceID string, leaseToken string) error {
	interval := scheduler.renewalInterval()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-scheduler.clock.After(interval):
			if ctx.Err() != nil {
				return nil
			}
			renewedAt := scheduler.clock.Now().UTC()
			if err := scheduler.store.Renew(ctx, LeaseRenewal{
				OccurrenceID: occurrenceID,
				LeaseToken:   leaseToken,
				RenewedAt:    renewedAt,
				LeaseUntil:   renewedAt.Add(scheduler.leaseDuration),
			}); err != nil {
				// The operation's completion and a renewal can become ready
				// at the same instant; cancellation after a normal end is
				// not a lost lease.
				if ctx.Err() != nil {
					return nil
				}
				cancel()
				return fmt.Errorf("renew lease: %w", err)
			}
		}
	}
}

func (scheduler *Scheduler) renewalInterval() time.Duration {
	interval := scheduler.leaseDuration / 3
	if interval <= 0 {
		return scheduler.leaseDuration
	}
	return interval
}

func collectDue(schedule grammar.Schedule, next, now time.Time, limit int) ([]dueOccurrence, bool, error) {
	occurrences := make([]dueOccurrence, 0)
	for !next.After(now) {
		if len(occurrences) == limit {
			return occurrences, true, nil
		}
		following, err := schedule.Next(next)
		if err != nil {
			return nil, false, err
		}
		occurrences = append(occurrences, dueOccurrence{
			scheduledAt: next.UTC(),
			nextRunAt:   following.UTC(),
		})
		next = following
	}
	return occurrences, false, nil
}

func occurrenceRunIndex(policy grammar.CatchUpPolicy, count int, startup, more bool) int {
	if count == 0 || policy == grammar.CatchUpAll {
		return 0
	}
	if startup && policy == grammar.CatchUpNone {
		return count
	}
	if more {
		return count
	}
	return count - 1
}

func nextWait(triggers []*activeTrigger, now time.Time, maximum time.Duration) time.Duration {
	next := triggers[0].state.NextRunAt
	for _, trigger := range triggers[1:] {
		if trigger.state.NextRunAt.Before(next) {
			next = trigger.state.NextRunAt
		}
	}
	if !next.After(now) {
		return 0
	}
	wait := next.Sub(now)
	if wait > maximum {
		return maximum
	}
	return wait
}

func callOperation(ctx context.Context, operation Operation, occurrence Occurrence) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("operation panic: %v", recovered)
		}
	}()
	return operation(ctx, occurrence)
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToValidUTF8(err.Error(), "?")
	if len(message) <= maxRecordedError {
		return message
	}
	message = message[:maxRecordedError]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return strings.TrimSpace(message)
}

func randomIdentity(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}

// shutdownOrFailure reports nothing once the scope asked the scheduler to
// stop: a store refused mid-shutdown is not a failure of the service.
func shutdownOrFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}
