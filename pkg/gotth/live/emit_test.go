package live_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// C-31(a) / D-18: what an Emitter accepts from an application.
//
// The emit closure already rejected three server-minted fields an application
// must leave alone. It accepted a Contributing list of any length, and that was
// the defect: the list is folded into a coalescing union with a schema ceiling,
// so an over-long one built a frame ValidateOutbound refused — on the actor
// goroutine, long after this call returned nil.
//
// Measured by L9-1 on default limits, before this change: 1,200 identifiers
// gave patches=0, errors=1, a non-fatal Error{INTERNAL}, a state change the
// client never saw, and emit returning nil. 1,024 passed, but only because
// unionEdges deduped the library's own scheduledBy edge against one the
// application happened to list — so the effective bound depended on accidental
// overlap between two sets neither party could see.
//
// These specs run the whole path rather than calling the closure, because the
// closure is not reachable from outside the package and because what is being
// asserted is that the failure arrives somewhere an application can act on it:
// as the Emitter's error, and then as the effect-failure event the reducer
// handles.
// ---------------------------------------------------------------------------

const outboundInvalidMetric = "gotthlive_outbound_validation_failed_total"

// ids builds n distinct plausible event identifiers. They start above any
// identifier this session will mint, so nothing here passes by colliding with
// a real one — which is the accident that made 1,024 look legal.
func ids(n int) []uint64 {
	out := make([]uint64, n)
	for i := range out {
		out[i] = uint64(100_000 + i)
	}
	return out
}

// emitting mounts an application whose effect emits ev exactly once, and
// returns the mounted session, the metrics recorder, and the error the Emitter
// gave back.
//
// The reducer folds both outcomes into the label, so one patch says which
// happened: an accepted emission relabels, a rejected one arrives as
// EffectFailedEvent because the effect's Run returns the Emitter's error
// unchanged.
// That is the contract this rejection is supposed to join, and reading it off
// the rendered fragment is how a spec sees the application's side of it.
func emitting(ev live.Event) (*mounted, *obstest.Metrics, chan error) {
	GinkgoHelper()

	metrics := obstest.NewMetrics()
	returned := make(chan error, 1)

	inject := func(_ context.Context, _ live.Session[user], emit live.Emitter) error {
		err := emit(ev)
		returned <- err
		return err
	}

	app := mount(func(c *live.Config[counter, user]) {
		c.Metrics = metrics
		c.Reduce = func(state counter, e live.Event) (counter, []live.Effect[user]) {
			switch e.Name {
			case "counter.increment":
				return state, []live.Effect[user]{logEffect(inject)}
			case "counter.relabel":
				state.Label = "emitted"
			case live.EffectFailedEvent:
				state.Label = "refused"
			}
			return state, nil
		}
	})
	app.send("counter.increment", nil)
	return app, metrics, returned
}

// framesUntilPatch reads until the first Patch arrives, returning everything
// read on the way to it.
//
// This harness has no background pump, so this is how a spec asserts about a
// frame that must not be there: read up to the frame that must be, and look at
// what arrived first. An Error the session emitted instead of the patch cannot
// hide behind the patch, because there would be no patch.
func framesUntilPatch(m *mounted) ([]*pb.Frame, *pb.Patch) {
	GinkgoHelper()
	var seen []*pb.Frame
	for {
		f := m.next()
		seen = append(seen, f)
		if p := f.GetPatch(); p != nil {
			return seen, p
		}
	}
}

// relabel is the event an effect emits: a registered name and a payload, so
// the transition it causes really changes state and really produces a patch.
func relabel(contributing []uint64) live.Event {
	return live.Event{
		Name:         "counter.relabel",
		Fields:       live.NewFields(map[string]string{"label": "from the effect"}),
		Contributing: contributing,
	}
}

