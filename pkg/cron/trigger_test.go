package cron_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cron "github.com/candacelabs/csf/pkg/cron"
)

var _ = Describe("Trigger declarations", func() {
	It("accepts the names the store's check constraint accepts and nothing else", func() {
		for _, name := range []string{"hourly", "ouroboros.retrieve", "chat/00000000", "a-b_c"} {
			Expect(cron.ValidateTriggerName(name)).To(Succeed(), name)
		}
		for _, name := range []string{"Hourly", "hourly:rollup", "with space", "", "1st"} {
			Expect(cron.ValidateTriggerName(name)).To(MatchError(cron.ErrInvalidTrigger), name)
		}
	})

	It("anchors an interval declaration at normalization and keeps an established anchor", func() {
		now := time.Date(2026, time.August, 10, 12, 0, 0, 500, time.UTC)
		schedule, err := cron.Spec(cron.Every(time.Minute)).Definition()
		Expect(err).NotTo(HaveOccurred())
		declared := cron.TriggerDefinition{Name: "rollup", Schedule: schedule, CatchUp: cron.CatchUpNone, Overlap: cron.OverlapSkip}

		normalized, anchored, err := cron.NormalizeTriggerDefinition(declared, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(normalized.Schedule.HasAnchor).To(BeTrue())
		Expect(normalized.Schedule.Anchor).To(Equal(now.Truncate(time.Microsecond)), "anchors keep PostgreSQL's precision")
		next, err := anchored.Next(now)
		Expect(err).NotTo(HaveOccurred())
		Expect(next).To(Equal(now.Truncate(time.Microsecond).Add(time.Minute)))

		kept := cron.PreserveIntervalAnchor(declared, normalized)
		Expect(kept.Schedule.Anchor).To(Equal(normalized.Schedule.Anchor))
		changed := declared
		changed.Schedule.Interval = 2 * time.Minute
		changed.Schedule.Canonical = "@every 2m0s"
		Expect(cron.PreserveIntervalAnchor(changed, normalized).Schedule.HasAnchor).To(BeFalse(), "a changed interval starts a new cadence")
	})

	It("rejects an invalid policy or schedule definition as an invalid trigger", func() {
		now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		schedule, err := cron.Spec(cron.Daily(cron.Noon())).Definition()
		Expect(err).NotTo(HaveOccurred())

		_, _, err = cron.NormalizeTriggerDefinition(cron.TriggerDefinition{
			Name: "rollup", Schedule: schedule, CatchUp: cron.CatchUpPolicy("sometimes"), Overlap: cron.OverlapSkip,
		}, now)
		Expect(err).To(MatchError(cron.ErrInvalidTrigger))

		_, _, err = cron.NormalizeTriggerDefinition(cron.TriggerDefinition{
			Name: "rollup", Schedule: cron.ScheduleDefinition{Kind: cron.ScheduleKindRaw, Timezone: "UTC", Canonical: "not a cron line"},
			CatchUp: cron.CatchUpNone, Overlap: cron.OverlapSkip,
		}, now)
		Expect(err).To(MatchError(cron.ErrInvalidTrigger))
	})

	It("identifies an occurrence by its trigger and instant, in UTC", func() {
		scheduledAt := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		identity := cron.OccurrenceID("rollup", scheduledAt)
		Expect(identity).To(HavePrefix("occ_"))
		Expect(identity).To(Equal(cron.OccurrenceID("rollup", scheduledAt.In(time.FixedZone("elsewhere", 3600)))))
		Expect(identity).NotTo(Equal(cron.OccurrenceID("rollup", scheduledAt.Add(time.Minute))))
		Expect(identity).NotTo(Equal(cron.OccurrenceID("other", scheduledAt)))
		Expect(cron.OccurrenceSkipped.Completion()).To(BeFalse())
		Expect(cron.OccurrenceCanceled.Completion()).To(BeTrue())
	})
})
