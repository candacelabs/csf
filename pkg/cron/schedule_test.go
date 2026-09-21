package cron_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cron "github.com/candacelabs/csf/pkg/cron"
)

var _ = Describe("Schedule", func() {
	It("reads as a typed, human-friendly daily declaration", func() {
		schedule := cron.Spec(cron.Daily(cron.At(3).PM()))

		Expect(schedule.Validate()).To(Succeed())
		canonical, err := schedule.Canonical()
		Expect(err).NotTo(HaveOccurred())
		Expect(canonical).To(Equal("0 15 * * *"))
		Expect(schedule.String()).To(Equal("daily at 3:00 PM (UTC)"))
	})

	It("builds weekly, monthly, and last-day schedules", func() {
		weekly := cron.Spec(cron.Weekly(time.Monday, cron.At(8, 30).AM()))
		monthly := cron.Spec(cron.Monthly(1, cron.Noon()))
		lastDay := cron.Spec(cron.LastDayOfMonth(cron.Midnight()))

		weeklyCanonical, err := weekly.Canonical()
		Expect(err).NotTo(HaveOccurred())
		Expect(weeklyCanonical).To(Equal("30 8 * * 1"))
		monthlyCanonical, err := monthly.Canonical()
		Expect(err).NotTo(HaveOccurred())
		Expect(monthlyCanonical).To(Equal("0 12 1 * *"))
		lastDayCanonical, err := lastDay.Canonical()
		Expect(err).NotTo(HaveOccurred())
		Expect(lastDayCanonical).To(Equal("0 0 L * *"))
	})

	It("renders every typed schedule and evaluates the last calendar day", func() {
		chicago, err := time.LoadLocation("America/Chicago")
		Expect(err).NotTo(HaveOccurred())
		weekly := cron.Spec(cron.Weekly(time.Friday, cron.At(5, 45).PM())).In(chicago)
		monthly := cron.Spec(cron.Monthly(31, cron.Noon()))
		lastDay := cron.Spec(cron.LastDayOfMonth(cron.Midnight()))
		every := cron.Spec(cron.Every(90 * time.Second))
		raw := cron.Spec(cron.Raw("0 6 * * *"))

		Expect(weekly.Location()).To(Equal(chicago))
		Expect(weekly.String()).To(Equal("weekly on Friday at 5:45 PM (America/Chicago)"))
		Expect(monthly.String()).To(Equal("monthly on day 31 at 12:00 PM (UTC)"))
		Expect(lastDay.String()).To(Equal("monthly on the last day at 12:00 AM (UTC)"))
		Expect(every.String()).To(Equal("every 1m30s"))
		Expect(raw.String()).To(Equal("cron 0 6 * * * (UTC)"))
		Expect(cron.Spec(cron.Rule{}).String()).To(ContainSubstring("invalid schedule"))

		next, err := lastDay.Next(time.Date(2026, time.January, 30, 23, 59, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC)))
		anchor := time.Date(2026, time.January, 31, 12, 0, 0, 0, time.UTC)
		next, err = cron.Spec(cron.Every(time.Hour)).Anchor(anchor).Next(anchor.Add(-time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(anchor))
	})

	It("reports invalid fluent declarations without panicking", func() {
		for _, schedule := range []cron.Schedule{
			cron.Spec(cron.Daily(cron.At(0).AM())),
			cron.Spec(cron.Daily(cron.At(3, -1).PM())),
			cron.Spec(cron.Daily(cron.At(3, 10, 20).AM())),
			cron.Spec(cron.Daily(cron.At24(-1))),
			cron.Spec(cron.Daily(cron.At24(24))),
			cron.Spec(cron.Daily(cron.At24(3, -1))),
			cron.Spec(cron.Daily(cron.At24(3, 10, 20))),
			cron.Spec(cron.Weekly(time.Weekday(7), cron.Noon())),
			cron.Spec(cron.Monthly(0, cron.Noon())),
			cron.Spec(cron.Monthly(32, cron.Noon())),
			cron.Spec(cron.Every(0)),
			cron.Spec(cron.Raw("")),
			cron.Spec(cron.Rule{}),
			cron.Spec(cron.Daily(cron.Noon())).In(nil),
		} {
			Expect(schedule.Validate()).NotTo(Succeed())
		}

		Expect(cron.Spec(cron.Daily(cron.At(12).AM())).String()).To(Equal("daily at 12:00 AM (UTC)"))
		Expect(cron.Spec(cron.Daily(cron.At(12).PM())).String()).To(Equal("daily at 12:00 PM (UTC)"))
	})

	It("rejects incomplete or contradictory durable definitions", func() {
		_, err := cron.ScheduleFromDefinition(cron.ScheduleDefinition{Kind: cron.ScheduleKindDaily})
		Expect(err).To(MatchError(ContainSubstring("timezone is required")))
		_, err = cron.ScheduleFromDefinition(cron.ScheduleDefinition{Kind: cron.ScheduleKindDaily, Timezone: "not/a-zone"})
		Expect(err).To(MatchError(ContainSubstring("definition timezone")))
		_, err = cron.ScheduleFromDefinition(cron.ScheduleDefinition{Kind: "unknown", Timezone: "UTC"})
		Expect(err).To(MatchError(ContainSubstring("unknown schedule definition kind")))
		_, err = cron.ScheduleFromDefinition(cron.ScheduleDefinition{
			Kind:      cron.ScheduleKindDaily,
			Timezone:  "UTC",
			Canonical: "0 4 * * *",
			Hour:      3,
		})
		Expect(err).To(MatchError(ContainSubstring("does not match normalized")))
		_, err = cron.ScheduleFromDefinition(cron.ScheduleDefinition{
			Kind:      cron.ScheduleKindEvery,
			Timezone:  "UTC",
			Interval:  time.Minute,
			HasAnchor: true,
		})
		Expect(err).To(MatchError(ContainSubstring("anchor must not be zero")))
	})

	It("keeps invalid fluent values safe until normal validation", func() {
		schedule := cron.Spec(cron.Daily(cron.At(13).PM()))

		Expect(schedule.Validate()).To(MatchError(ContainSubstring("between 1 and 12")))
		_, err := schedule.Next(time.Now())
		Expect(err).To(MatchError(ContainSubstring("between 1 and 12")))
	})

	It("requires a portable named timezone for durable schedules", func() {
		Expect(cron.Spec(cron.Daily(cron.Noon())).In(time.Local).Validate()).To(
			MatchError(ContainSubstring("explicit portable timezone")),
		)
		Expect(cron.Spec(cron.Daily(cron.Noon())).In(time.FixedZone("custom-offset", 3600)).Validate()).To(
			MatchError(ContainSubstring("not portable")),
		)
	})

	It("normalizes raw five-field syntax and descriptors", func() {
		raw := cron.Spec(cron.Raw("*/15 2-3 * * 1,3"))
		daily := cron.Spec(cron.Raw("@daily"))

		canonical, err := raw.Canonical()
		Expect(err).NotTo(HaveOccurred())
		Expect(canonical).To(Equal("*/15 2-3 * * 1,3"))
		dailyCanonical, err := daily.Canonical()
		Expect(err).NotTo(HaveOccurred())
		Expect(dailyCanonical).To(Equal("@daily"))
	})

	It("delegates raw parsing and next-occurrence semantics to robfig cron", func() {
		// Standard cron treats restricted day-of-month and day-of-week fields as
		// alternatives: this is the first Monday after the reference instant,
		// rather than waiting for the first day of the following month.
		schedule := cron.Spec(cron.Raw("0 0 1 * 1"))

		next, err := schedule.Next(time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)))
		Expect(cron.Spec(cron.Raw("not a cron expression")).Validate()).To(MatchError(ContainSubstring("Raw:")))
	})

	It("uses a durable interval anchor instead of process start", func() {
		anchor := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		schedule := cron.Spec(cron.Every(5 * time.Minute)).Anchor(anchor)

		next, err := schedule.Next(anchor.Add(12*time.Minute + time.Second))
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(anchor.Add(15 * time.Minute)))
		storedAnchor, ok := schedule.IntervalAnchor()
		Expect(ok).To(BeTrue())
		Expect(storedAnchor).To(Equal(anchor))
	})

	It("normalizes interval anchors to durable PostgreSQL precision", func() {
		anchor := time.Date(2026, time.August, 10, 12, 0, 0, 1234, time.FixedZone("offset", 3600))
		schedule := cron.Spec(cron.Every(time.Minute)).Anchor(anchor)

		storedAnchor, ok := schedule.IntervalAnchor()
		Expect(ok).To(BeTrue())
		Expect(storedAnchor).To(Equal(anchor.UTC().Truncate(time.Microsecond)))
		definition, err := schedule.Definition()
		Expect(err).NotTo(HaveOccurred())
		Expect(definition.Anchor).To(Equal(storedAnchor))
	})

	It("keeps interval cadence representable by the durable PostgreSQL clock", func() {
		Expect(cron.Spec(cron.Every(time.Nanosecond)).Validate()).To(
			MatchError(ContainSubstring("whole number of microseconds")),
		)
		Expect(cron.Spec(cron.Every(time.Microsecond + time.Nanosecond)).Validate()).To(
			MatchError(ContainSubstring("whole number of microseconds")),
		)
	})

	It("rejects anchors that cannot have durable interval semantics", func() {
		Expect(cron.Spec(cron.Daily(cron.Noon())).Anchor(time.Now()).Validate()).To(
			MatchError(ContainSubstring("only valid for Every")),
		)
		Expect(cron.Spec(cron.Every(time.Minute)).Anchor(time.Time{}).Validate()).To(
			MatchError(ContainSubstring("must not be zero")),
		)
	})

	It("round-trips a neutral definition without SQLC or protobuf types", func() {
		chicago, err := time.LoadLocation("America/Chicago")
		Expect(err).NotTo(HaveOccurred())
		original := cron.Spec(cron.Weekly(time.Friday, cron.At(5, 45).PM())).In(chicago)

		definition, err := original.Definition()
		Expect(err).NotTo(HaveOccurred())
		Expect(definition.Kind).To(Equal(cron.ScheduleKindWeekly))
		Expect(definition.Canonical).To(Equal("45 17 * * 5"))
		Expect(definition.Timezone).To(Equal("America/Chicago"))

		rebuilt, err := cron.ScheduleFromDefinition(definition)
		Expect(err).NotTo(HaveOccurred())
		Expect(rebuilt.String()).To(Equal(original.String()))
	})

	It("uses explicit real-instant DST semantics", func() {
		newYork, err := time.LoadLocation("America/New_York")
		Expect(err).NotTo(HaveOccurred())
		schedule := cron.Spec(cron.Daily(cron.At(2, 30).AM())).In(newYork)

		// 2:30 AM never occurs locally on the spring-forward date.
		next, err := schedule.Next(time.Date(2026, time.March, 8, 6, 0, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(time.Date(2026, time.March, 9, 6, 30, 0, 0, time.UTC)))

		fold := cron.Spec(cron.Daily(cron.At(1, 30).AM())).In(newYork)
		first, err := fold.Next(time.Date(2026, time.November, 1, 4, 0, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
		second, err := fold.Next(first)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(Equal(time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC)))
		Expect(second).To(Equal(time.Date(2026, time.November, 1, 6, 30, 0, 0, time.UTC)))
	})
})
