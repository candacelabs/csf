package eventually_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/eventually"
)

// steppingClock is a controllable clock: every wait is delivered at once and
// moves the clock by exactly the duration asked, so a spec reads the elapsed
// time the wait computed without any time passing.
type steppingClock struct {
	now   time.Time
	waits []time.Duration
}

func (clock *steppingClock) Now() time.Time { return clock.now }

func (clock *steppingClock) After(d time.Duration) <-chan time.Time {
	clock.waits = append(clock.waits, d)
	clock.now = clock.now.Add(d)
	delivered := make(chan time.Time, 1)
	delivered <- clock.now
	return delivered
}

var untilBudget = eventually.Budget{Within: 10 * time.Second, Interval: 3 * time.Second}

var _ = Describe("Until", func() {
	var clock *steppingClock

	BeforeEach(func() {
		clock = &steppingClock{now: time.Date(2026, 10, 5, 2, 10, 0, 0, time.UTC)}
	})

	counter := func() func(ctx context.Context) int {
		polls := 0
		return func(_ context.Context) int {
			polls++
			return polls
		}
	}

	It("returns the accepted value with the clock's elapsed time once the condition is met", func() {
		outcome, err := eventually.Until(context.Background(), clock, untilBudget, counter(),
			func(value int) bool { return value == 3 }, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(eventually.Outcome[int]{Met: true, Elapsed: 6 * time.Second, Polls: 3, Last: 3}))
	})

	It("stops at the deadline with the last value seen, the final wait cut to the deadline", func() {
		outcome, err := eventually.Until(context.Background(), clock, untilBudget, counter(),
			func(_ int) bool { return false }, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(eventually.Outcome[int]{Elapsed: 10 * time.Second, Polls: 5, Last: 5}))
		Expect(clock.waits).To(Equal([]time.Duration{3 * time.Second, 3 * time.Second, 3 * time.Second, time.Second}))
	})

	It("ends early when the value can no longer be accepted", func() {
		outcome, err := eventually.Until(context.Background(), clock, untilBudget, counter(),
			func(_ int) bool { return false }, func(value int) bool { return value == 2 })

		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(eventually.Outcome[int]{Final: true, Elapsed: 3 * time.Second, Polls: 2, Last: 2}))
	})

	It("polls every DefaultInterval when the budget names none", func() {
		_, err := eventually.Until(context.Background(), clock, eventually.Budget{Within: time.Second}, counter(),
			func(value int) bool { return value == 2 }, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(clock.waits).To(Equal([]time.Duration{eventually.DefaultInterval}))
	})

	It("refuses a budget that is not positive without polling", func() {
		polled := false
		_, err := eventually.Until(context.Background(), clock, eventually.Budget{}, func(_ context.Context) bool {
			polled = true
			return true
		}, func(value bool) bool { return value }, nil)

		Expect(err).To(MatchError(eventually.ErrNoBudget))
		Expect(polled).To(BeFalse())
	})

	It("returns the context's cause when it ends first, with the outcome so far", func() {
		cause := errors.New("operator interrupted")
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)

		outcome, err := eventually.Until(ctx, blockedClock{}, untilBudget, counter(), func(_ int) bool { return false }, nil)

		Expect(err).To(MatchError(cause))
		Expect(outcome.Polls).To(Equal(1))
	})
})

// blockedClock never delivers a wait, so only the context can end one.
type blockedClock struct{}

func (blockedClock) Now() time.Time                         { return time.Time{} }
func (blockedClock) After(_ time.Duration) <-chan time.Time { return nil }