var _ = Describe("The Emitter, given an event an application built", func() {
	DescribeTable("refuses a field the server owns, telling the effect which one",
		func(ev live.Event, wants ...string) {
			app, _, returned := emitting(ev)
			defer app.stop()

			var err error
			Eventually(returned, 5*time.Second).Should(Receive(&err))
			Expect(err).To(HaveOccurred(),
				"the emission was accepted, so the effect that made the mistake was told nothing")
			for _, want := range wants {
				Expect(err).To(MatchError(ContainSubstring(want)))
			}
		},

		// The three checks that were already here. They had no spec at all,
		// which is why the fourth is written beside them rather than alone: a
		// list of rejections where only the newest is checked is a list that
		// loses its older entries to the next refactor.
		Entry("a server-minted causal identifier",
			live.Event{Name: "counter.relabel", ID: 7},
			"Event.ID", "minted by the server"),
		Entry("a timestamp the boundary stamps",
			live.Event{Name: "counter.relabel", At: time.Unix(1, 0)},
			"Event.At", "leave it zero"),
		Entry("zero among the contributing identifiers",
			relabel([]uint64{12, 0, 14}),
			"listed 0 in Event.Contributing"),

		// C-31(a). The count and the limit are both in the message, because an
		// application that listed 1,200 needs to know what the number should
		// have been, not merely that 1,200 was wrong (FR-58).
		Entry("more contributing identifiers than one event may claim",
			relabel(ids(session.MaxEventContributing+1)),
			"Event.Contributing", "65", "64"),
		Entry("L9-1's measured 1,200",
			relabel(ids(1200)),
			"1200", "64"),
	)

	// The boundary, from both sides. 64 is not a round number chosen to look
	// safe: it is H-4's bound on every other repeated field in the schema, and
	// it is subtracted from the coalescing headroom, which is why one more is
	// refused rather than tolerated.
	It("accepts a list exactly at the bound, and carries it to the wire", func() {
		want := ids(session.MaxEventContributing)
		app, metrics, returned := emitting(relabel(want))
		defer app.stop()

		var err error
		Eventually(returned, 5*time.Second).Should(Receive(&err))
		Expect(err).NotTo(HaveOccurred())

		_, patch := framesUntilPatch(app)

		Expect(patch.GetOrigin().GetKind()).To(Equal(pb.OriginKind_EFFECT))
		Expect(patch.GetUpdates()[0].GetHtml()).To(Equal("<b>emitted 0</b>"))
		Expect(patch.GetOrigin().GetContributingEventIds()).To(ContainElements(want),
			"the emission was accepted and %d of its contributing identifiers did not reach "+
				"the frame: a bound that admits a list and then drops part of it is the "+
				"truncation the flush trigger exists to avoid", len(want))
		Expect(metrics.Total(outboundInvalidMetric)).To(BeZero())
	})

	It("refuses one identifier more, and nothing reaches the wire malformed", func() {
		app, metrics, returned := emitting(relabel(ids(session.MaxEventContributing + 1)))
		defer app.stop()

		var err error
		Eventually(returned, 5*time.Second).Should(Receive(&err))
		Expect(err).To(HaveOccurred())

		// The failure arrives as an ordinary event the reducer handled, which
		// is the whole point of rejecting at the emit path rather than at the
		// flush: the application's own code decides what to do about it.
		seen, patch := framesUntilPatch(app)
		Expect(patch.GetUpdates()[0].GetHtml()).To(Equal("<b>refused 0</b>"))

		// D-18's signature on the wire, asserted absent. Under the defect the
		// client was sent a non-fatal Error{INTERNAL} instead of the patch —
		// which reads as a transient blip rather than as the application
		// mistake it was.
		for _, f := range seen {
			Expect(f.GetError()).To(BeNil(),
				"the session answered an over-long Contributing with %v: the emission is "+
					"refused before a frame is built, so no frame can fail to be built",
				f.GetError())
		}

		// The metric an operator is paged on. It is documented as never being
		// the client's doing, and until this change an application could drive
		// it from its own input.
		Expect(metrics.Registered()).To(ContainElement(outboundInvalidMetric),
			"the instrument was never created, so a zero total below would prove nothing")
		Expect(metrics.Total(outboundInvalidMetric)).To(BeZero(),
			"an application's own mistake incremented the counter whose documentation "+
				"sends the reader to this library's issue tracker")
	})

	It("refuses L9-1's measured 1,200 the same way, and says nothing about luck", func() {
		app, metrics, returned := emitting(relabel(ids(1200)))
		defer app.stop()

		// Deterministic: the answer does not depend on whether any of the
		// 1,200 happens to equal the scheduledBy edge the library prepends.
		// Before this change 1,024 passed for exactly that reason and 1,200
		// failed, so the observable bound was a function of two sets neither
		// the application nor the library could compare.
		var err error
		Eventually(returned, 5*time.Second).Should(Receive(&err))
		Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("listed %d identifiers", 1200))))

		seen, patch := framesUntilPatch(app)
		Expect(patch.GetUpdates()[0].GetHtml()).To(Equal("<b>refused 0</b>"))
		for _, f := range seen {
			Expect(f.GetError()).To(BeNil())
		}
		Expect(metrics.Total(outboundInvalidMetric)).To(BeZero())
	})
})
