package contract_test

import (
	"errors"
	"time"

	cron "github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/cron/contract"
	cronv1 "github.com/candacelabs/csf/pkg/cron/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ = Describe("Liquid Proto schedule boundary", func() {
	It("round-trips the human-readable DSL without becoming a storage model", func() {
		chicago, err := time.LoadLocation("America/Chicago")
		Expect(err).NotTo(HaveOccurred())
		schedule := cron.Spec(cron.Weekly(time.Friday, cron.At(5, 45).PM())).In(chicago)

		message, err := contract.ScheduleToProto(schedule)
		Expect(err).NotTo(HaveOccurred())
		Expect(message.GetWeekly().GetAt().GetHour()).To(Equal(uint32(17)))
		Expect(message.GetTimezone()).To(Equal("America/Chicago"))

		rebuilt, err := contract.ScheduleFromProto(message)
		Expect(err).NotTo(HaveOccurred())
		Expect(rebuilt.String()).To(Equal(schedule.String()))
	})

	It("preserves an explicit durable interval anchor", func() {
		anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		message, err := contract.ScheduleToProto(cron.Spec(cron.Every(5 * time.Minute)).Anchor(anchor))
		Expect(err).NotTo(HaveOccurred())
		Expect(message.GetInterval().GetAnchor().AsTime()).To(Equal(anchor))

		rebuilt, err := contract.ScheduleFromProto(message)
		Expect(err).NotTo(HaveOccurred())
		actual, ok := rebuilt.IntervalAnchor()
		Expect(ok).To(BeTrue())
		Expect(actual).To(Equal(anchor))
	})

	It("rejects incomplete nested rules at the contract boundary", func() {
		message := &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule: &cronv1.ScheduleSpec_Daily{
				Daily: &cronv1.DailySchedule{},
			},
		}
		Expect(errors.Is(contract.ValidateSchedule(message), contract.ErrInvalid)).To(BeTrue())
	})

	It("requires the raw cron dialect explicitly", func() {
		message := &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule: &cronv1.ScheduleSpec_RawCron{RawCron: &cronv1.RawCronSchedule{
				Expression: "0 3 * * *",
			}},
		}
		Expect(contract.ValidateSchedule(message)).To(MatchError(ContainSubstring("dialect")))
	})

	It("validates every codec marshal and unmarshal", func() {
		codec, err := contract.NewScheduleCodec()
		Expect(err).NotTo(HaveOccurred())
		message, err := contract.ScheduleToProto(cron.Spec(cron.Daily(cron.At(3).PM())))
		Expect(err).NotTo(HaveOccurred())

		wire, err := codec.Marshal(message)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := codec.Unmarshal(wire)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(decoded, message)).To(BeTrue())
	})

	DescribeTable("round-trips each portable schedule rule",
		func(schedule cron.Schedule, inspect func(spec *cronv1.ScheduleSpec)) {
			message, err := contract.ScheduleToProto(schedule)
			Expect(err).NotTo(HaveOccurred())
			inspect(message)

			rebuilt, err := contract.ScheduleFromProto(message)
			Expect(err).NotTo(HaveOccurred())
			Expect(rebuilt.String()).To(Equal(schedule.String()))
		},
		Entry("daily", cron.Spec(cron.Daily(cron.Midnight())), func(message *cronv1.ScheduleSpec) {
			Expect(message.GetDaily()).NotTo(BeNil())
		}),
		Entry("monthly day", cron.Spec(cron.Monthly(31, cron.Noon())), func(message *cronv1.ScheduleSpec) {
			Expect(message.GetMonthlyDay().GetDayOfMonth()).To(Equal(uint32(31)))
		}),
		Entry("monthly last day", cron.Spec(cron.LastDayOfMonth(cron.At(11, 30).PM())), func(message *cronv1.ScheduleSpec) {
			Expect(message.GetMonthlyLastDay()).NotTo(BeNil())
		}),
		Entry("raw robfig expression", cron.Spec(cron.Raw("  0 3 * * *  ")), func(message *cronv1.ScheduleSpec) {
			Expect(message.GetRawCron().GetExpression()).To(Equal("0 3 * * *"))
		}),
	)

	It("rejects an invalid domain schedule before projection", func() {
		_, err := contract.ScheduleToProto(cron.Schedule{})
		Expect(err).To(MatchError(ContainSubstring("domain schedule")))
	})

	DescribeTable("rejects malformed portable schedule rules",
		func(message *cronv1.ScheduleSpec, want string) {
			_, err := contract.ScheduleFromProto(message)
			Expect(err).To(MatchError(ContainSubstring(want)))
			Expect(errors.Is(err, contract.ErrInvalid)).To(BeTrue())
		},
		Entry("nil schedule", nil, "schedule is required"),
		Entry("invalid timezone scalar", &cronv1.ScheduleSpec{}, "scalar refinements"),
		Entry("unknown timezone", &cronv1.ScheduleSpec{
			Timezone: "No/Such_Zone",
			Rule: &cronv1.ScheduleSpec_Daily{Daily: &cronv1.DailySchedule{
				At: &cronv1.TimeOfDay{},
			}},
		}, "timezone"),
		Entry("daily without time", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule:     &cronv1.ScheduleSpec_Daily{Daily: &cronv1.DailySchedule{}},
		}, "time of day is required"),
		Entry("daily with invalid time", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule: &cronv1.ScheduleSpec_Daily{Daily: &cronv1.DailySchedule{
				At: &cronv1.TimeOfDay{Hour: 24},
			}},
		}, "time of day"),
		Entry("nil weekly rule", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule:     &cronv1.ScheduleSpec_Weekly{},
		}, "weekly rule is required"),
		Entry("weekly without weekday", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule: &cronv1.ScheduleSpec_Weekly{Weekly: &cronv1.WeeklySchedule{
				At: &cronv1.TimeOfDay{},
			}},
		}, "weekday must be specified"),
		Entry("nil monthly day rule", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule:     &cronv1.ScheduleSpec_MonthlyDay{},
		}, "monthly_day rule is required"),
		Entry("invalid monthly day", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule: &cronv1.ScheduleSpec_MonthlyDay{MonthlyDay: &cronv1.MonthlyDaySchedule{
				At: &cronv1.TimeOfDay{},
			}},
		}, "monthly_day"),
		Entry("nil monthly last-day rule", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule:     &cronv1.ScheduleSpec_MonthlyLastDay{},
		}, "monthly_last_day rule is required"),
		Entry("nil interval rule", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule:     &cronv1.ScheduleSpec_Interval{},
		}, "interval rule is required"),
		Entry("invalid interval scalar", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule: &cronv1.ScheduleSpec_Interval{Interval: &cronv1.IntervalSchedule{
				IntervalNanoseconds: 1,
			}},
		}, "interval"),
		Entry("invalid interval anchor", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule: &cronv1.ScheduleSpec_Interval{Interval: &cronv1.IntervalSchedule{
				IntervalNanoseconds: int64(time.Minute),
				Anchor:              &timestamppb.Timestamp{Seconds: 253402300800},
			}},
		}, "interval anchor"),
		Entry("nil raw rule", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule:     &cronv1.ScheduleSpec_RawCron{},
		}, "raw_cron rule is required"),
		Entry("invalid raw scalar", &cronv1.ScheduleSpec{
			Timezone: "UTC",
			Rule:     &cronv1.ScheduleSpec_RawCron{RawCron: &cronv1.RawCronSchedule{}},
		}, "raw_cron"),
		Entry("missing rule", &cronv1.ScheduleSpec{Timezone: "UTC"}, "exactly one schedule rule"),
	)
})
