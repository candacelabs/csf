package conformance_test

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// What an authenticated client can do to the session it is entitled to.
//
// Every spec here is an abuse case from a client that passed the origin check,
// the identity hook and the CSRF hook. That is the interesting adversary: the
// unauthenticated one is stopped at the handshake, and the authenticated one is
// the reason FR-51's limits exist.
// ---------------------------------------------------------------------------

var _ = Describe("An event flood from an authenticated client", func() {
	It("engages the rate limit, answers typed errors, and closes on sustained abuse", func() {
		d := dial(func(c *live.Config[tally, qaUser]) {
			// A small bucket so the behaviour is reachable in a test rather
			// than only under a load generator. The shape is the default's.
			c.Limits.MaxEventsPerSecond = 1
			c.Limits.EventBurst = 4
		})

		for i := 0; i < 120; i++ {
			if err := d.writeFrame(d.envelope(&pb.Event{
				ClientRef: uint64(i + 1), Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
			})); err != nil {
				break
			}
		}

		var rateLimited, patches int
		deadline := time.After(10 * time.Second)
	collect:
		for {
			select {
			case <-deadline:
				break collect
			default:
			}
			f, err := d.readErr(2 * time.Second)
			if err != nil {
				break collect
			}
			if e := f.GetError(); e != nil && e.GetCode() == pb.ErrorCode_RATE_LIMITED {
				rateLimited++
			}
			if f.GetPatch() != nil {
				patches++
			}
		}

		Expect(rateLimited).To(BeNumerically(">", 0),
			"120 events against a burst of 4 produced no RATE_LIMITED error")
		Expect(patches).To(BeNumerically("<=", 6),
			"the rate limit did not bound the work: %d patches came back", patches)
		Expect(d.closed(10*time.Second)).To(BeTrue(),
			"sustained flooding must end in a close, not in an endless stream of refusals")
	})

	// The error frames a refusal produces must still name the interaction they
	// refused. An error that cannot be tied to an event is FR-58's defect.
	It("names the refused interaction on every rejection", func() {
		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Limits.MaxEventsPerSecond = 1
			c.Limits.EventBurst = 2
		})

		for i := 0; i < 8; i++ {
			if err := d.writeFrame(d.envelope(&pb.Event{
				ClientRef: uint64(100 + i), Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
			})); err != nil {
				break
			}
		}

		var checked int
		for i := 0; i < 12; i++ {
			f, err := d.readErr(2 * time.Second)
			if err != nil {
				break
			}
			e := f.GetError()
			if e == nil || e.GetCode() != pb.ErrorCode_RATE_LIMITED {
				continue
			}
			checked++
			Expect(e.GetEventId()).NotTo(BeZero(),
				"a rate-limit error carries no event id, so nothing can say what was refused")
			Expect(e.GetClientRef()).NotTo(BeZero(),
				"a rate-limit error carries no client_ref, so the browser cannot correlate it")
			Expect(e.GetMessage()).NotTo(BeEmpty())
		}
		Expect(checked).To(BeNumerically(">", 0))
	})
})

var _ = Describe("A mailbox at its bound", func() {
	// The mailbox is bounded and must reject rather than block: blocking the
	// read pump would stall the connection's own liveness handling, which is
	// the failure mode that turns one slow session into a stuck one.
	It("rejects rather than blocking when the actor cannot keep up", func() {
		release := make(chan struct{})
		var released bool
		defer func() {
			if !released {
				close(release)
			}
		}()

		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Limits.MailboxDepth = 1
			c.Limits.MaxEventsPerSecond = 100000
			c.Limits.EventBurst = 100000
			c.Reduce = func(s tally, ev live.Event) (tally, []live.Effect[qaUser]) {
				if ev.Name == "qa.increment" {
					<-release
				}
				s.N++
				return s, nil
			}
		})

		for i := 0; i < 40; i++ {
			if err := d.writeFrame(d.envelope(&pb.Event{
				ClientRef: uint64(i + 1), Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
			})); err != nil {
				break
			}
		}

		var saturated int
		for i := 0; i < 60; i++ {
			f, err := d.readErr(3 * time.Second)
			if err != nil {
				break
			}
			if e := f.GetError(); e != nil && e.GetCode() == pb.ErrorCode_RATE_LIMITED {
				saturated++
				Expect(e.GetEventId()).NotTo(BeZero())
			}
			if saturated >= 5 {
				break
			}
		}

		Expect(saturated).To(BeNumerically(">", 0),
			"a mailbox of depth 1 under 40 events produced no saturation error: it blocked instead of rejecting")

		released = true
		close(release)
	})
})

