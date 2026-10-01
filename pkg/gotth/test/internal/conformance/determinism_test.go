package conformance_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// ---------------------------------------------------------------------------
// FR-19 — deterministic render output
//
// "The same state MUST render byte-identical HTML across runs and across
// processes." The nearest thing a single test binary has to a second process
// is a second session with its own actor, its own renderer and its own
// connection, so that is what these specs compare against.
// ---------------------------------------------------------------------------

var _ = Describe("Deterministic render output (FR-19)", func() {
	It("renders byte-identical markup for two sessions driven the same way", func() {
		log := []string{"qa.increment", "qa.increment", "qa.relabel", "qa.increment"}

		first := drive(log)
		second := drive(log)

		Expect(second).To(Equal(first),
			"two sessions given the same events produced different markup:\n  A: %v\n  B: %v",
			first, second)
	})

	// Order-independence is not claimed by FR-19 and is not asserted here. What
	// is asserted is the weaker, true thing: two interleavings that arrive at
	// the same state render the same bytes, so the markup is a function of
	// state and not of the path taken to it.
	It("renders byte-identical markup for two interleavings reaching one state", func() {
		// The comparison is over a full snapshot rather than the last patch,
		// because a patch carries only the fragments that moved and the two
		// orders move them in a different sequence. What FR-19 claims is about
		// the state, so the whole rendered state is what is compared.
		forward := finalMarkup([]string{"qa.increment", "qa.relabel", "qa.increment"})
		reverse := finalMarkup([]string{"qa.increment", "qa.increment", "qa.relabel"})

		Expect(reverse).To(Equal(forward),
			"the same state rendered differently depending on how it was reached:\n  A: %v\n  B: %v",
			forward, reverse)
	})

	// The suppression path is the library's own byte-equality check, and it is
	// observable: a transition whose render did not move emits no patch. That
	// makes "the same state renders the same bytes" checkable over the wire
	// rather than only by comparing strings in a test.
	It("emits nothing when a transition re-renders identical bytes", func() {
		d := dial(nil)

		d.event("qa.relabel", d.highestSeq(), [2]string{"label", "same"})
		d.nextPatch()
		before := d.highestSeq()

		// The same value again: state changes to an equal value, so the render
		// is byte-identical and the patch must be suppressed.
		d.event("qa.relabel", d.highestSeq(), [2]string{"label", "same"})
		d.drainUntilQuiet(500 * time.Millisecond)

		Expect(d.highestSeq()).To(Equal(before),
			"an identical re-render emitted a patch: the byte-equality suppression did not engage")
	})

	// A render that is a pure function of state cannot be sensitive to how many
	// times it has been called. Fifty renders of one state must be one string.
	It("renders one state the same way however many times it is asked", func() {
		cfg := qaConfig()
		state := tally{N: 42, Label: "steady"}

		var seen string
		for i := 0; i < 50; i++ {
			for _, f := range cfg.Fragments {
				html := renderOnce(f, state)
				if i == 0 && f.ID == "count" {
					seen = html
				}
				if f.ID == "count" {
					Expect(html).To(Equal(seen),
						"render %d of the same state produced different bytes", i)
				}
			}
		}
	})
})

// ---------------------------------------------------------------------------
// FR-15 — the determinism helper is tested, not assumed
//
// The requirement is that the library ships a helper which catches a
// nondeterministic reducer. A helper that passes everything satisfies the
// letter of that and none of its purpose, so the helper itself is put under
// test here: it must fail a reducer that reads a clock, and pass one that does
// not.
// ---------------------------------------------------------------------------

// probeTB is a testing.TB that records a failure instead of ending the test.
//
// testing.TB cannot be implemented outside the testing package, so the real
// interface is embedded — as a nil value — and only the three methods ReplayN
// actually calls are overridden. Any other method would panic on the nil
// embed, which is the honest shape for a probe: it works for exactly the
// helper it was written to observe, and fails loudly if that changes.
type probeTB struct {
	testing.TB
	failed bool
	msg    string
}

type probeAborted struct{}

func (p *probeTB) Helper() {}

func (p *probeTB) Fatalf(format string, args ...any) {
	p.failed = true
	p.msg = fmt.Sprintf(format, args...)
	panic(probeAborted{})
}

