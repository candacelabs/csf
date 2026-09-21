package warden_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// Freezes the semantic contract of NewRealClock's Timer/Ticker, as promised by
// the doc comments ("same semantics as time.Timer.Stop", "mirrors time.Ticker").
// The election and watchdog state machines rely on these semantics, so a
// regression here silently breaks liveness. Durations are kept small; the
// assertions use Eventually/Consistently rather than fixed sleeps to stay
// non-flaky under load.

var _ = Describe("NewRealClock", func() {
	var clock warden.IClock

	BeforeEach(func() {
		clock = warden.NewRealClock()
	})

	Describe("Now", func() {
		It("advances strictly forward over real elapsed time", func() {
			t1 := clock.Now()
			// A frozen or backwards clock would never satisfy this.
			Eventually(func() bool { return clock.Now().After(t1) }, "1s", "1ms").Should(BeTrue())
		})
	})

	Describe("After", func() {
		It("delivers exactly one time value once the duration elapses", func() {
			ch := clock.After(5 * time.Millisecond)
			var got time.Time
			Eventually(ch, "1s").Should(Receive(&got))
			Expect(got).NotTo(BeZero())
			// A one-shot channel does not fire again.
			Consistently(ch, "50ms").ShouldNot(Receive())
		})
	})

	Describe("Timer", func() {
		It("fires on its channel after the duration", func() {
			t := clock.NewTimer(5 * time.Millisecond)
			Eventually(t.C, "1s").Should(Receive())
		})

		It("Stop() on a still-armed timer reports true and prevents the fire", func() {
			t := clock.NewTimer(time.Hour) // long enough to never fire in-test
			Expect(t.Stop()).To(BeTrue())
			Consistently(t.C, "50ms").ShouldNot(Receive())
		})

		It("Stop() on an already-stopped timer reports false", func() {
			t := clock.NewTimer(time.Hour)
			Expect(t.Stop()).To(BeTrue())
			Expect(t.Stop()).To(BeFalse())
		})

		It("Reset() re-arms a stopped timer so it fires again", func() {
			t := clock.NewTimer(time.Hour)
			Expect(t.Stop()).To(BeTrue())
			// Reset on a stopped/drained timer is the documented safe usage.
			t.Reset(5 * time.Millisecond)
			Eventually(t.C, "1s").Should(Receive())
		})
	})

	Describe("Ticker", func() {
		It("fires repeatedly at its interval", func() {
			tk := clock.NewTicker(5 * time.Millisecond)
			defer tk.Stop()
			// At least two ticks confirms it is periodic, not one-shot.
			Eventually(tk.C, "1s").Should(Receive())
			Eventually(tk.C, "1s").Should(Receive())
		})

		It("stops delivering ticks after Stop()", func() {
			tk := clock.NewTicker(5 * time.Millisecond)
			Eventually(tk.C, "1s").Should(Receive())
			tk.Stop()
			// Drain any tick already buffered at the moment of Stop, then assert
			// quiescence.
			select {
			case <-tk.C:
			default:
			}
			Consistently(tk.C, "60ms").ShouldNot(Receive())
		})
	})
})
