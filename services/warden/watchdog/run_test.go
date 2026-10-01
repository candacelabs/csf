package watchdog

import (
	"context"
	"errors"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/pkg/patience"
	"github.com/candacelabs/csf/services/warden"
)

// schedulerBudget is what these specifications are willing to wait for the
// scheduler, and nothing else. No time-based watchdog logic depends on it —
// that logic is exercised deterministically through the fake clock — so the
// only thing this budget guards against is a goroutine that never runs at
// all, and a generous number there costs a slow failure on a test that was
// going to fail anyway.
var schedulerBudget = patience.Budget{Within: 30 * time.Second, Interval: time.Millisecond}

func runReturns(done <-chan error) error {
	GinkgoHelper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		Fail("Run did not return after ctx cancel")
		return nil
	}
}

var _ = Describe("Watchdog.Run", func() {
	// TestRunSubscriptionTriggersEvaluation: a view change delivered on the
	// subscription channel triggers evaluation, and re-delivering the same state
	// does not re-notify.
	It("evaluates on a subscription change and does not re-notify identical state", func() {
		src := newFakeSource()
		ctrl := gomock.NewController(GinkgoT())
		mock, rec := recordingNotifier(ctrl)
		clk := newFakeClock(baseTime)
		w := New(Config{CheckInterval: time.Hour}, src, mock, clk)

		src.set(followerView(7, "other-node"))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()

		// The startup evaluation reads the follower view first.
		patience.Await(GinkgoTB(), "the startup evaluation to read a view", schedulerBudget,
			src.views, func(views int) bool { return views >= 1 })

		seen := baseTime.Add(-time.Minute)
		dead := leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen))
		src.push(dead)
		patience.Await(GinkgoTB(), "the dead peer's one notification", schedulerBudget,
			rec.Sent, func(sent []warden.Incident) bool { return len(sent) == 1 })

		// Re-deliver the same view; the episode is open, so no new notification.
		before := src.views()
		src.push(dead)
		patience.Await(GinkgoTB(), "the re-delivered view to be evaluated", schedulerBudget,
			src.views, func(views int) bool { return views > before })
		Expect(rec.Sent()).To(HaveLen(1), "re-delivered view must not re-notify")
		Expect(w.Incidents()).To(HaveLen(1), "incident recorded once")

		cancel()
		Expect(errors.Is(runReturns(done), context.Canceled)).To(BeTrue(), "Run should return context.Canceled")
	})

	// TestRunTickerTriggersEvaluation: the CheckInterval ticker triggers
	// evaluation even without a subscription signal.
	It("evaluates on the CheckInterval tick without a subscription signal", func() {
		src := newFakeSource()
		ctrl := gomock.NewController(GinkgoT())
		mock, rec := recordingNotifier(ctrl)
		clk := newFakeClock(baseTime)
		w := New(Config{CheckInterval: time.Second}, src, mock, clk)

		src.set(followerView(7, "other-node"))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()

		// Ensure the startup evaluation has read the follower view; after this the
		// only thing that can trigger another evaluation is the tick (we never
		// signal the subscription).
		patience.Await(GinkgoTB(), "the startup evaluation to read a view", schedulerBudget,
			src.views, func(views int) bool { return views >= 1 })

		seen := baseTime.Add(-time.Minute)
		src.set(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen)))
		clk.tick()

		patience.Await(GinkgoTB(), "the tick's one notification", schedulerBudget,
			rec.Sent, func(sent []warden.Incident) bool { return len(sent) == 1 })

		cancel()
		Expect(errors.Is(runReturns(done), context.Canceled)).To(BeTrue(), "Run should return context.Canceled")
	})

	// TestRunNoGoroutineLeak: cancelling ctx cleanly stops Run and leaves no
	// goroutines behind, even after delivery goroutines were spawned.
	It("leaves no goroutines behind after ctx cancel", func() {
		src := newFakeSource()
		ctrl := gomock.NewController(GinkgoT())
		mock, rec := recordingNotifier(ctrl)
		clk := newFakeClock(baseTime)
		w := New(Config{CheckInterval: time.Hour}, src, mock, clk)

		src.set(followerView(7, "other-node"))
		base := runtime.NumGoroutine()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		patience.Await(GinkgoTB(), "the startup evaluation to read a view", schedulerBudget,
			src.views, func(views int) bool { return views >= 1 })

		seen := baseTime.Add(-time.Minute)
		// Two extra alive peers keep the cluster quorate (alive 5 of 8) despite
		// three dead peers, so the isolation guard lets all three episodes open.
		src.push(leaderView(7,
			peer("a", "10.0.0.1:1", warden.StatusDead, seen),
			peer("b", "10.0.0.2:2", warden.StatusDead, seen),
			peer("c", "10.0.0.3:3", warden.StatusDead, seen),
			peer("x", "10.0.0.8:8", warden.StatusAlive, baseTime),
			peer("y", "10.0.0.9:9", warden.StatusAlive, baseTime),
		))
		patience.Await(GinkgoTB(), "all three episodes to notify", schedulerBudget,
			rec.Sent, func(sent []warden.Incident) bool { return len(sent) == 3 })

		cancel()
		Expect(errors.Is(runReturns(done), context.Canceled)).To(BeTrue(), "Run should return context.Canceled")
		// Run joins every delivery goroutine before returning, so the count must
		// settle back to the baseline (allow scheduler slop).
		patience.Await(GinkgoTB(), "the goroutine count to return to its baseline", schedulerBudget,
			runtime.NumGoroutine, func(count int) bool { return count <= base+1 })
	})

	// TestIncidentsNeverHangs: Incidents() never hangs — before Run starts, while
	// it runs, and after it exits.
	It("serves Incidents() without hanging before, during, and after Run", func() {
		src := newFakeSource()
		ctrl := gomock.NewController(GinkgoT())
		mock, _ := recordingNotifier(ctrl)
		clk := newFakeClock(baseTime)
		w := New(Config{CheckInterval: time.Hour}, src, mock, clk)
		src.set(followerView(7, "other-node"))

		// Before Run starts: returns empty, no hang.
		got := w.Incidents()
		Expect(got).NotTo(BeNil(), "Incidents() returned nil before Run")
		Expect(got).To(HaveLen(0), "Incidents() before Run")

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		patience.Await(GinkgoTB(), "the startup evaluation to read a view", schedulerBudget,
			src.views, func(views int) bool { return views >= 1 })

		seen := baseTime.Add(-time.Minute)
		src.push(leaderView(7, peer("node-a", peerAddr, warden.StatusDead, seen)))
		// While running: eventually reflects the incident, never hangs.
		patience.Await(GinkgoTB(), "the incident to reach Incidents()", schedulerBudget,
			w.Incidents, func(incidents []warden.Incident) bool { return len(incidents) == 1 })

		cancel()
		Expect(errors.Is(runReturns(done), context.Canceled)).To(BeTrue(), "Run should return context.Canceled")

		// After exit: still returns the final snapshot, no hang.
		Expect(w.Incidents()).To(HaveLen(1), "Incidents() after exit")
	})
})
