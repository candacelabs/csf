// Package patience is the one way a test in this repository waits for
// something to become true.
//
// It exists because of a measurement taken on 2026-09-02. Four named await
// helpers — awaitRaft, awaitReload, waitUntil, awaitOutput — plus five more
// inside a single end-to-end suite were each a private re-typing of the same
// loop, and a dozen further waits were spelled as a sleep inside a for. In a
// tree where 89 test files already had a polling library on the import path.
// The primitive was in the dependency tree the whole time; what was missing
// was a typed shell over it, so every author wrote their own.
//
// # The typed shell
//
// [Await] takes the poll and the predicate as typed function values and hands
// back the value that satisfied the predicate. That is the whole difference
// from calling the polling library directly, and it is the shape CS-7 asks
// for: the reflection-driven, any-typed engine underneath is real, is correct,
// and stays — but it is erased exactly once, inside this package, instead of
// at every call site in the repository.
//
// It buys a better failure as well as a better signature, which is the part
// that shows up on a Tuesday:
//
//	Eventually(func() bool { return len(sent) == 1 }).Should(BeTrue())
//
// fails with "Expected <bool>: false to be true", which says nothing a reader
// can act on. Polling the value and judging it with a predicate fails with the
// slice that was actually there.
//
// # Budgets are named, and generous
//
// A [Budget] is a wall clock, and the cost of the two mistakes is not
// symmetric: a budget that is too large makes a failing test slow, while a
// budget that is too small makes a correct test red on a loaded machine. State
// it generously and give it a name in the suite that uses it. The warden CLI
// contract suite spent a month flaking because "5s" sat inline in one helper
// where nobody argued with it.
//
// # What is not an await
//
// A sleep that generates load, paces a sender, throttles a link, or defines an
// observation window is the subject of its test rather than a wait for one,
// and nothing here replaces it. The chaos suite's slow-client throttle is the
// clearest case: the sleep is the slow client. Converting one of those to a
// poll deletes the experiment.
//
// [Consistently] is the negative-space twin, for the assertion that something
// does *not* happen — no violation is raised, no goroutine appears, the queue
// never grows. A bare sleep followed by one read is that assertion sampled
// once, at the least informative moment.
package patience

import (
	"time"

	// gomega is imported qualified on purpose, and it is the CS-11 exemption:
	// that rule dot-imports gomega in *test* files so specs read Expect/Eventually
	// unqualified, but this is production code and gomega is its engine, not its
	// assertion vocabulary. Dot-importing an assertion library into a non-test
	// package pollutes that package's namespace with matchers, and this package's
	// whole job is to be the one typed shell that names Await/Consistently itself.
	"github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
)

// DefaultInterval is how often an await looks when its budget does not say.
//
// It is deliberately short relative to any honest budget. Polling frequency
// costs a test a few function calls; the budget is the number that decides
// whether a loaded machine fails a correct test, and the two are not the same
// dial even though both are durations.
const DefaultInterval = 20 * time.Millisecond

// IReporter is the part of a test's own handle that an await needs: a helper
// marker, so a failure is reported at the call site rather than inside this
// package, and one fatal failure.
//
// It is declared here rather than the signatures taking [testing.TB], for two
// reasons. The narrow one is that testing.TB carries an unexported method and
// cannot be implemented outside the standard library, so this package's own
// specifications could not drive the failing path at all. The wider one is
// that these two methods are exactly what the polling engine underneath
// requires, so this is the contract rather than a subset of somebody else's.
//
// *testing.T, *testing.B, and Ginkgo's GinkgoTB() all satisfy it.
type IReporter interface {
	Helper()
	Fatalf(format string, arguments ...any)
}

// Budget is how long an await is willing to wait and how often it looks.
//
// Declare one as a named variable beside the specifications that use it —
// readyBudget, convergeBudget — rather than writing the durations inline. A
// budget with a name is a claim somebody can argue with; a duration buried in
// an argument list is a number nobody revisits until it flakes.
type Budget struct {
	// Within is the whole wall clock the await may spend. It must be
	// positive; an await with no budget is not an await.
	Within time.Duration

	// Interval is how often poll is called. Zero means [DefaultInterval].
	Interval time.Duration
}

