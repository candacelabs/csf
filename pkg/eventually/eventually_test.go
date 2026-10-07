package eventually_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/eventually"
)

// A budget small enough that the failing specifications below cost
// milliseconds, and large enough that the passing ones are not racing the
// scheduler. Every other suite in this repository should be doing the
// opposite — see the package comment — but these specifications are about the
// primitive rather than about a system, and the thing being waited on is a
// counter in the same goroutine.
var (
	quick   = eventually.Budget{Within: 500 * time.Millisecond, Interval: time.Millisecond}
	brief   = eventually.Budget{Within: 30 * time.Millisecond, Interval: time.Millisecond}
	unspent = eventually.Budget{}
)

var _ = Describe("Await", func() {
	It("returns the value the predicate accepted, not a re-read of it", func() {
		reporter := &recordingReporter{}
		polls := 0

		accepted := eventually.Await(reporter, "the counter to pass two", quick,
			func() int {
				polls++
				return polls
			},
			func(value int) bool { return value > 2 })

		Expect(accepted).To(Equal(3))
		Expect(reporter.failures).To(BeEmpty())
	})

	It("polls until the predicate accepts rather than judging one reading", func() {
		reporter := &recordingReporter{}
		polls := 0

		eventually.Await(reporter, "the third reading", quick,
			func() int {
				polls++
				return polls
			},
			func(value int) bool { return value == 3 })

		Expect(polls).To(Equal(3))
	})

	It("marks itself a helper so the failure is reported at the call site", func() {
		reporter := &recordingReporter{}

		eventually.Await(reporter, "anything at all", quick,
			func() bool { return true },
			func(value bool) bool { return value })

		Expect(reporter.helped).To(BeNumerically(">", 0))
	})

	// The payoff of the typed shell, and the reason this package is not a
	// one-line re-export: a bool-returning poll fails with "false is not
	// true", while a typed poll fails with the value that was actually there.
	It("fails naming the subject, the budget, and the last value it saw", func() {
		reporter := &recordingReporter{}

		eventually.Await(reporter, "the roster to reach three", brief,
			func() []string { return []string{"node-a", "node-b"} },
			func(roster []string) bool { return len(roster) == 3 })

		Expect(reporter.failures).To(HaveLen(1))
		Expect(reporter.failed()).To(ContainSubstring("the roster to reach three"))
		Expect(reporter.failed()).To(ContainSubstring("30ms"))
		Expect(reporter.failed()).To(ContainSubstring("node-b"),
			"the last value polled is the whole point of returning it typed")
	})

	It("returns the last value it polled even when nothing matched", func() {
		reporter := &recordingReporter{}

		last := eventually.Await(reporter, "an impossible reading", brief,
			func() int { return 7 },
			func(value int) bool { return value == 8 })

		Expect(last).To(Equal(7))
	})

	// A zero budget reaches the engine as "use your own default", which spends
	// a duration nobody wrote and then reports it. Refusing is the only answer
	// that keeps the failure message true.
	It("refuses a budget with no wall clock, without polling anything", func() {
		reporter := &recordingReporter{}
		polls := 0

		eventually.Await(reporter, "something with no budget", unspent,
			func() int {
				polls++
				return polls
			},
			func(value int) bool { return true })

		Expect(polls).To(BeZero())
		Expect(reporter.failed()).To(ContainSubstring("must be positive"))
	})
})

var _ = Describe("Consistently", func() {
	It("returns the last value when the predicate held for the whole budget", func() {
		reporter := &recordingReporter{}
		polls := 0

		held := eventually.Consistently(reporter, "the violation log to stay empty", brief,
			func() []string {
				polls++
				return nil
			},
			func(violations []string) bool { return len(violations) == 0 })

		Expect(held).To(BeEmpty())
		Expect(reporter.failures).To(BeEmpty())
		Expect(polls).To(BeNumerically(">", 1),
			"one reading is a sleep with extra steps; the point is sampling throughout")
	})

	It("fails at the first value that breaks it, naming that value", func() {
		reporter := &recordingReporter{}
		polls := 0

		eventually.Consistently(reporter, "the queue to stay empty", quick,
			func() []string {
				polls++
				if polls > 3 {
					return []string{"overflow"}
				}
				return nil
			},
			func(queue []string) bool { return len(queue) == 0 })

		Expect(reporter.failures).To(HaveLen(1))
		Expect(reporter.failed()).To(ContainSubstring("the queue to stay empty"))
		Expect(reporter.failed()).To(ContainSubstring("overflow"))
	})

	It("refuses a budget with no wall clock", func() {
		reporter := &recordingReporter{}

		eventually.Consistently(reporter, "an absence with no budget", unspent,
			func() int { return 0 },
			func(value int) bool { return true })

		Expect(reporter.failed()).To(ContainSubstring("must be positive"))
	})
})

var _ = Describe("Budget", func() {
	It("polls at DefaultInterval when the budget does not say", func() {
		Expect(eventually.BudgetInterval(eventually.Budget{Within: time.Second})).
			To(Equal(eventually.DefaultInterval))
		Expect(eventually.BudgetInterval(eventually.Budget{Within: time.Second, Interval: -1})).
			To(Equal(eventually.DefaultInterval))
	})

	It("polls at the interval the budget does state", func() {
		Expect(eventually.BudgetInterval(eventually.Budget{Within: time.Second, Interval: time.Minute})).
			To(Equal(time.Minute))
	})
})

var _ = Describe("Wait", func() {
	It("returns the value the predicate accepted and no error", func() {
		polls := 0
		accepted, err := eventually.Wait("the counter to pass two", quick,
			func() int {
				polls++
				return polls
			},
			func(value int) bool { return value > 2 })
		Expect(err).NotTo(HaveOccurred())
		Expect(accepted).To(Equal(3))
	})

	It("returns ErrNotMet naming the last value when the budget runs out, and keeps the caller running", func() {
		_, err := eventually.Wait("a value that never comes", brief,
			func() string { return "still-starting" },
			func(value string) bool { return value == "ready" })
		Expect(err).To(MatchError(eventually.ErrNotMet))
		Expect(err.Error()).To(ContainSubstring("still-starting"))
		Expect(err.Error()).To(ContainSubstring("a value that never comes"))
	})
})
