package cron

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidTrigger reports a trigger declaration the grammar rejects: its
// name, one of its policies or its schedule definition.
var ErrInvalidTrigger = errors.New("cron: invalid trigger")

const (
	// MaxTriggerNameBytes bounds a trigger name, as the store's check
	// constraint does.
	MaxTriggerNameBytes = 128
	// triggerNameGrammar is the name grammar, quoted in the rejection.
	triggerNameGrammar = "^[a-z][a-z0-9._/-]*$"
)

// CatchUpPolicy says what a trigger does with the occurrences it missed while
// its scheduler was not running.
type CatchUpPolicy string

const (
	// CatchUpNone advances past every missed occurrence without invoking it,
	// as traditional cron does. It is the default.
	CatchUpNone CatchUpPolicy = "none"
	// CatchUpLatest invokes only the most recent missed occurrence.
	CatchUpLatest CatchUpPolicy = "latest"
	// CatchUpAll invokes every missed occurrence, up to the scheduler's
	// catch-up limit; the overlap policy still applies while they run.
	CatchUpAll CatchUpPolicy = "all"
)

// OverlapPolicy says whether two occurrences of one trigger may run at the
// same time. The store enforces it, so it holds across processes sharing one
// database, not only within one.
type OverlapPolicy string

const (
	// OverlapSkip records a skipped occurrence while another occurrence of
	// the trigger holds a live lease. It is the default.
	OverlapSkip OverlapPolicy = "skip"
	// OverlapAllow lets occurrences of one trigger run concurrently.
	OverlapAllow OverlapPolicy = "allow"
)

// Valid reports whether the policy is one the grammar defines.
func (policy CatchUpPolicy) Valid() bool {
	switch policy {
	case CatchUpNone, CatchUpLatest, CatchUpAll:
		return true
	default:
		return false
	}
}

// Valid reports whether the policy is one the grammar defines.
func (policy OverlapPolicy) Valid() bool {
	switch policy {
	case OverlapSkip, OverlapAllow:
		return true
	default:
		return false
	}
}

// TriggerDefinition is the static, persistence-neutral declaration of one
// trigger: its name, its schedule and its two policies. The operation a
// trigger invokes is registered in the process by name and never persisted;
// a store maps the definition to its own rows at its boundary.
type TriggerDefinition struct {
	Name     string             `json:"name"`
	Schedule ScheduleDefinition `json:"schedule"`
	CatchUp  CatchUpPolicy      `json:"catch_up"`
	Overlap  OverlapPolicy      `json:"overlap"`
}

// TriggerState is the durable scheduling cursor of one active trigger.
type TriggerState struct {
	Definition TriggerDefinition `json:"definition"`
	NextRunAt  time.Time         `json:"next_run_at"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// ValidateTriggerName accepts the names the store's check constraint accepts:
// one to MaxTriggerNameBytes bytes of lower-case ASCII letters, digits,
// dots, underscores, slashes and dashes, starting with a letter.
func ValidateTriggerName(name string) error {
	if len(name) == 0 || len(name) > MaxTriggerNameBytes || name[0] < 'a' || name[0] > 'z' {
		return triggerNameError()
	}
	for index := 1; index < len(name); index++ {
		value := name[index]
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') &&
			value != '.' && value != '_' && value != '/' && value != '-' {
			return triggerNameError()
		}
	}
	return nil
}

func triggerNameError() error {
	return fmt.Errorf("%w: trigger name must match %s and contain 1 to %d bytes",
		ErrInvalidTrigger, triggerNameGrammar, MaxTriggerNameBytes)
}

// NormalizeTriggerDefinition validates a declaration and returns it in its
// persisted form together with its schedule. An interval schedule that
// states no anchor is anchored at now, so its cadence survives a restart;
// an anchor is kept at PostgreSQL's microsecond precision.
func NormalizeTriggerDefinition(definition TriggerDefinition, now time.Time) (TriggerDefinition, Schedule, error) {
	if err := ValidateTriggerName(definition.Name); err != nil {
		return TriggerDefinition{}, Schedule{}, err
	}
	if !definition.CatchUp.Valid() || !definition.Overlap.Valid() {
		return TriggerDefinition{}, Schedule{}, fmt.Errorf("%w: trigger %q has invalid policies", ErrInvalidTrigger, definition.Name)
	}
	if definition.Schedule.HasAnchor {
		definition.Schedule.Anchor = definition.Schedule.Anchor.UTC().Truncate(time.Microsecond)
	}
	schedule, err := ScheduleFromDefinition(definition.Schedule)
	if err != nil {
		return TriggerDefinition{}, Schedule{}, fmt.Errorf("%w: trigger %q schedule definition: %w", ErrInvalidTrigger, definition.Name, err)
	}
	if definition.Schedule.Kind == ScheduleKindEvery && !definition.Schedule.HasAnchor {
		schedule = schedule.Anchor(now)
	}
	definition.Schedule, err = schedule.Definition()
	if err != nil {
		return TriggerDefinition{}, Schedule{}, fmt.Errorf("%w: trigger %q normalized schedule: %w", ErrInvalidTrigger, definition.Name, err)
	}
	return definition, schedule, nil
}

// PreserveIntervalAnchor keeps an established interval anchor when a trigger
// is declared again without one and nothing about its interval changed, so a
// restart does not restart the cadence.
func PreserveIntervalAnchor(incoming, existing TriggerDefinition) TriggerDefinition {
	if incoming.Schedule.Kind != ScheduleKindEvery || incoming.Schedule.HasAnchor ||
		existing.Schedule.Kind != ScheduleKindEvery || !existing.Schedule.HasAnchor {
		return incoming
	}
	if incoming.Schedule.Interval == existing.Schedule.Interval &&
		incoming.Schedule.Timezone == existing.Schedule.Timezone &&
		incoming.Schedule.Canonical == existing.Schedule.Canonical {
		incoming.Schedule.Anchor = existing.Schedule.Anchor
		incoming.Schedule.HasAnchor = true
	}
	return incoming
}