// interval is Interval with the zero value resolved.
func (budget Budget) interval() time.Duration {
	if budget.Interval <= 0 {
		return DefaultInterval
	}
	return budget.Interval
}

// usable reports whether the budget can be waited on, failing the test when it
// cannot rather than handing a zero timeout to the engine, which would spend
// the library's default and report a duration nobody wrote.
func (budget Budget) usable(reporter IReporter, what string) bool {
	reporter.Helper()
	if budget.Within > 0 {
		return true
	}
	reporter.Fatalf(
		"patience: waiting for %s was given a budget of %s. A budget is a wall "+
			"clock and must be positive; name one in the suite rather than "+
			"leaving it zero.",
		what, budget.Within)
	return false
}

// asyncFor is one of the two polling shapes the engine offers, as a function
// value rather than a boolean parameter on a shared body. There are exactly
// two and there will not be a third, but a flag named `holds bool` at a call
// site is unreadable in a way `eventually` and `consistently` are not.
type asyncFor[Value any] func(
	assertions *gomega.WithT,
	poll func() Value,
) gomegatypes.AsyncAssertion

func eventually[Value any](
	assertions *gomega.WithT,
	poll func() Value,
) gomegatypes.AsyncAssertion {
	return assertions.Eventually(poll)
}

func consistently[Value any](
	assertions *gomega.WithT,
	poll func() Value,
) gomegatypes.AsyncAssertion {
	return assertions.Consistently(poll)
}

// tracked wraps poll so the last value it produced outlives the assertion.
//
// The engine hands its actual to the matcher and keeps it; this is what lets
// the typed value come back out to the caller, which is the entire reason this
// package is a shell rather than a re-export.
func tracked[Value any](poll func() Value, last *Value) func() Value {
	return func() Value {
		*last = poll()
		return *last
	}
}

// settle is the one polling loop this package owns. Both entry points are it
// with a different shape and a different sentence for the failure.
func settle[Value any](
	reporter IReporter,
	what string,
	budget Budget,
	poll func() Value,
	match func(value Value) bool,
	shape asyncFor[Value],
	failure string,
) Value {
	reporter.Helper()
	var last Value
	if !budget.usable(reporter, what) {
		return last
	}
	shape(gomega.NewWithT(reporter), tracked(poll, &last)).
		WithTimeout(budget.Within).
		WithPolling(budget.interval()).
		Should(gomega.Satisfy(match), failure, what, budget.Within, budget.interval())
	return last
}

const awaitFailure = "patience: %s did not happen within %s, polled every %s. " +
	"The value below is the last one poll returned, so a value that looks right " +
	"means the predicate is narrower than the state the system reaches."

const consistentlyFailure = "patience: %s stopped holding inside %s, polled every %s. " +
	"The value below is the one that broke it."

// Await polls until match accepts a value, and returns that value.
//
// what names the subject in the failure message and is required: the
// alternative sentence is "the predicate never matched", which tells a reader
// nothing they can act on. Write it as the thing being waited for — "the
// leader to report an elected term", "the reload to replace the document".
//
// The returned value is the one match accepted, so a caller asserts against
// what it waited for rather than polling a second time and racing itself.
func Await[Value any](
	reporter IReporter,
	what string,
	budget Budget,
	poll func() Value,
	match func(value Value) bool,
) Value {
	reporter.Helper()
	return settle(reporter, what, budget, poll, match, eventually[Value], awaitFailure)
}

// Consistently polls for the whole budget and fails the first time match
// rejects a value, returning the last value polled.
//
// This is the assertion for an absence — no violation was raised, no goroutine
// appeared, the queue never grew — and it is what a bare sleep followed by one
// read was reaching for. The sleep samples that claim once, at the least
// informative moment; this samples it throughout and names the value that
// broke it.
func Consistently[Value any](
	reporter IReporter,
	what string,
	budget Budget,
	poll func() Value,
	match func(value Value) bool,
) Value {
	reporter.Helper()
	return settle(reporter, what, budget, poll, match, consistently[Value], consistentlyFailure)
}
