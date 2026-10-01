// Package contract maps the cron domain model to validated Liquid Proto
// messages at HTTP and messaging boundaries. PostgreSQL persistence lives in
// the sibling postgres package and never stores these wire representations.
package contract

import (
	"errors"
	"fmt"
	"time"

	cron "github.com/candacelabs/csf/pkg/cron"
	cronv1 "github.com/candacelabs/csf/pkg/cron/v1"
	"github.com/candacelabs/csf/pkg/liquidproto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrInvalid identifies a cron boundary message that fails either a generated
// Liquid scalar refinement or the semantic checks spanning nested fields.
var ErrInvalid = errors.New("cron contract: invalid message")

// NewScheduleCodec returns a deterministic, validating codec suitable for a
// message bus or cache boundary. Its wire bytes are not a database format.
func NewScheduleCodec() (*liquidproto.Codec[*cronv1.ScheduleSpec], error) {
	return liquidproto.NewCodec(
		func() *cronv1.ScheduleSpec { return new(cronv1.ScheduleSpec) },
		ValidateSchedule,
	)
}

// ValidateSchedule applies generated Liquid refinements and the complete
// nested schedule contract.
func ValidateSchedule(message *cronv1.ScheduleSpec) error {
	_, err := ScheduleFromProto(message)
	return err
}

// ScheduleToProto projects an immutable domain schedule into its portable
// boundary representation.
func ScheduleToProto(schedule cron.Schedule) (*cronv1.ScheduleSpec, error) {
	definition, err := schedule.Definition()
	if err != nil {
		return nil, fmt.Errorf("%w: domain schedule: %v", ErrInvalid, err)
	}

	message := &cronv1.ScheduleSpec{Timezone: definition.Timezone}
	at := func() *cronv1.TimeOfDay {
		return &cronv1.TimeOfDay{Hour: uint32(definition.Hour), Minute: uint32(definition.Minute)}
	}
	switch definition.Kind {
	case cron.ScheduleKindDaily:
		message.Rule = &cronv1.ScheduleSpec_Daily{Daily: &cronv1.DailySchedule{At: at()}}
	case cron.ScheduleKindWeekly:
		weekday, err := weekdayToProto(definition.Weekday)
		if err != nil {
			return nil, err
		}
		message.Rule = &cronv1.ScheduleSpec_Weekly{Weekly: &cronv1.WeeklySchedule{Weekday: weekday, At: at()}}
	case cron.ScheduleKindMonthly:
		message.Rule = &cronv1.ScheduleSpec_MonthlyDay{MonthlyDay: &cronv1.MonthlyDaySchedule{
			DayOfMonth: uint32(definition.MonthDay),
			At:         at(),
		}}
	case cron.ScheduleKindLastDayOfMonth:
		message.Rule = &cronv1.ScheduleSpec_MonthlyLastDay{MonthlyLastDay: &cronv1.MonthlyLastDaySchedule{At: at()}}
	case cron.ScheduleKindEvery:
		interval := &cronv1.IntervalSchedule{IntervalNanoseconds: int64(definition.Interval)}
		if definition.HasAnchor {
			interval.Anchor = timestamppb.New(definition.Anchor)
		}
		message.Rule = &cronv1.ScheduleSpec_Interval{Interval: interval}
	case cron.ScheduleKindRaw:
		message.Rule = &cronv1.ScheduleSpec_RawCron{RawCron: &cronv1.RawCronSchedule{
			Dialect:    cronv1.CronDialect_CRON_DIALECT_ROBFIG_STANDARD,
			Expression: definition.Canonical,
		}}
	default:
		return nil, fmt.Errorf("%w: unsupported schedule kind %q", ErrInvalid, definition.Kind)
	}
	if err := ValidateSchedule(message); err != nil {
		return nil, err
	}
	return message, nil
}

// ScheduleFromProto validates and maps a boundary message into the domain
// model without allowing protobuf types to leak into persistence.
func ScheduleFromProto(message *cronv1.ScheduleSpec) (cron.Schedule, error) {
	if message == nil {
		return cron.Schedule{}, fmt.Errorf("%w: schedule is required", ErrInvalid)
	}
	if err := cronv1.ValidateScheduleSpec(message); err != nil {
		return cron.Schedule{}, fmt.Errorf("%w: scalar refinements: %w", ErrInvalid, err)
	}
	location, err := time.LoadLocation(message.GetTimezone())
	if err != nil {
		return cron.Schedule{}, fmt.Errorf("%w: timezone: %v", ErrInvalid, err)
	}

	var schedule cron.Schedule
	switch rule := message.GetRule().(type) {
	case *cronv1.ScheduleSpec_Daily:
		at, err := timeOfDay(rule.Daily.GetAt())
		if err != nil {
			return cron.Schedule{}, err
		}
		schedule = cron.Spec(cron.Daily(at))
	case *cronv1.ScheduleSpec_Weekly:
		if rule.Weekly == nil {
			return cron.Schedule{}, fmt.Errorf("%w: weekly rule is required", ErrInvalid)
		}
		weekday, err := weekdayFromProto(rule.Weekly.GetWeekday())
		if err != nil {
			return cron.Schedule{}, err
		}
		at, err := timeOfDay(rule.Weekly.GetAt())
		if err != nil {
			return cron.Schedule{}, err
		}
		schedule = cron.Spec(cron.Weekly(weekday, at))
	case *cronv1.ScheduleSpec_MonthlyDay:
		if rule.MonthlyDay == nil {
			return cron.Schedule{}, fmt.Errorf("%w: monthly_day rule is required", ErrInvalid)
		}
		if err := cronv1.ValidateMonthlyDaySchedule(rule.MonthlyDay); err != nil {
			return cron.Schedule{}, fmt.Errorf("%w: monthly_day: %w", ErrInvalid, err)
		}
		at, err := timeOfDay(rule.MonthlyDay.GetAt())
		if err != nil {
			return cron.Schedule{}, err
		}
		schedule = cron.Spec(cron.Monthly(int(rule.MonthlyDay.GetDayOfMonth()), at))
	case *cronv1.ScheduleSpec_MonthlyLastDay:
		if rule.MonthlyLastDay == nil {
			return cron.Schedule{}, fmt.Errorf("%w: monthly_last_day rule is required", ErrInvalid)
		}
		at, err := timeOfDay(rule.MonthlyLastDay.GetAt())
		if err != nil {
			return cron.Schedule{}, err
		}
		schedule = cron.Spec(cron.LastDayOfMonth(at))
	case *cronv1.ScheduleSpec_Interval:
		if rule.Interval == nil {
			return cron.Schedule{}, fmt.Errorf("%w: interval rule is required", ErrInvalid)
		}
		if err := cronv1.ValidateIntervalSchedule(rule.Interval); err != nil {
			return cron.Schedule{}, fmt.Errorf("%w: interval: %w", ErrInvalid, err)
		}
		schedule = cron.Spec(cron.Every(time.Duration(rule.Interval.GetIntervalNanoseconds())))
		if rule.Interval.GetAnchor() != nil {
			if err := rule.Interval.GetAnchor().CheckValid(); err != nil {
				return cron.Schedule{}, fmt.Errorf("%w: interval anchor: %v", ErrInvalid, err)
			}
			schedule = schedule.Anchor(rule.Interval.GetAnchor().AsTime())
		}
	case *cronv1.ScheduleSpec_RawCron:
		if rule.RawCron == nil {
			return cron.Schedule{}, fmt.Errorf("%w: raw_cron rule is required", ErrInvalid)
		}
		if err := cronv1.ValidateRawCronSchedule(rule.RawCron); err != nil {
			return cron.Schedule{}, fmt.Errorf("%w: raw_cron: %w", ErrInvalid, err)
		}
		if rule.RawCron.GetDialect() != cronv1.CronDialect_CRON_DIALECT_ROBFIG_STANDARD {
			return cron.Schedule{}, fmt.Errorf("%w: raw_cron dialect must be ROBFIG_STANDARD", ErrInvalid)
		}
		schedule = cron.Spec(cron.Raw(rule.RawCron.GetExpression()))
	case nil:
		return cron.Schedule{}, fmt.Errorf("%w: exactly one schedule rule is required", ErrInvalid)
	default:
		return cron.Schedule{}, fmt.Errorf("%w: unsupported schedule rule %T", ErrInvalid, rule)
	}

	schedule = schedule.In(location)
	if err := schedule.Validate(); err != nil {
		return cron.Schedule{}, fmt.Errorf("%w: schedule semantics: %v", ErrInvalid, err)
	}
	return schedule, nil
}

func timeOfDay(message *cronv1.TimeOfDay) (cron.TimeOfDay, error) {
	if message == nil {
		return cron.TimeOfDay{}, fmt.Errorf("%w: time of day is required", ErrInvalid)
	}
	if err := cronv1.ValidateTimeOfDay(message); err != nil {
		return cron.TimeOfDay{}, fmt.Errorf("%w: time of day: %w", ErrInvalid, err)
	}
	return cron.At24(int(message.GetHour()), int(message.GetMinute())), nil
}

func weekdayToProto(day time.Weekday) (cronv1.Weekday, error) {
	if day < time.Sunday || day > time.Saturday {
		return cronv1.Weekday_WEEKDAY_UNSPECIFIED, fmt.Errorf("%w: unsupported weekday %d", ErrInvalid, day)
	}
	return cronv1.Weekday(int32(day) + 1), nil
}

func weekdayFromProto(day cronv1.Weekday) (time.Weekday, error) {
	if day < cronv1.Weekday_WEEKDAY_SUNDAY || day > cronv1.Weekday_WEEKDAY_SATURDAY {
		return 0, fmt.Errorf("%w: weekday must be specified", ErrInvalid)
	}
	return time.Weekday(int(day) - 1), nil
}