var _ = Describe("A ResyncRequest storm (H-14)", func() {
	// protocol.md states the amplification test in so many words: fifty resync
	// requests a second from one authenticated client must not produce fifty
	// full renders. Resync is the one client frame that costs work proportional
	// to the whole state, so it carries a budget of its own.
	It("does not turn fifty requests into fifty full renders", func() {
		d := dial(nil)

		// Open a real gap first: a request describing no gap is answered with
		// an Ack and would not exercise the limiter at all.
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()
		Expect(d.highestSeq()).To(BeNumerically(">=", 2))

		for i := 0; i < 50; i++ {
			if err := d.writeFrame(d.envelope(&pb.ResyncRequest{
				LastAppliedSeq: 1, Reason: pb.ResyncReason_GAP,
			})); err != nil {
				break
			}
		}

		d.drainUntilQuiet(2 * time.Second)

		snapshots := len(d.snapshots())
		// One mount snapshot plus at most the resync burst.
		Expect(snapshots).To(BeNumerically("<=", 1+live.DefaultLimits().ResyncBurst+1),
			"fifty resync requests produced %d snapshots: the H-14 bucket did not engage", snapshots)

		var rateLimited int
		for _, f := range mustFrames(d) {
			if e := f.GetError(); e != nil && e.GetCode() == pb.ErrorCode_RATE_LIMITED {
				rateLimited++
			}
		}
		Expect(rateLimited).To(BeNumerically(">", 0),
			"the resync budget refused nothing and said nothing")
	})

	// The independence of the two buckets is the property, not merely that a
	// bucket exists. A resync storm must not consume the ordinary event budget,
	// and vice versa, or one becomes a denial-of-service against the other.
	It("keeps the resync budget independent of the event budget", func() {
		d := dial(nil)

		d.event("qa.increment", d.highestSeq())
		d.nextPatch()

		// Exhaust the resync bucket without reaching the close threshold. The
		// budget is a burst of three and the connection closes after three
		// times the burst in consecutive denials, so six requests empties the
		// bucket and leaves the session open — which is the state this property
		// is about. Twenty would close it, correctly, and prove nothing here.
		for i := 0; i < 6; i++ {
			if err := d.writeFrame(d.envelope(&pb.ResyncRequest{
				LastAppliedSeq: 1, Reason: pb.ResyncReason_GAP,
			})); err != nil {
				break
			}
		}
		d.drainUntilQuiet(time.Second)

		// An ordinary event must still be served: its own bucket is untouched.
		d.event("qa.increment", d.highestSeq())

		var served bool
		for i := 0; i < 20; i++ {
			f, err := d.readErr(2 * time.Second)
			if err != nil {
				break
			}
			if p := f.GetPatch(); p != nil && p.GetOrigin().GetSource() == "event:qa.increment" {
				served = true
				break
			}
		}
		Expect(served).To(BeTrue(),
			"an ordinary event was refused after a resync storm: the two budgets are not independent")
	})
})