func (p *probeTB) Fatal(args ...any) {
	p.failed = true
	p.msg = fmt.Sprint(args...)
	panic(probeAborted{})
}

// runProbe calls fn with a recording TB and reports whether it failed.
//
// The recovery has to publish the result itself. A helper that fails calls
// Fatalf, which panics past the ordinary return, so reading p only on the
// happy path would report every failure as a pass — which is exactly the
// direction a self-test must not be wrong in.
func runProbe(fn func(tb testing.TB)) (failed bool, msg string) {
	p := &probeTB{}
	defer func() {
		r := recover()
		failed, msg = p.failed, p.msg
		if r == nil {
			return
		}
		if _, ok := r.(probeAborted); !ok {
			panic(r)
		}
	}()
	fn(p)
	return p.failed, p.msg
}

var _ = Describe("The reducer determinism helper (FR-15)", func() {
	log := []live.Event{
		{Name: "qa.increment"},
		{Name: "qa.increment"},
		{Name: "qa.noop"},
	}

	It("passes a reducer that is a pure function of its inputs", func() {
		pure := func(s tally, ev live.Event) (tally, []live.Effect[qaUser]) {
			if ev.Name == "qa.increment" {
				s.N++
			}
			return s, nil
		}

		failed, msg := runProbe(func(tb testing.TB) {
			livetest.ReplayN(tb, pure, tally{}, log, 5)
		})

		Expect(failed).To(BeFalse(), "a pure reducer was reported nondeterministic: %s", msg)
	})

	// The mutation. If this passes, the helper is decoration.
	It("fails a reducer that reads a clock", func() {
		impure := func(s tally, ev live.Event) (tally, []live.Effect[qaUser]) {
			s.N++
			s.Label = fmt.Sprint(time.Now().UnixNano())
			return s, nil
		}

		failed, msg := runProbe(func(tb testing.TB) {
			livetest.ReplayN(tb, impure, tally{}, log, 5)
		})

		Expect(failed).To(BeTrue(), "the helper accepted a reducer that reads a clock")
		Expect(msg).To(ContainSubstring("different state"))
	})

	// The mutation that varies effects while leaving state alone, so the
	// helper has to be comparing effects and cannot pass on the state check.
	//
	// The impurity is a call counter rather than a clock, and that is a
	// correction rather than a preference. QA-1 defect D-13: the first version
	// of this spec branched on `time.Now().UnixNano()%2`, which failed the
	// whole gate once in roughly twelve runs — not because the helper missed
	// anything, but because a tight replay loop advances the clock by a nearly
	// constant stride, so every replay could sample the same parity and the
	// "impure" reducer was accidentally deterministic. A self-test whose
	// mutation is only probably a mutation reports the helper broken at random
	// and cannot report it working. A counter differs between replay 1 and
	// replay 2 by construction, on every host and every schedule.
	It("fails a reducer whose effects differ between replays", func() {
		calls := 0
		impure := func(s tally, ev live.Event) (tally, []live.Effect[qaUser]) {
			// State advances identically on every replay, so the effects
			// comparison is the only thing that can catch this.
			s.N++
			calls++
			if calls%2 == 1 {
				return s, []live.Effect[qaUser]{noisyEffect()}
			}
			return s, nil
		}

		failed, msg := runProbe(func(tb testing.TB) {
			livetest.ReplayN(tb, impure, tally{}, log, 8)
		})

		Expect(failed).To(BeTrue(),
			"the helper never noticed a reducer emitting different effects across replays")
		Expect(msg).To(ContainSubstring("different effects"),
			"the helper failed, but on the state comparison rather than the effects one, "+
				"so this spec would pass with the effects check deleted: %s", msg)
	})

	It("refuses to certify anything from a single replay or an empty log", func() {
		pure := func(s tally, _ live.Event) (tally, []live.Effect[qaUser]) { return s, nil }

		tooFew, msgFew := runProbe(func(tb testing.TB) {
			livetest.ReplayN(tb, pure, tally{}, log, 1)
		})
		Expect(tooFew).To(BeTrue(), "one replay compares nothing and must not pass")
		Expect(msgFew).To(ContainSubstring("at least 2"))

		empty, msgEmpty := runProbe(func(tb testing.TB) {
			livetest.ReplayN(tb, pure, tally{}, nil, 5)
		})
		Expect(empty).To(BeTrue(), "replaying an empty log proves nothing and must not pass")
		Expect(msgEmpty).To(ContainSubstring("empty event log"))
	})
})

