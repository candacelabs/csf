package livetest_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

func TestLivetest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Livetest Suite")
}

type counter struct {
	N     int
	Label string
}

// recorder stands in for testing.TB so a spec can assert that the helper fails
// rather than having the helper fail the spec.
type recorder struct {
	testing.TB
	failed  bool
	message string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatal(args ...any) {
	r.failed = true
	r.message = fmt.Sprint(args...)
	panic(sentinel)
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
	panic(sentinel)
}

const sentinel = "livetest stopped the test"

// run calls fn and reports what the helper did, converting the helper's own
// stop-the-test panic back into a value.
func run(fn func(t testing.TB)) *recorder {
	r := &recorder{}
	func() {
		defer func() {
			if v := recover(); v != nil && v != sentinel {
				panic(v)
			}
		}()
		fn(r)
	}()
	return r
}

func text(format string, args ...any) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	})
}

var log = []live.Event{
	{Name: "counter.increment", ID: 1},
	{Name: "counter.increment", ID: 2},
	{Name: "counter.noop", ID: 3},
}

var _ = Describe("ReplayN", func() {
	It("passes a reducer that is a pure function of its inputs", func() {
		pure := func(state counter, ev live.Event) (counter, []live.Effect[live.AnonymousIdentity]) {
			if ev.Name == "counter.increment" {
				state.N++
			}
			return state, nil
		}

		r := run(func(tb testing.TB) { livetest.ReplayN(tb, pure, counter{}, log, 8) })

		Expect(r.failed).To(BeFalse(), r.message)
	})

	It("catches a reducer that reads a clock", func() {
		impure := func(state counter, ev live.Event) (counter, []live.Effect[live.AnonymousIdentity]) {
			state.Label = time.Now().Format(time.RFC3339Nano)
			return state, nil
		}

		r := run(func(tb testing.TB) { livetest.ReplayN(tb, impure, counter{}, log, 4) })

		Expect(r.failed).To(BeTrue())
		Expect(r.message).To(ContainSubstring("different state"))
		Expect(r.message).To(ContainSubstring("clock"))
	})

	It("catches a reducer whose effects differ between runs", func() {
		var runs int
		impure := func(state counter, ev live.Event) (counter, []live.Effect[live.AnonymousIdentity]) {
			runs++
			if runs > len(log) {
				return state, []live.Effect[live.AnonymousIdentity]{drift()}
			}
			return state, nil
		}

		r := run(func(tb testing.TB) { livetest.ReplayN(tb, impure, counter{}, log, 3) })

		Expect(r.failed).To(BeTrue())
		Expect(r.message).To(ContainSubstring("scheduled different effects"))
	})

	It("refuses to prove anything from an empty log or a single replay", func() {
		pure := func(state counter, _ live.Event) (counter, []live.Effect[live.AnonymousIdentity]) { return state, nil }

		Expect(run(func(tb testing.TB) { livetest.ReplayN(tb, pure, counter{}, nil, 4) }).failed).To(BeTrue())
		Expect(run(func(tb testing.TB) { livetest.ReplayN(tb, pure, counter{}, log, 1) }).failed).To(BeTrue())
	})
})

// drift is the effect the impure reducer above schedules on one run and not on
// another. Only its source matters: ReplayN compares the sequence of sources
// two runs produced, because Effect.Run is a function value and Go cannot
// compare two of those.
func drift() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: "test.drift",
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return nil
		},
	}
}

