// Package cron provides durable in-process scheduling with human-readable
// declarations and explicit state stores.
package cron

import (
	"fmt"
	"strings"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

// Schedule is an immutable, validated-on-use scheduling definition. A zero
// Schedule is invalid. Its location defaults to UTC unless In is called.
//
// Typed-rule DST semantics are deliberately instant-based: Next walks real UTC
// minutes and matches their local civil representation. Spring-forward civil
// times do not run because they do not exist; a matching repeated fall-back
// time runs once for each real instant. Raw rules use robfig/cron's semantics.
type Schedule struct {
	rule      Rule
	location  *time.Location
	anchor    time.Time
	hasAnchor bool
}

// ScheduleKind identifies the structured scheduling form in a Definition.
// It is a domain value, deliberately independent of protobuf and SQLC types.
type ScheduleKind string

const (
	ScheduleKindDaily          ScheduleKind = "daily"
	ScheduleKindWeekly         ScheduleKind = "weekly"
	ScheduleKindMonthly        ScheduleKind = "monthly"
	ScheduleKindLastDayOfMonth ScheduleKind = "last_day_of_month"
	ScheduleKindEvery          ScheduleKind = "every"
	ScheduleKindRaw            ScheduleKind = "raw"
)

// ScheduleDefinition is the neutral persistence and boundary projection of a
// Schedule. It deliberately contains ordinary typed Go values: adapters map
// this into relational SQLC parameters or API messages at their own boundary.
// Canonical is a normalized expression, while the typed fields preserve the
// pleasant DSL shape for schedule kinds that have one.
type ScheduleDefinition struct {
	Kind      ScheduleKind `json:"kind"`
	Timezone  string       `json:"timezone"`
	Canonical string       `json:"canonical"`

	Hour     int          `json:"hour,omitempty"`
	Minute   int          `json:"minute,omitempty"`
	Weekday  time.Weekday `json:"weekday,omitempty"`
	MonthDay int          `json:"month_day,omitempty"`

	Interval  time.Duration `json:"interval,omitempty"`
	Anchor    time.Time     `json:"anchor,omitempty"`
	HasAnchor bool          `json:"has_anchor,omitempty"`
}

// Rule is an opaque schedule declaration constructed by the fluent helpers.
// It intentionally has no exported fields so a caller cannot construct an
// unvalidated wire-shaped schedule.
type Rule struct {
	kind     ruleKind
	at       TimeOfDay
	weekday  time.Weekday
	monthDay int
	interval time.Duration
	raw      string
	err      error
}

type ruleKind uint8

const (
	ruleInvalid ruleKind = iota
	ruleDaily
	ruleWeekly
	ruleMonthly
	ruleLastDayOfMonth
	ruleEvery
	ruleRaw
)

// TimeOfDay is a 24-hour local civil time.
type TimeOfDay struct {
	hour   int
	minute int
	err    error
}

// MeridiemTime is deliberately distinct from TimeOfDay. It can only become a
// schedule time after AM or PM is selected, preventing Daily(At(3)).
type MeridiemTime struct {
	hour   int
	minute int
	err    error
}

// At starts a 12-hour clock declaration. It accepts an optional minute.
func At(hour int, minute ...int) MeridiemTime {
	valueMinute, err := validateClockInput("At", hour, 1, 12, minute)
	return MeridiemTime{hour: hour, minute: valueMinute, err: err}
}

// AM completes a 12-hour declaration.
func (value MeridiemTime) AM() TimeOfDay {
	if value.err != nil {
		return TimeOfDay{err: value.err}
	}
	hour := value.hour
	if hour == 12 {
		hour = 0
	}
	return TimeOfDay{hour: hour, minute: value.minute}
}

// PM completes a 12-hour declaration.
func (value MeridiemTime) PM() TimeOfDay {
	if value.err != nil {
		return TimeOfDay{err: value.err}
	}
	hour := value.hour
	if hour != 12 {
		hour += 12
	}
	return TimeOfDay{hour: hour, minute: value.minute}
}

// At24 declares a 24-hour local civil time.
func At24(hour int, minute ...int) TimeOfDay {
	valueMinute, err := validateClockInput("At24", hour, 0, 23, minute)
	return TimeOfDay{hour: hour, minute: valueMinute, err: err}
}

func validateClockInput(name string, hour, minimumHour, maximumHour int, minute []int) (int, error) {
	if len(minute) > 1 {
		return 0, fmt.Errorf("%s: expected at most one minute, got %d", name, len(minute))
	}
	valueMinute := 0
	if len(minute) == 1 {
		valueMinute = minute[0]
	}
	if hour < minimumHour || hour > maximumHour {
		return valueMinute, fmt.Errorf("%s: hour must be between %d and %d, got %d", name, minimumHour, maximumHour, hour)
	}
	if valueMinute < 0 || valueMinute > 59 {
		return valueMinute, fmt.Errorf("%s: minute must be between 0 and 59, got %d", name, valueMinute)
	}
	return valueMinute, nil
}

func Midnight() TimeOfDay { return At24(0) }
func Noon() TimeOfDay     { return At24(12) }

// Daily returns a rule that fires every local calendar day at at.
func Daily(at TimeOfDay) Rule { return Rule{kind: ruleDaily, at: at, err: at.err} }

// Weekly returns a rule that fires every week at at.
func Weekly(day time.Weekday, at TimeOfDay) Rule {
	rule := Rule{kind: ruleWeekly, weekday: day, at: at, err: at.err}
	if rule.err == nil && (day < time.Sunday || day > time.Saturday) {
		rule.err = fmt.Errorf("Weekly: weekday must be between Sunday and Saturday, got %d", day)
	}
	return rule
}

// Monthly returns a rule that fires on day (1 through 31). Months without that
// day are skipped.
func Monthly(day int, at TimeOfDay) Rule {
	rule := Rule{kind: ruleMonthly, monthDay: day, at: at, err: at.err}
	if rule.err == nil && (day < 1 || day > 31) {
		rule.err = fmt.Errorf("Monthly: day must be between 1 and 31, got %d", day)
	}
	return rule
}

// LastDayOfMonth returns a rule that fires on each month's final local day.
func LastDayOfMonth(at TimeOfDay) Rule {
	return Rule{kind: ruleLastDayOfMonth, at: at, err: at.err}
}

// Every returns an interval rule. Its cadence is anchored when Anchor is
// supplied by the durable runtime; absent an explicit anchor, Next uses the
// Unix epoch as a stable default rather than process start time.
func Every(interval time.Duration) Rule {
	rule := Rule{kind: ruleEvery, interval: interval}
	if interval < time.Microsecond || interval%time.Microsecond != 0 {
		rule.err = fmt.Errorf("Every: interval must be a positive whole number of microseconds, got %s", interval)
	}
	return rule
}

// Raw declares a five-field cron expression. Raw is an escape hatch; it is
// parsed and normalized by Spec/Validate, never persisted as a wire blob.
func Raw(expression string) Rule { return Rule{kind: ruleRaw, raw: strings.TrimSpace(expression)} }

// Spec wraps a rule in a Schedule. It does not panic: invalid declarations are
// retained and reported by Validate, Canonical, or Next.
func Spec(rule Rule) Schedule { return Schedule{rule: rule, location: time.UTC} }

// In returns a copy scheduled in location. A nil location is reported by
// Validate instead of panicking.
func (schedule Schedule) In(location *time.Location) Schedule {
	schedule.location = location
	return schedule
}

// Anchor returns a copy with a durable interval anchor. It applies only to
// Every rules and lets a runtime retain cadence across restarts. The anchor is
// normalized to PostgreSQL's microsecond timestamp precision.
func (schedule Schedule) Anchor(anchor time.Time) Schedule {
	schedule.anchor = anchor.UTC().Truncate(time.Microsecond)
	schedule.hasAnchor = true
	return schedule
}

// IntervalAnchor reports the explicit durable anchor, if any.
func (schedule Schedule) IntervalAnchor() (time.Time, bool) {
	return schedule.anchor, schedule.hasAnchor
}

// Definition returns a structured, normalized, persistence-ready projection.
func (schedule Schedule) Definition() (ScheduleDefinition, error) {
	if err := schedule.Validate(); err != nil {
		return ScheduleDefinition{}, err
	}
	canonical, err := schedule.Canonical()
	if err != nil {
		return ScheduleDefinition{}, err
	}
	definition := ScheduleDefinition{
		Timezone:  schedule.location.String(),
		Canonical: canonical,
		Anchor:    schedule.anchor,
		HasAnchor: schedule.hasAnchor,
	}
	switch schedule.rule.kind {
	case ruleDaily:
		definition.Kind = ScheduleKindDaily
	case ruleWeekly:
		definition.Kind = ScheduleKindWeekly
	case ruleMonthly:
		definition.Kind = ScheduleKindMonthly
	case ruleLastDayOfMonth:
		definition.Kind = ScheduleKindLastDayOfMonth
	case ruleEvery:
		definition.Kind = ScheduleKindEvery
		definition.Interval = schedule.rule.interval
	case ruleRaw:
		definition.Kind = ScheduleKindRaw
	}
	definition.Hour = schedule.rule.at.hour
	definition.Minute = schedule.rule.at.minute
	definition.Weekday = schedule.rule.weekday
	definition.MonthDay = schedule.rule.monthDay
	return definition, nil
}

// ScheduleFromDefinition reconstructs and validates a Schedule from its
// neutral domain projection. It rejects a mismatched Canonical value so an
// adapter cannot silently reinterpret persisted schedule fields.
func ScheduleFromDefinition(definition ScheduleDefinition) (Schedule, error) {
	if definition.Timezone == "" {
		return Schedule{}, fmt.Errorf("schedule definition timezone is required")
	}
	location, err := time.LoadLocation(definition.Timezone)
	if err != nil {
		return Schedule{}, fmt.Errorf("schedule definition timezone: %w", err)
	}
	at := At24(definition.Hour, definition.Minute)
	var rule Rule
	switch definition.Kind {
	case ScheduleKindDaily:
		rule = Daily(at)
	case ScheduleKindWeekly:
		rule = Weekly(definition.Weekday, at)
	case ScheduleKindMonthly:
		rule = Monthly(definition.MonthDay, at)
	case ScheduleKindLastDayOfMonth:
		rule = LastDayOfMonth(at)
	case ScheduleKindEvery:
		rule = Every(definition.Interval)
	case ScheduleKindRaw:
		rule = Raw(definition.Canonical)
	default:
		return Schedule{}, fmt.Errorf("unknown schedule definition kind %q", definition.Kind)
	}
	schedule := Spec(rule).In(location)
	if definition.HasAnchor {
		schedule = schedule.Anchor(definition.Anchor)
	}
	if err := schedule.Validate(); err != nil {
		return Schedule{}, err
	}
	canonical, err := schedule.Canonical()
	if err != nil {
		return Schedule{}, err
	}
	if definition.Canonical != "" && definition.Canonical != canonical {
		return Schedule{}, fmt.Errorf("schedule definition canonical %q does not match normalized %q", definition.Canonical, canonical)
	}
	return schedule, nil
}

// Location returns the schedule location after validation. UTC is the default.
func (schedule Schedule) Location() *time.Location { return schedule.location }

// Validate verifies and compiles all schedule input without panics.
func (schedule Schedule) Validate() error {
	if schedule.location == nil {
		return fmt.Errorf("schedule location must not be nil")
	}
	if schedule.location.String() == "Local" {
		return fmt.Errorf("schedule location must be an explicit portable timezone, not Local")
	}
	if _, err := time.LoadLocation(schedule.location.String()); err != nil {
		return fmt.Errorf("schedule location %q is not portable: %w", schedule.location, err)
	}
	if schedule.rule.err != nil {
		return schedule.rule.err
	}
	if schedule.hasAnchor {
		if schedule.rule.kind != ruleEvery {
			return fmt.Errorf("schedule anchor is only valid for Every")
		}
		if schedule.anchor.IsZero() {
			return fmt.Errorf("schedule interval anchor must not be zero")
		}
	}
	switch schedule.rule.kind {
	case ruleDaily, ruleWeekly, ruleMonthly, ruleLastDayOfMonth:
		return validateTimeOfDay(schedule.rule.at)
	case ruleEvery:
		if schedule.rule.interval < time.Microsecond || schedule.rule.interval%time.Microsecond != 0 {
			return fmt.Errorf("Every: interval must be a positive whole number of microseconds, got %s", schedule.rule.interval)
		}
	case ruleRaw:
		_, err := parseRaw(schedule.rule.raw)
		return err
	default:
		return fmt.Errorf("schedule rule is required")
	}
	return nil
}

// Canonical returns a normalized five-field cron expression, or @every for an
// interval. The separate location and anchor are intentionally not encoded in
// that expression.
func (schedule Schedule) Canonical() (string, error) {
	if err := schedule.Validate(); err != nil {
		return "", err
	}
	rule := schedule.rule
	switch rule.kind {
	case ruleDaily:
		return fmt.Sprintf("%d %d * * *", rule.at.minute, rule.at.hour), nil
	case ruleWeekly:
		return fmt.Sprintf("%d %d * * %d", rule.at.minute, rule.at.hour, rule.weekday), nil
	case ruleMonthly:
		return fmt.Sprintf("%d %d %d * *", rule.at.minute, rule.at.hour, rule.monthDay), nil
	case ruleLastDayOfMonth:
		return fmt.Sprintf("%d %d L * *", rule.at.minute, rule.at.hour), nil
	case ruleEvery:
		return "@every " + rule.interval.String(), nil
	case ruleRaw:
		return normalizeRaw(rule.raw), nil
	default:
		return "", fmt.Errorf("schedule rule is required")
	}
}

// String is the human-readable representation for logs and status pages.
func (schedule Schedule) String() string {
	if err := schedule.Validate(); err != nil {
		return "invalid schedule: " + err.Error()
	}
	zone := schedule.location.String()
	rule := schedule.rule
	switch rule.kind {
	case ruleDaily:
		return "daily at " + rule.at.String() + " (" + zone + ")"
	case ruleWeekly:
		return "weekly on " + rule.weekday.String() + " at " + rule.at.String() + " (" + zone + ")"
	case ruleMonthly:
		return fmt.Sprintf("monthly on day %d at %s (%s)", rule.monthDay, rule.at, zone)
	case ruleLastDayOfMonth:
		return "monthly on the last day at " + rule.at.String() + " (" + zone + ")"
	case ruleEvery:
		return "every " + rule.interval.String()
	case ruleRaw:
		canonical, _ := schedule.Canonical()
		return "cron " + canonical + " (" + zone + ")"
	default:
		return "invalid schedule"
	}
}

func (value TimeOfDay) String() string {
	hour := value.hour
	meridiem := "AM"
	if hour == 0 {
		hour = 12
	} else if hour == 12 {
		meridiem = "PM"
	} else if hour > 12 {
		hour -= 12
		meridiem = "PM"
	}
	return fmt.Sprintf("%d:%02d %s", hour, value.minute, meridiem)
}

// Next returns the first matching real instant strictly after after. Raw rules
// delegate five-field parsing and occurrence evaluation to robfig/cron. Typed
// calendar rules are bounded to five years so malformed state cannot spin.
func (schedule Schedule) Next(after time.Time) (time.Time, error) {
	if err := schedule.Validate(); err != nil {
		return time.Time{}, err
	}
	if schedule.rule.kind == ruleEvery {
		anchor := time.Unix(0, 0).UTC()
		if schedule.hasAnchor {
			anchor = schedule.anchor
		}
		if after.Before(anchor) {
			return anchor, nil
		}
		elapsed := after.UTC().Sub(anchor)
		steps := elapsed/schedule.rule.interval + 1
		return anchor.Add(steps * schedule.rule.interval), nil
	}
	if schedule.rule.kind == ruleRaw {
		parsed, err := parseRaw(schedule.rule.raw)
		if err != nil {
			return time.Time{}, err
		}
		return parsed.Next(after.In(schedule.location)).UTC(), nil
	}
	matcher, err := schedule.matcher()
	if err != nil {
		return time.Time{}, err
	}
	// Truncating in UTC avoids invalid local civil timestamps. Checking each
	// real minute gives the documented gap/fold behavior.
	candidate := after.UTC().Truncate(time.Minute).Add(time.Minute)
	deadline := candidate.AddDate(5, 0, 0)
	for !candidate.After(deadline) {
		if matcher(candidate.In(schedule.location)) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("schedule has no occurrence within five years")
}

func (schedule Schedule) matcher() (func(value time.Time) bool, error) {
	rule := schedule.rule
	switch rule.kind {
	case ruleDaily:
		return atMatcher(rule.at), nil
	case ruleWeekly:
		return func(value time.Time) bool { return value.Weekday() == rule.weekday && matchesAt(value, rule.at) }, nil
	case ruleMonthly:
		return func(value time.Time) bool { return value.Day() == rule.monthDay && matchesAt(value, rule.at) }, nil
	case ruleLastDayOfMonth:
		return func(value time.Time) bool { return value.Day() == lastDay(value) && matchesAt(value, rule.at) }, nil
	default:
		return nil, fmt.Errorf("unsupported schedule rule")
	}
}

func atMatcher(at TimeOfDay) func(value time.Time) bool {
	return func(value time.Time) bool { return matchesAt(value, at) }
}
func matchesAt(value time.Time, at TimeOfDay) bool {
	return value.Hour() == at.hour && value.Minute() == at.minute
}
func lastDay(value time.Time) int {
	return time.Date(value.Year(), value.Month()+1, 0, 0, 0, 0, 0, value.Location()).Day()
}
func validateTimeOfDay(value TimeOfDay) error {
	if value.err != nil {
		return value.err
	}
	if value.hour < 0 || value.hour > 23 || value.minute < 0 || value.minute > 59 {
		return fmt.Errorf("invalid time of day")
	}
	return nil
}

func parseRaw(expression string) (robfigcron.Schedule, error) {
	expression = normalizeRaw(expression)
	if len(expression) == 0 || len(expression) > 256 {
		return nil, fmt.Errorf("Raw: expression must contain 1 to 256 bytes")
	}
	parsed, err := robfigcron.NewParser(
		robfigcron.Minute |
			robfigcron.Hour |
			robfigcron.Dom |
			robfigcron.Month |
			robfigcron.Dow |
			robfigcron.Descriptor,
	).Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("Raw: %w", err)
	}
	return parsed, nil
}

func normalizeRaw(expression string) string {
	return strings.Join(strings.Fields(expression), " ")
}
