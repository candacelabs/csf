package testclock_test

// Contract tests for the deterministic testclock, the shared foundation every
// timing-dependent warden test stands on. If the fake clock's Timer/Ticker
// semantics drift from real time.Timer/time.Ticker, the election and watchdog
// suites silently stop meaning what they claim, so these freeze the behaviours
// the docs promise: manual Advance, chronological firing, Stop/Reset semantics,
// the panic on a non-positive ticker, and BlockUntilTimers.

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/testclock"
)

func TestTestclockContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "testclock contract suite")
}

var tcStart = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

var _ = Describe("testclock.Clock", func() {
	It("implements warden.IClock", func() {
		var _ warden.IClock = testclock.New(tcStart)
	})

	Describe("Now / Advance", func() {
		It("starts at the constructed instant and only moves via Advance", func() {
			c := testclock.New(tcStart)
			Expect(c.Now()).To(Equal(tcStart))
			c.Advance(3 * time.Second)
			Expect(c.Now()).To(Equal(tcStart.Add(3 * time.Second)))
		})
	})

	Describe("NewTimer", func() {
		It("does not fire before its deadline", func() {
			c := testclock.New(tcStart)
			tm := c.NewTimer(10 * time.Millisecond)
			c.Advance(9 * time.Millisecond)
			Consistently(tm.C, "20ms").ShouldNot(Receive())
		})

		It("fires exactly once at/after its deadline, delivering the fire time", func() {
			c := testclock.New(tcStart)
			tm := c.NewTimer(10 * time.Millisecond)
			c.Advance(10 * time.Millisecond)
			var got time.Time
			Eventually(tm.C).Should(Receive(&got))
			Expect(got).To(Equal(tcStart.Add(10 * time.Millisecond)))
			// One-shot: no second fire even after more time passes.
			c.Advance(time.Second)
			Consistently(tm.C, "20ms").ShouldNot(Receive())
		})

		It("Stop() on an armed timer returns true and prevents the fire", func() {
			c := testclock.New(tcStart)
			tm := c.NewTimer(10 * time.Millisecond)
			Expect(tm.Stop()).To(BeTrue())
			c.Advance(time.Second)
			Consistently(tm.C, "20ms").ShouldNot(Receive())
		})

		It("Stop() on an already-stopped timer returns false", func() {
			c := testclock.New(tcStart)
			tm := c.NewTimer(10 * time.Millisecond)
			Expect(tm.Stop()).To(BeTrue())
			Expect(tm.Stop()).To(BeFalse())
		})

		It("Reset() re-arms a stopped timer to fire again", func() {
			c := testclock.New(tcStart)
			tm := c.NewTimer(10 * time.Millisecond)
			Expect(tm.Stop()).To(BeTrue())
			Expect(tm.Reset(5 * time.Millisecond)).To(BeFalse()) // was not armed
			c.Advance(5 * time.Millisecond)
			Eventually(tm.C).Should(Receive())
		})
	})

	Describe("chronological firing", func() {
		It("fires only the timers whose deadline the advance has reached", func() {
			c := testclock.New(tcStart)
			early := c.NewTimer(10 * time.Millisecond)
			late := c.NewTimer(30 * time.Millisecond)

			c.Advance(15 * time.Millisecond)
			Eventually(early.C).Should(Receive())
			Consistently(late.C, "20ms").ShouldNot(Receive())

			c.Advance(20 * time.Millisecond) // now at 35ms total
			Eventually(late.C).Should(Receive())
		})
	})

	Describe("NewTicker", func() {
		It("fires on every interval when the channel is drained between ticks", func() {
			c := testclock.New(tcStart)
			tk := c.NewTicker(10 * time.Millisecond)
			defer tk.Stop()
			// The tick channel is buffered size 1 and drops undrained ticks (same
			// as real time.Ticker), so a periodic ticker is exercised by advancing
			// one interval at a time and draining between.
			c.Advance(10 * time.Millisecond)
			Eventually(tk.C).Should(Receive())
			c.Advance(10 * time.Millisecond)
			Eventually(tk.C).Should(Receive())
			c.Advance(10 * time.Millisecond)
			Eventually(tk.C).Should(Receive())
		})

		It("drops an undrained tick across a wide Advance (size-1 buffer, like time.Ticker)", func() {
			c := testclock.New(tcStart)
			tk := c.NewTicker(10 * time.Millisecond)
			defer tk.Stop()
			c.Advance(35 * time.Millisecond) // three ticks fire, but none are drained
			// Exactly one tick is buffered; the rest were dropped.
			Eventually(tk.C).Should(Receive())
			Consistently(tk.C, "20ms").ShouldNot(Receive())
		})

		It("stops firing after Stop()", func() {
			c := testclock.New(tcStart)
			tk := c.NewTicker(10 * time.Millisecond)
			c.Advance(10 * time.Millisecond)
			Eventually(tk.C).Should(Receive())
			tk.Stop()
			select {
			case <-tk.C:
			default:
			}
			c.Advance(time.Second)
			Consistently(tk.C, "20ms").ShouldNot(Receive())
		})

		It("panics on a non-positive interval, mirroring time.NewTicker", func() {
			c := testclock.New(tcStart)
			Expect(func() { c.NewTicker(0) }).To(Panic())
			Expect(func() { c.NewTicker(-1) }).To(Panic())
		})
	})

	Describe("After", func() {
		It("delivers the fire time once the duration elapses", func() {
			c := testclock.New(tcStart)
			ch := c.After(10 * time.Millisecond)
			c.Advance(10 * time.Millisecond)
			var got time.Time
			Eventually(ch).Should(Receive(&got))
			Expect(got).To(Equal(tcStart.Add(10 * time.Millisecond)))
		})
	})

	Describe("timer bookkeeping", func() {
		It("counts armed waiters and releases BlockUntilTimers at the threshold", func() {
			c := testclock.New(tcStart)
			Expect(c.ArmedTimers()).To(Equal(0))
			_ = c.NewTimer(time.Hour)
			_ = c.NewTicker(time.Hour)
			Expect(c.ArmedTimers()).To(Equal(2))
			// Already satisfied: returns without blocking.
			done := make(chan struct{})
			go func() { c.BlockUntilTimers(2); close(done) }()
			Eventually(done).Should(BeClosed())
		})

		It("BlockUntilTimers unblocks once enough timers are later armed", func() {
			c := testclock.New(tcStart)
			done := make(chan struct{})
			go func() { c.BlockUntilTimers(1); close(done) }()
			Consistently(done, "30ms").ShouldNot(BeClosed())
			_ = c.NewTimer(time.Hour)
			Eventually(done).Should(BeClosed())
		})
	})
})