var _ = Describe("AssertDirtyComplete", func() {
	config := func(dirty func(prev, next counter) bool) live.Config[counter, live.AnonymousIdentity] {
		return live.Config[counter, live.AnonymousIdentity]{
			Reduce: func(state counter, ev live.Event) (counter, []live.Effect[live.AnonymousIdentity]) {
				switch ev.Name {
				case "counter.increment":
					state.N++
				case "counter.relabel":
					state.Label = "changed"
				}
				return state, nil
			},
			Fragments: []live.Fragment[counter]{{
				ID:     "counter",
				Render: func(s counter) templ.Component { return text("<b>%s %d</b>", s.Label, s.N) },
				Dirty:  dirty,
			}},
		}
	}

	It("passes a change declaration that covers everything the render reads", func() {
		cfg := config(func(prev, next counter) bool { return prev != next })

		r := run(func(tb testing.TB) { livetest.AssertDirtyComplete(tb, cfg, counter{}, log) })

		Expect(r.failed).To(BeFalse(), r.message)
	})

	It("catches a fragment that declared itself unchanged while its markup moved", func() {
		// The declaration watches the counter and the render also prints the
		// label: the classic under-declaration, invisible in development
		// because some other transition usually re-renders the region.
		cfg := config(func(prev, next counter) bool { return prev.N != next.N })

		r := run(func(tb testing.TB) {
			livetest.AssertDirtyComplete(tb, cfg, counter{}, []live.Event{{Name: "counter.relabel", ID: 1}})
		})

		Expect(r.failed).To(BeTrue())
		Expect(r.message).To(ContainSubstring(`fragment "counter" declared itself unchanged`))
		Expect(r.message).To(ContainSubstring("Widen the fragment's Dirty function"))
	})

	It("does not object to over-declaring, which costs a suppressed render and nothing else", func() {
		cfg := config(func(prev, next counter) bool { return true })

		r := run(func(tb testing.TB) { livetest.AssertDirtyComplete(tb, cfg, counter{}, log) })

		Expect(r.failed).To(BeFalse(), r.message)
	})

	It("treats a nil declaration as always dirty rather than as a failure", func() {
		cfg := config(nil)

		r := run(func(tb testing.TB) { livetest.AssertDirtyComplete(tb, cfg, counter{}, log) })

		Expect(r.failed).To(BeFalse(), r.message)
	})

	It("refuses a configuration it cannot replay", func() {
		Expect(run(func(tb testing.TB) {
			livetest.AssertDirtyComplete(tb, live.Config[counter, live.AnonymousIdentity]{}, counter{}, log)
		}).failed).To(BeTrue())

		cfg := config(nil)
		cfg.Reduce = nil
		Expect(run(func(tb testing.TB) {
			livetest.AssertDirtyComplete(tb, cfg, counter{}, log)
		}).failed).To(BeTrue())
	})

	It("reports a fragment that renders nothing at all", func() {
		cfg := config(nil)
		cfg.Fragments[0].Render = func(state counter) templ.Component { return nil }

		r := run(func(tb testing.TB) { livetest.AssertDirtyComplete(tb, cfg, counter{}, log) })

		Expect(r.failed).To(BeTrue())
		Expect(strings.ToLower(r.message)).To(ContainSubstring("rendered no component"))
	})
})

// This package exported a testing.TB adapter until L9-1's review-wave ruling 1
// withdrew it: Ginkgo ships GinkgoTB() for the same purpose and this module
// already required the version that has it. These specs are what goes red if
// that stops being true — they are the reason there is no adapter here, stated
// as an executable claim rather than as a sentence in a doc comment.
var _ = Describe("the testing.TB a Ginkgo suite passes", func() {
	It("drives every helper here with no adapter in between", func() {
		tb := GinkgoTB()
		var _ testing.TB = tb // compile-time: no adaptation is required.

		pure := func(state counter, ev live.Event) (counter, []live.Effect[live.AnonymousIdentity]) {
			if ev.Name == "counter.increment" {
				state.N++
			}
			return state, nil
		}
		livetest.ReplayN(tb, pure, counter{}, log, 8)

		livetest.AssertDirtyComplete(tb, live.Config[counter, live.AnonymousIdentity]{
			Reduce: pure,
			Fragments: []live.Fragment[counter]{{
				ID:     "counter",
				Render: func(s counter) templ.Component { return text("<b>%d</b>", s.N) },
				Dirty:  func(prev, next counter) bool { return prev != next },
			}},
		}, counter{}, log)
	})

	It("implements the methods the withdrawn adapter left to panic", func() {
		tb := GinkgoTB()

		// The adapter embedded a nil testing.TB, so every method it did not
		// override panicked, and its doc comment argued that stopping loudly
		// was the failure mode to want. GinkgoTB implements them instead,
		// which is why that property is obsoleted rather than lost: a helper
		// here growing a tb.Cleanup call now works rather than stopping.
		Expect(func() { tb.Cleanup(func() {}) }).NotTo(Panic())
		Expect(func() { tb.Helper() }).NotTo(Panic())
		Expect(tb.Name()).NotTo(BeEmpty())
	})
})
