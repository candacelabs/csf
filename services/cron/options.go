// Copyright 2026 Candace Labs

package cron

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/candacelabs/csf/ipc/clock"
	grammar "github.com/candacelabs/csf/pkg/cron"
)

const (
	// DefaultLeaseDuration is how long an occurrence's lease stays valid
	// without a renewal; a running occurrence renews three times per
	// duration.
	DefaultLeaseDuration = 30 * time.Second
	// DefaultCatchUpLimit bounds the due occurrences one trigger processes
	// in one scheduling cycle.
	DefaultCatchUpLimit = 1_000
	maxCatchUpLimit     = 1<<31 - 1
	maxLeaseOwnerBytes  = 128
)

var (
	// ErrInvalidConfiguration reports an option, a declaration or a stored
	// value that cannot form a safe scheduler.
	ErrInvalidConfiguration = errors.New("cron: invalid configuration")
	// ErrStoreRequired reports a scheduler built without [WithStore].
	ErrStoreRequired = errors.New("cron: a store is required")
	// ErrNoTriggers reports a scheduler built without a [WithTrigger].
	ErrNoTriggers = errors.New("cron: at least one trigger is required")
	// ErrAlreadyStarted reports a second Start of one scheduler.
	ErrAlreadyStarted = errors.New("cron: scheduler is already started")
)

// Option configures a [Scheduler]; [NewScheduler] validates the whole set
// before building anything.
type Option func(configuration *configuration) error

// TriggerOption configures one trigger declared with [WithTrigger].
type TriggerOption func(policies *triggerPolicies) error

type configuration struct {
	store         IStore
	clock         clock.IClock
	triggers      []declaredTrigger
	leaseDuration time.Duration
	catchUpLimit  int
	leaseOwner    string
}

type triggerPolicies struct {
	catchUp grammar.CatchUpPolicy
	overlap grammar.OverlapPolicy
}

type declaredTrigger struct {
	name      string
	schedule  grammar.Schedule
	operation Operation
	policies  triggerPolicies
}

// WithStore grants the store occurrences are recorded through: [NewStore]
// over the csfpg capability in a binary, the same store over pgmem in a
// spec. Required.
func WithStore(store IStore) Option {
	return func(configuration *configuration) error {
		if store == nil {
			return fmt.Errorf("%w: nil store", ErrInvalidConfiguration)
		}
		configuration.store = store
		return nil
	}
}

// WithClock grants the clock the scheduler reads and waits on. The host's
// clock is the default; a spec grants a controllable one.
func WithClock(source clock.IClock) Option {
	return func(configuration *configuration) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidConfiguration)
		}
		configuration.clock = source
		return nil
	}
}

// WithTrigger declares one trigger: a name, its human-readable schedule and
// the operation each occurrence invokes, with [WithCatchUp] and
// [WithOverlap] choosing its policies. At least one is required, and names
// are unique.
func WithTrigger(name string, schedule grammar.Schedule, operation Operation, options ...TriggerOption) Option {
	return func(configuration *configuration) error {
		if err := grammar.ValidateTriggerName(name); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidConfiguration, err)
		}
		if err := schedule.Validate(); err != nil {
			return fmt.Errorf("%w: trigger %q schedule: %w", ErrInvalidConfiguration, name, err)
		}
		if operation == nil {
			return fmt.Errorf("%w: trigger %q has no operation", ErrInvalidConfiguration, name)
		}
		policies := triggerPolicies{catchUp: grammar.CatchUpNone, overlap: grammar.OverlapSkip}
		for _, option := range options {
			if option == nil {
				return fmt.Errorf("%w: trigger %q has a nil option", ErrInvalidConfiguration, name)
			}
			if err := option(&policies); err != nil {
				return fmt.Errorf("trigger %q: %w", name, err)
			}
		}
		configuration.triggers = append(configuration.triggers, declaredTrigger{
			name: name, schedule: schedule, operation: operation, policies: policies,
		})
		return nil
	}
}

// WithCatchUp sets a trigger's missed-occurrence policy.
func WithCatchUp(policy grammar.CatchUpPolicy) TriggerOption {
	return func(policies *triggerPolicies) error {
		if !policy.Valid() {
			return fmt.Errorf("%w: unknown catch-up policy %q", ErrInvalidConfiguration, policy)
		}
		policies.catchUp = policy
		return nil
	}
}

// WithOverlap sets a trigger's concurrent-occurrence policy.
func WithOverlap(policy grammar.OverlapPolicy) TriggerOption {
	return func(policies *triggerPolicies) error {
		if !policy.Valid() {
			return fmt.Errorf("%w: unknown overlap policy %q", ErrInvalidConfiguration, policy)
		}
		policies.overlap = policy
		return nil
	}
}

// WithLeaseDuration sets how long an occurrence's lease stays valid without
// a renewal.
func WithLeaseDuration(duration time.Duration) Option {
	return func(configuration *configuration) error {
		if duration < time.Microsecond || duration%time.Microsecond != 0 {
			return fmt.Errorf("%w: lease duration must be a positive whole number of microseconds", ErrInvalidConfiguration)
		}
		configuration.leaseDuration = duration
		return nil
	}
}

// WithCatchUpLimit bounds the due occurrences one trigger processes in one
// scheduling cycle.
func WithCatchUpLimit(limit int) Option {
	return func(configuration *configuration) error {
		if limit <= 0 || limit > maxCatchUpLimit {
			return fmt.Errorf("%w: catch-up limit must be between 1 and %d", ErrInvalidConfiguration, maxCatchUpLimit)
		}
		configuration.catchUpLimit = limit
		return nil
	}
}

// WithLeaseOwner names this process on the leases it holds. [NewScheduler]
// generates a random identity otherwise; a binary with a stable replica
// identity passes it here.
func WithLeaseOwner(owner string) Option {
	return func(configuration *configuration) error {
		owner = strings.TrimSpace(owner)
		if owner == "" || len(owner) > maxLeaseOwnerBytes {
			return fmt.Errorf("%w: lease owner must contain 1 to %d bytes", ErrInvalidConfiguration, maxLeaseOwnerBytes)
		}
		configuration.leaseOwner = owner
		return nil
	}
}