var _ = Describe("Acknowledgements that a well-behaved client would not send", func() {
	// H-7. An acknowledgement is a cumulative high-water mark, so a repeat is
	// harmless and a decrease is not a mistake a real client makes.
	It("tolerates a repeated acknowledgement", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()
		high := d.highestSeq()

		d.ack(high)
		d.ack(high)
		d.ack(high)

		// The session must still serve. A close here would mean an idempotent
		// signal was treated as a violation.
		d.event("qa.increment", high)
		Expect(d.nextPatch()).NotTo(BeNil())
	})

	It("closes on an acknowledgement that goes backwards (H-7)", func() {
		d := dial(nil)
		for i := 0; i < 3; i++ {
			d.event("qa.increment", d.highestSeq())
			d.nextPatch()
		}
		high := d.highestSeq()
		Expect(high).To(BeNumerically(">=", 4))

		d.ack(high)
		d.drainUntilQuiet(200 * time.Millisecond)
		_ = d.writeFrame(d.envelope(&pb.Ack{ServerSeq: high - 2}))

		Expect(d.closed(5*time.Second)).To(BeTrue(),
			"an acknowledgement below the high-water mark must close the connection")
	})

	It("closes on an acknowledgement of a patch that was never sent (H-7)", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()

		_ = d.writeFrame(d.envelope(&pb.Ack{ServerSeq: d.highestSeq() + 500}))

		Expect(d.closed(5*time.Second)).To(BeTrue(),
			"a forged acknowledgement must close the connection, not be ignored")
	})
})

var _ = Describe("Client telemetry, which is entirely untrusted (H-11)", func() {
	It("drops a report naming a patch this session never sent", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		d.nextPatch()

		Expect(d.writeFrame(d.envelope(&pb.ClientTelemetry{
			PatchId: 999999, MorphMicros: 1000, ApplyMicros: 1000,
		}))).To(Succeed())

		// The session survives and keeps serving: a forged report is dropped
		// and counted, never used to fabricate a span and never fatal.
		d.drainUntilQuiet(300 * time.Millisecond)
		d.event("qa.increment", d.highestSeq())
		Expect(d.nextPatch()).NotTo(BeNil())
	})

	It("accepts a report naming a patch that is still in the window", func() {
		d := dial(nil)
		d.event("qa.increment", d.highestSeq())
		patch := d.nextPatch()

		Expect(d.writeFrame(d.envelope(&pb.ClientTelemetry{
			PatchId: patch.GetPatchId(), MorphMicros: 2500, ApplyMicros: 3000,
		}))).To(Succeed())

		d.drainUntilQuiet(300 * time.Millisecond)
		d.event("qa.increment", d.highestSeq())
		Expect(d.nextPatch()).NotTo(BeNil())
	})
})

// mustFrames returns everything captured, for specs that scan rather than wait.
func mustFrames(d *driven) []*pb.Frame {
	frames, _ := d.captured()
	return frames
}

// ---------------------------------------------------------------------------
// D-14: the Limits an application is allowed to set, held against §7 P5.
//
// This replaces the PIt QA-1 pre-registered in provenance_test.go, whose note
// asked for exactly this: "un-pend this when either live.New rejects a
// CoalesceFlushAt above the ceiling or Normalize clamps it to one". New now
// rejects, so the property is stated in two halves — the values an application
// may set, and what happens to the ones it may not.
//
// The measurement that fixed the boundary, over the repro below at 4,000
// unacknowledged transitions followed by a resync:
//
//	CoalesceFlushAt   largest union on the wire   union on wire / swallowed
//	           512                          513              3,978 / 3,978
//	          1023                         1024              3,982 / 3,982
//	          1024                          899                907 / 3,982
//	          4000                (resync refused)                8 / 1,385
//
// The trigger counted deferred transitions; the frame it forced carried one
// identifier more than it counted, because takePending folds in the origin of
// the transition being emitted at the time. So 1023 was the largest setting
// whose flush produced a frame the schema accepts, and it was a boundary that
// was measured rather than chosen.
//
// C-31 moved the boundary and the reason is arithmetic rather than measurement
// being overturned. The trigger now counts the union the frame will carry
// (Actor.unionReaches), which removes the "+1" discrepancy above — the same
// provenance reaches the wire, one flush earlier — and an application may now
// contribute up to session.MaxEventContributing identifiers to the event being
// emitted, which is a second term the old arithmetic did not have. So the
// headroom is 1 + MaxEventContributing rather than 1, and MaxCoalesceFlushAt is
// 959. The table above is kept as the record of what was measured under the old
// trigger; the spec below is written against the constants, so it moves with
// them.
// ---------------------------------------------------------------------------