var _ = Describe("The dirty-declaration helper", func() {
	// Under-declaring is the rendering mistake that produces a stale region in
	// production and nothing in development. The helper exists to catch it, so
	// the helper is checked against a fragment that under-declares.
	It("catches a fragment that declares itself unchanged while its markup moves", func() {
		cfg := qaConfig()
		// A Dirty that always says "nothing changed" while the render tracks N.
		cfg.Fragments[0].Dirty = func(prev, next tally) bool { return false }

		failed, msg := runProbe(func(tb testing.TB) {
			livetest.AssertDirtyComplete(tb, cfg, tally{}, []live.Event{{Name: "qa.increment"}})
		})

		Expect(failed).To(BeTrue(), "an under-declaring fragment was accepted")
		Expect(msg).To(ContainSubstring("declared itself unchanged"))
	})

	It("accepts an over-declaring fragment, which costs a comparison and nothing else", func() {
		cfg := qaConfig()
		cfg.Fragments[0].Dirty = func(prev, next tally) bool { return true }
		cfg.Fragments[1].Dirty = func(prev, next tally) bool { return true }

		failed, msg := runProbe(func(tb testing.TB) {
			livetest.AssertDirtyComplete(tb, cfg, tally{}, []live.Event{{Name: "qa.increment"}})
		})

		Expect(failed).To(BeFalse(), "over-declaring was reported as a failure: %s", msg)
	})
})

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// noisyEffect is the effect the impure reducer above schedules on one replay
// and not on the next. Only its source matters: ReplayN compares the sequence
// of sources two replays produced.
func noisyEffect() live.Effect[qaUser] {
	return live.Effect[qaUser]{
		Source: "qa.noisy",
		Run:    func(ctx context.Context, session live.Session[qaUser], emit live.Emitter) error { return nil },
	}
}

// drive runs one session through a log of event names and returns the markup
// of every fragment update it produced, in order.
func drive(names []string) []string {
	GinkgoHelper()
	d := dial(nil)

	var out []string
	for i, name := range names {
		seq := d.highestSeq()
		if name == "qa.relabel" {
			d.event(name, seq, [2]string{"label", fmt.Sprintf("v%d", i)})
		} else {
			d.event(name, seq)
		}
		patch := d.nextPatch()
		for _, u := range patch.GetUpdates() {
			out = append(out, u.GetFragmentId()+"="+u.GetHtml())
		}
		d.ack(d.highestSeq())
	}
	return out
}

// finalMarkup drives a log and then forces a full re-render, returning every
// fragment's markup. The resync is how a full render is obtained over the
// protocol: a snapshot renders everything, where a patch renders only what
// moved.
func finalMarkup(names []string) map[string]string {
	GinkgoHelper()
	d := dial(nil)

	for _, name := range names {
		seq := d.highestSeq()
		if name == "qa.relabel" {
			// A fixed value, so the two orders genuinely arrive at one state.
			// An index-derived label would make them different states and the
			// comparison would be asserting nothing.
			d.event(name, seq, [2]string{"label", "settled"})
		} else {
			d.event(name, seq)
		}
		d.nextPatch()
	}

	Expect(d.writeFrame(d.envelope(&pb.ResyncRequest{
		LastAppliedSeq: 1, Reason: pb.ResyncReason_CLIENT_REQUEST,
	}))).To(Succeed())
	snap := d.nextSnapshot()

	out := map[string]string{}
	for _, u := range snap.GetUpdates() {
		out[u.GetFragmentId()] = u.GetHtml()
	}
	Expect(out).NotTo(BeEmpty(), "the snapshot rendered no fragment")
	return out
}

// renderOnce renders a fragment outside the library, which is what makes the
// comparison meaningful: it is the application's own component being exercised,
// not the library's caching of it.
func renderOnce(f live.Fragment[tally], state tally) string {
	GinkgoHelper()
	var sb stringWriter
	component := f.Render(state)
	Expect(component).NotTo(BeNil())
	Expect(component.Render(context.Background(), &sb)).To(Succeed())
	return sb.String()
}

type stringWriter struct{ b []byte }

func (w *stringWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func (w *stringWriter) String() string { return string(w.b) }
