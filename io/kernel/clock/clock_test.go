// Copyright 2026 Candace Labs

package clock_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/kernel/clock"
)

var _ = Describe("the clock capability", func() {
	start := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)

	It("grants the host's clock as an IClock", func() {
		var granted clock.IClock = clock.NewSystemClock()
		before := time.Now()
		Expect(granted.Now()).To(BeTemporally(">=", before))
		Expect(granted.After(0)).To(Receive())
	})

	It("moves a manual clock only when advanced, firing waits in deadline order", func() {
		manual := clock.NewManualClock(start)
		var granted clock.IClock = manual
		Expect(granted.Now()).To(Equal(start))

		late := granted.After(time.Minute)
		early := granted.After(time.Second)
		Expect(manual.Waiting()).To(Equal(2))
		Consistently(early).ShouldNot(Receive())

		manual.Advance(time.Second)
		Expect(manual.Now()).To(Equal(start.Add(time.Second)))
		Expect(early).To(Receive(Equal(start.Add(time.Second))))
		Expect(late).NotTo(Receive())
		Expect(manual.Waiting()).To(Equal(1))

		manual.Advance(time.Hour)
		Expect(late).To(Receive(Equal(start.Add(time.Minute))), "a wait fires with its own deadline, not the clock's")
		Expect(manual.Now()).To(Equal(start.Add(time.Second + time.Hour)))
		Expect(manual.Waiting()).To(BeZero())
	})

	It("delivers a wait of no duration at once", func() {
		manual := clock.NewManualClock(start)
		Expect(manual.After(0)).To(Receive(Equal(start)))
		Expect(manual.After(-time.Second)).To(Receive(Equal(start)))
		Expect(manual.Waiting()).To(BeZero())
	})
})