var _ = Describe("The coalescing flush trigger an application configures", func() {
	It("holds P5 at the largest CoalesceFlushAt an application may set", func() {
		d := dial(func(c *live.Config[tally, qaUser]) {
			c.Limits.CoalesceFlushAt = session.MaxCoalesceFlushAt
			c.Limits.MailboxDepth = 8192
			c.Limits.MaxEventsPerSecond = 1e6
			c.Limits.EventBurst = 1 << 20
		})
		// Enough unacknowledged transitions to reach the trigger twice and
		// leave a third batch for the resync to flush, so both flush paths —
		// emitPatch's and emitSnapshot's — are exercised at the boundary
		// rather than only the one that happens to fire first.
		for i := 0; i < 3000; i++ {
			d.event("qa.increment", mountSeq)
		}
		d.drainUntilQuiet(2 * time.Second)

		// The resync is written out rather than taken through
		// provenance_test.go's resync helper, which waits for a Snapshot and
		// fails with a read timeout when none comes. Under the defect the
		// answer is an Error rather than a Snapshot, and a spec about
		// provenance loss should say so in those words rather than in the
		// harness's.
		Expect(d.writeFrame(d.envelope(&pb.ResyncRequest{
			LastAppliedSeq: mountSeq, Reason: pb.ResyncReason_GAP,
		}))).To(Succeed())
		d.drainUntilQuiet(2 * time.Second)

		cs := d.carriers()

		// H-4 directly: no frame carries more contributing identifiers than
		// the schema permits. This is the assertion that fails first when the
		// boundary moves, and it fails with the number.
		widest := 0
		for _, c := range cs {
			if n := len(c.origin.GetContributingEventIds()); n > widest {
				widest = n
			}
		}
		Expect(widest).To(BeNumerically("<=", protocol.CoalesceFlushCeiling),
			"a frame named %d contributing events, above H-4's ceiling of %d: MaxCoalesceFlushAt "+
				"leaves room for one more maximal emission on top of the trigger, and this says "+
				"the room is not enough",
			widest, protocol.CoalesceFlushCeiling)
		Expect(widest).To(BeNumerically(">", 0),
			"no frame carried a contributing set at all, so this run proves nothing about P5")

		// No Error frame: the failure mode D-14 describes announces itself to
		// the client as a non-fatal Error{INTERNAL} where a Snapshot was
		// asked for, which is what makes it look like a transient blip rather
		// than provenance loss.
		for _, f := range mustFrames(d) {
			Expect(f.GetError()).To(BeNil(),
				"the session answered with %v: at a legal CoalesceFlushAt no frame this library "+
					"builds should be one it then refuses to send", f.GetError())
		}

		// P5 itself: the union on the wire is exactly the set of transitions
		// that changed state and got no patch of their own.
		onWire := make([]uint64, 0)
		for id := range contributingCounts(cs) {
			onWire = append(onWire, id)
		}
		swallowed := swallowedEvents(d.logs.provenance())
		Expect(swallowed).NotTo(BeEmpty(),
			"nothing was swallowed, so the coalescing ladder was never entered and P5 is vacuous here")
		Expect(onWire).To(ConsistOf(swallowed),
			"%d transitions were swallowed and %d identifiers reached the wire: the flush that "+
				"carries the union built a frame the outbound validator refused, and the deferred "+
				"set was already taken when it did",
			len(swallowed), len(onWire))
	})

	// The other half. "Every CoalesceFlushAt an application is allowed to set"
	// is only a property if the ones it is not allowed to set cannot reach a
	// session, and rejecting at construction is what makes that true: there is
	// no running session at a value above the boundary to hold a property
	// against.
	It("refuses at construction the CoalesceFlushAt that would lose the union", func() {
		cfg := qaConfig()
		cfg.Limits.CoalesceFlushAt = 4000

		_, err := live.New(cfg)

		var cfgErr *live.ConfigError
		Expect(errors.As(err, &cfgErr)).To(BeTrue(),
			"live.New accepted CoalesceFlushAt 4000, which is D-14's measured configuration: "+
				"1,385 transitions swallowed, 8 identifiers on the wire, and a resync answered "+
				"with Error{INTERNAL}; got %v", err)
		Expect(cfgErr.Field).To(Equal("Limits.CoalesceFlushAt"))
	})
})
