package testclock_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden/testclock"
)

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// recv does a non-blocking receive, reporting whether a value was ready.
func recv(ch <-chan time.Time) (time.Time, bool) {
	select {
	case t := <-ch:
		return t, true
	default:
		return time.Time{}, false
	}
}

var _ = Describe("testclock Clock", func() {
	// TestNowAndAdvance
	It("advances Now by exactly the requested duration", func() {
		clk := testclock.New(start)
		Expect(clk.Now()).To(BeTemporally("==", start))
		clk.Advance(3 * time.Second)
		Expect(clk.Now()).To(BeTemporally("==", start.Add(3*time.Second)))
	})

	// TestAfterFiresOnlyWhenDue
	It("After fires only once its deadline is reached", func() {
		clk := testclock.New(start)
		ch := clk.After(10 * time.Second)

		_, ok := recv(ch)
		Expect(ok).To(BeFalse(), "After fired before advancing")
		clk.Advance(5 * time.Second)
		_, ok = recv(ch)
		Expect(ok).To(BeFalse(), "After fired before its deadline")
		clk.Advance(5 * time.Second)
		got, ok := recv(ch)
		Expect(ok).To(BeTrue(), "After did not fire at its deadline")
		Expect(got).To(BeTemporally("==", start.Add(10*time.Second)))
	})

	// TestTimerStopPreventsFire
	It("Stop prevents an armed timer from firing", func() {
		clk := testclock.New(start)
		timer := clk.NewTimer(10 * time.Second)
		Expect(timer.Stop()).To(BeTrue(), "Stop on an armed timer should report true")
		clk.Advance(20 * time.Second)
		_, ok := recv(timer.C)
		Expect(ok).To(BeFalse(), "stopped timer must not fire")
		Expect(timer.Stop()).To(BeFalse(), "Stop on an already-stopped timer should report false")
	})

	// TestTimerReset
	It("Reset re-arms a timer to a new deadline", func() {
		clk := testclock.New(start)
		timer := clk.NewTimer(10 * time.Second)

		clk.Advance(5 * time.Second)  // now = +5, timer still armed for +10
		timer.Reset(10 * time.Second) // re-arm for now+10 = +15

		clk.Advance(5 * time.Second) // now = +10, not yet
		_, ok := recv(timer.C)
		Expect(ok).To(BeFalse(), "reset timer fired early")
		clk.Advance(5 * time.Second) // now = +15
		got, ok := recv(timer.C)
		Expect(ok).To(BeTrue(), "reset timer did not fire at new deadline")
		Expect(got).To(BeTemporally("==", start.Add(15*time.Second)))
	})

	// TestTickerRepeats
	It("Ticker repeats on interval and stops cleanly", func() {
		clk := testclock.New(start)
		ticker := clk.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for i := 1; i <= 3; i++ {
			clk.Advance(10 * time.Second)
			got, ok := recv(ticker.C)
			Expect(ok).To(BeTrue(), "ticker did not fire on interval %d", i)
			Expect(got).To(BeTemporally("==", start.Add(time.Duration(i)*10*time.Second)))
		}

		ticker.Stop()
		clk.Advance(10 * time.Second)
		_, ok := recv(ticker.C)
		Expect(ok).To(BeFalse(), "stopped ticker must not fire")
	})

	// TestChronologicalFireTimestamps
	It("delivers chronological fire timestamps", func() {
		clk := testclock.New(start)
		early := clk.NewTimer(10 * time.Second)
		late := clk.NewTimer(20 * time.Second)

		clk.Advance(30 * time.Second)

		e, ok := recv(early.C)
		Expect(ok).To(BeTrue(), "early timer did not fire")
		l, ok := recv(late.C)
		Expect(ok).To(BeTrue(), "late timer did not fire")
		Expect(e).To(BeTemporally("<", l), "early fire should precede late fire")
		Expect(e).To(BeTemporally("==", start.Add(10*time.Second)))
		Expect(l).To(BeTemporally("==", start.Add(20*time.Second)))
	})

	// TestBlockUntilTimers
	It("BlockUntilTimers waits for the expected registration count", func() {
		clk := testclock.New(start)

		// Already-satisfied case returns immediately.
		clk.NewTimer(time.Second)
		clk.BlockUntilTimers(1)

		// Arm the second timer from a goroutine; BlockUntilTimers(2) unblocks
		// once it is registered.
		go func() {
			defer GinkgoRecover()
			clk.NewTimer(2 * time.Second)
		}()
		clk.BlockUntilTimers(2)
		Expect(clk.ArmedTimers()).To(BeNumerically(">=", 2), "ArmedTimers should be >= 2 after BlockUntilTimers(2)")
	})

	// TestArmedTimersDecrementsOnFireAndStop
	It("ArmedTimers decrements on fire and on stop", func() {
		clk := testclock.New(start)
		clk.NewTimer(10 * time.Second) // one-shot
		ticker := clk.NewTicker(10 * time.Second)
		Expect(clk.ArmedTimers()).To(Equal(2))
		clk.Advance(10 * time.Second) // one-shot fires and is removed; ticker reschedules
		Expect(clk.ArmedTimers()).To(Equal(1), "ticker only after one-shot fire")
		ticker.Stop()
		Expect(clk.ArmedTimers()).To(Equal(0), "after ticker stop")
	})
})
