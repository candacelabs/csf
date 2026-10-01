package fleet

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// feedPatience bounds a wait for one view. It is generous against a feed that
// does no work at all, because a timeout here should mean "nothing arrived",
// never "the machine was busy".
const feedPatience = 5 * time.Second

// runFeed starts a feed for the length of one specification and joins its
// goroutine on the way out, so a feed still running when the next specification
// starts is a failure here rather than a race report attributed to somebody else.
func runFeed(buffer int) (*Feed[int], context.Context) {
	GinkgoHelper()

	feed := NewFeed[int](buffer)
	ctx, cancel := context.WithCancel(context.Background())

	var crew Crew
	crew.Go(ctx, feed.Run)
	DeferCleanup(func() {
		cancel()
		done := make(chan struct{})
		go func() { crew.Wait(); close(done) }()
		Eventually(done, feedPatience).Should(BeClosed())
	})
	return feed, ctx
}

var _ = Describe("A feed", func() {
	It("hands the current view to a subscriber that arrives after it", func() {
		feed, ctx := runFeed(4)
		Expect(feed.Publish(ctx, 7)).To(BeTrue())

		// The publish above and the subscribe below are two goroutines racing
		// for the feed's select, so the assertion is that the view arrives
		// eventually rather than that it was already there: what is being
		// specified is that a late subscriber is never left with nothing.
		views, subscribeError := feed.Subscribe(ctx)
		Expect(subscribeError).ToNot(HaveOccurred())
		Eventually(views, feedPatience).Should(Receive(Equal(7)))
	})

	It("gives every subscriber the same view", func() {
		feed, ctx := runFeed(4)

		first, firstError := feed.Subscribe(ctx)
		Expect(firstError).ToNot(HaveOccurred())
		second, secondError := feed.Subscribe(ctx)
		Expect(secondError).ToNot(HaveOccurred())

		Expect(feed.Publish(ctx, 11)).To(BeTrue())
		Eventually(first, feedPatience).Should(Receive(Equal(11)))
		Eventually(second, feedPatience).Should(Receive(Equal(11)))
	})

	It("drops rather than blocking when a subscriber stops reading", func() {
		feed, ctx := runFeed(1)

		views, subscribeError := feed.Subscribe(ctx)
		Expect(subscribeError).ToNot(HaveOccurred())

		// Far more views than the subscriber's buffer holds, with nothing
		// reading. Every publish must still return: an engine that stalled
		// because a browser stopped reading would be a service hostage to its
		// own console.
		for view := range 32 {
			Expect(feed.Publish(ctx, view)).To(BeTrue(),
				"publish %d blocked on a subscriber that is not reading", view)
		}

		// And what the subscriber does hold is a view the engine really
		// minted, rather than a placeholder standing in for the ones it missed.
		var taken int
		Eventually(views, feedPatience).Should(Receive(&taken))
		Expect(taken).To(BeNumerically(">=", 0))
		Expect(taken).To(BeNumerically("<", 32))
	})

	It("closes a subscriber whose context ended, at the next view", func() {
		feed, ctx := runFeed(4)

		leaving, cancelLeaving := context.WithCancel(ctx)
		views, subscribeError := feed.Subscribe(leaving)
		Expect(subscribeError).ToNot(HaveOccurred())
		cancelLeaving()

		// Two views: the first is the one the feed notices the departure on,
		// and it may already have been queued to this subscriber before the
		// cancel landed. The channel closing is what is being specified.
		Expect(feed.Publish(ctx, 1)).To(BeTrue())
		Expect(feed.Publish(ctx, 2)).To(BeTrue())
		Eventually(func() bool {
			select {
			case _, open := <-views:
				return !open
			default:
				return false
			}
		}, feedPatience).Should(BeTrue())
	})

	It("closes every subscriber when the feed stops", func() {
		feed := NewFeed[int](4)
		ctx, cancel := context.WithCancel(context.Background())

		var crew Crew
		crew.Go(ctx, feed.Run)

		views, subscribeError := feed.Subscribe(ctx)
		Expect(subscribeError).ToNot(HaveOccurred())

		cancel()
		crew.Wait()
		Eventually(views, feedPatience).Should(BeClosed())
	})

	It("refuses to subscribe once the caller's context has ended", func() {
		feed := NewFeed[int](4)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		views, subscribeError := feed.Subscribe(ctx)
		Expect(subscribeError).To(MatchError(context.Canceled))
		Expect(views).To(BeNil())
	})
})

var _ = Describe("A start token", func() {
	It("is taken by exactly one caller", func() {
		once := NewOnce()
		Expect(once.Take()).To(BeTrue())
		Expect(once.Take()).To(BeFalse())
		Expect(once.Take()).To(BeFalse())
	})
})

var _ = Describe("A per-goroutine random stream", func() {
	It("repeats for one seed and index, and differs across indices", func() {
		first := Jitter(20260902, 0)
		again := Jitter(20260902, 0)
		other := Jitter(20260902, 1)

		firstDraws := []int64{first.Int64N(1_000_000), first.Int64N(1_000_000)}
		againDraws := []int64{again.Int64N(1_000_000), again.Int64N(1_000_000)}
		otherDraws := []int64{other.Int64N(1_000_000), other.Int64N(1_000_000)}

		Expect(againDraws).To(Equal(firstDraws),
			"a fixed seed and index is what makes an engine's delays reproducible")
		Expect(otherDraws).ToNot(Equal(firstDraws),
			"equal streams per goroutine are the livelock this exists to break")
	})
})
