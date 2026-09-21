package conformance_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// C-31(b) / D-18: the flush trigger, once an application contributes.
//
// D-14 gave CoalesceFlushAt a validated range, and the arithmetic behind it was
// exact for the edges this library adds on its own: the trigger counted the
// deferred set and the frame carried one identifier more. It was not exact for
// an application's, and nothing said so. deferPatch folds an emitted event's
// Event.Contributing into pendingIDs, but the origin about to be emitted
// carries its own, which unionEdges merges in and which the count never saw —
// so a legal per-event Contributing and a legal CoalesceFlushAt could still
// build a frame the schema refuses. That is D-14's failure mode reached through
// an input D-14 did not bound.
//
// The property here is the invariant, stated over the wire: no frame this
// library builds names more contributing events than H-4 permits, in a run
// where the application contributes the largest list it is allowed on every
// single emission. It is the run the trigger has to survive, and it is written
// against `mustFlush` rather than against a number: revert the trigger to
// len(pendingIDs) and it fails, because the union it stopped counting is
// exactly the application's.
// ---------------------------------------------------------------------------

// contributingEffect is scheduled by every increment and emits one event
// carrying a full-size contributing list.
func contributingEffect(label func() string, take func(n int) []uint64) live.Effect[qaUser] {
	return live.Effect[qaUser]{
		Source: "qa.contributes",
		Run: func(ctx context.Context, sess live.Session[qaUser], emit live.Emitter) error {
			return emit(live.Event{
				Name:         "qa.relabel",
				Fields:       live.NewFields(map[string]string{"label": label()}),
				Contributing: take(session.MaxEventContributing),
			})
		},
	}
}

// distinctIDs hands out blocks of identifiers that never repeat between
// emissions, so the union really grows rather than deduplicating back down to
// one block. Overlap is what made D-18's observable bound depend on luck, and a
// spec that let the blocks collide would be measuring the dedup instead of the
// trigger.
type distinctIDs struct{ next atomic.Uint64 }

func (d *distinctIDs) take(n int) []uint64 {
	base := d.next.Add(uint64(n)) - uint64(n) + 1_000_000
	out := make([]uint64, n)
	for i := range out {
		out[i] = base + uint64(i)
	}
	return out
}

var _ = Describe("The coalescing flush trigger, with an application contributing to every emission", func() {
	It("holds H-4 over a run where every emitted event carries the largest legal Contributing", func() {
		metrics := obstest.NewMetrics()
		var pool distinctIDs
		var labels atomic.Uint64

		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Metrics = metrics
			// The widest setting an operator may configure, so the headroom
			// this spec is about is the smallest it can legally be.
			c.Limits.CoalesceFlushAt = session.MaxCoalesceFlushAt
			// A two-frame window is above its coalesce threshold from the
			// mount snapshot onward, so every patch below is one the flush
			// trigger produced rather than one the ordinary path emitted.
			c.Limits.AckWindow = 2
			c.Limits.MailboxDepth = 8192
			c.Limits.MaxEventsPerSecond = 1e6
			c.Limits.EventBurst = 1 << 20

			c.Reduce = func(state tally, ev live.Event) (tally, []live.Effect[qaUser]) {
				switch ev.Name {
				case "qa.increment":
					// No state change here: the patch this spec is about is
					// the one the effect's emission causes.
					return state, []live.Effect[qaUser]{contributingEffect(
						func() string { return fmt.Sprintf("v%d", labels.Add(1)) },
						pool.take,
					)}
				case "qa.relabel":
					state.Label = ev.Fields.Get("label")
				}
				return state, nil
			}
		})

		// Enough emissions to reach the trigger several times over. Each one
		// adds MaxEventContributing identifiers plus the scheduledBy edge, so
		// the union crosses CoalesceFlushAt after roughly a fifteenth of these.
		for i := 0; i < 120; i++ {
			d.event("qa.increment", mountSeq)
		}
		d.drainUntilQuiet(3 * time.Second)

		cs := d.carriers()
		widest := 0
		for _, c := range cs {
			if n := len(c.origin.GetContributingEventIds()); n > widest {
				widest = n
			}
		}

		// H-4 directly, and the assertion that fails first when the trigger
		// stops counting the frame. It fails with the number, because the
		// number is how far past the ceiling the uncounted term put it.
		Expect(widest).To(BeNumerically("<=", protocol.CoalesceFlushCeiling),
			"a frame named %d contributing events against H-4's ceiling of %d: the flush "+
				"trigger is counting something other than the union the frame carries, and "+
				"the difference is what the application contributed to the event being emitted",
			widest, protocol.CoalesceFlushCeiling)

		// Non-vacuity, and it is a real risk here: if nothing coalesced, every
		// frame carries one scheduledBy edge and the assertion above passes
		// over a run that never approached the bound.
		Expect(widest).To(BeNumerically(">", session.MaxEventContributing+1),
			"the widest union was %d, which is one emission's worth: this run never coalesced, "+
				"so the trigger was never exercised and the ceiling was never approached", widest)

		// The failure D-14 describes, asserted absent on the path D-18 opened.
		// An Error{INTERNAL} here is the flush trigger having become the
		// emission failure it exists to prevent.
		for _, f := range mustFrames(d) {
			Expect(f.GetError()).To(BeNil(),
				"the session answered with %v: at a legal CoalesceFlushAt and a legal "+
					"Event.Contributing, no frame this library builds may fail its own validator",
				f.GetError())
		}

		// C-31(c)'s half of the same story. The counter's documentation used to
		// say any non-zero value was a library bug; an application driving it
		// from its own input is what made that false, and this is the run that
		// used to drive it.
		Expect(metrics.Registered()).To(ContainElement("gotthlive_outbound_validation_failed_total"),
			"the instrument was never created, so a zero total below would prove nothing")
		Expect(metrics.Total("gotthlive_outbound_validation_failed_total")).To(BeZero(),
			"an application contributing within its own bound made this library refuse a frame "+
				"it had built, and page an operator about it")
	})
})
