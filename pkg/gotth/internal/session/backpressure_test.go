package session_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/render"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

var _ = Describe("Backpressure", func() {
	// The property under test is that a render which cannot be sent is skipped
	// rather than queued: memory under a slow client is proportional to the
	// number of fragments and not to the number of pending patches.
	//
	// The ladder has three stages and the specs below walk all three, because
	// the defect that produced them was a first stage that existed in the
	// design, existed in the window, and was called by nothing — so coalescing
	// engaged only at a completely full window and the wire was burstier than
	// the design says.
	//
	// With an eight-frame window the stages are: emit per transition below four
	// outstanding, coalesce in pairs from four, stop entirely at eight.
	const window = 8

	var (
		app *testApp
		h   *harness
		lim session.Limits
	)

	BeforeEach(func() {
		lim = session.DefaultLimits()
		lim.AckWindow = window
		app = newTestApp()
		h = newHarness(app, lim)
		h.start()
		DeferCleanup(h.stop)
	})

	// toCoalesceThreshold drives the window to exactly half full. The mount
	// snapshot occupies the first slot, so three more patches reach four.
	toCoalesceThreshold := func() {
		GinkgoHelper()
		for i := 0; i < window/2-1; i++ {
			h.sendEvent("counter.increment")
		}
		Eventually(h.sink.patches).Should(HaveLen(window/2 - 1))
	}

	// toFull drives the window from the coalesce threshold to completely full.
	// Above the threshold transitions pair up, so it takes two events a frame.
	toFull := func() {
		GinkgoHelper()
		toCoalesceThreshold()
		for i := 0; i < window; i++ {
			h.sendEvent("counter.increment")
		}
		Eventually(h.sink.patches).Should(HaveLen(window - 1))
	}

	Describe("below half the window", func() {
		It("emits one patch per transition", func() {
			for i := 0; i < window/2-1; i++ {
				h.sendEvent("counter.increment")
			}

			Eventually(h.sink.patches).Should(HaveLen(window/2 - 1))
			for _, p := range h.sink.patches() {
				Expect(p.GetOrigin().GetKind()).To(Equal(pb.OriginKind_CLIENT_EVENT))
				Expect(p.GetOrigin().GetContributingEventIds()).To(BeEmpty(),
					"a patch below the coalesce threshold merged something")
			}
		})
	})

	Describe("at half the window — the coalesce stage", func() {
		It("merges transitions in pairs rather than emitting one frame each", func() {
			toCoalesceThreshold()
			before := len(h.sink.patches())

			for i := 0; i < 4; i++ {
				h.sendEvent("counter.increment")
			}

			// Four transitions, two frames. That the ladder's first stage does
			// anything at all is the whole of the defect this spec pins.
			Eventually(func() int { return len(h.sink.patches()) }).Should(Equal(before + 2))
			Consistently(func() int { return len(h.sink.patches()) }, 100*time.Millisecond).
				Should(Equal(before + 2))
		})

		It("carries the merged transition's event in the patch that absorbed it", func() {
			toCoalesceThreshold()
			before := len(h.sink.patches())

			h.sendEvent("counter.increment")
			h.sendEvent("counter.increment")

			Eventually(func() int { return len(h.sink.patches()) }).Should(Equal(before + 1))
			merged := h.sink.patches()[before]

			Expect(merged.GetOrigin().GetContributingEventIds()).To(HaveLen(1),
				"the deferred transition was dropped rather than merged")
			Expect(merged.GetOrigin().GetContributingEventIds()).NotTo(
				ContainElement(merged.GetOrigin().GetEventId()),
				"an event was counted both as the cause and as a contributor")
		})

		It("renders the merged patch from current state, not from the state it deferred at", func() {
			toCoalesceThreshold()
			at := len(h.sink.patches())

			h.sendEvent("counter.increment")
			h.sendEvent("counter.increment")

			Eventually(func() int { return len(h.sink.patches()) }).Should(Equal(at + 1))
			Expect(h.sink.patches()[at].GetUpdates()[0].GetHtml()).
				To(Equal("<b>hits " + itoa(at+2) + "</b>"))
		})

		It("does not tell the application anything: this stage is relief, not distress", func() {
			toCoalesceThreshold()

			h.sendEvent("counter.increment")
			h.sendEvent("counter.increment")

			Consistently(app.reducedNames, 100*time.Millisecond).
				ShouldNot(ContainElement(protocol.SourceSlowClient))
		})
	})

	Describe("at a full window — the degrade stage", func() {
		It("stops emitting entirely, and keeps reducing", func() {
			toFull()
			before := len(h.sink.patches())
			reduced := len(app.reducedNames())

			for i := 0; i < 10; i++ {
				h.sendEvent("counter.increment")
			}

			Eventually(func() int { return len(app.reducedNames()) }).
				Should(BeNumerically(">=", reduced+10))
			Consistently(func() int { return len(h.sink.patches()) }, 100*time.Millisecond).
				Should(Equal(before), "the actor emitted past the window bound")
		})

		It("synthesizes a backpressure event rather than letting a reducer read the window", func() {
			toFull()

			h.sendEvent("counter.increment")

			Eventually(app.reducedNames).Should(ContainElement(protocol.SourceSlowClient))
		})

		It("renders from current state when an acknowledgement re-opens the window", func() {
			toFull()
			before := len(h.sink.patches())

			const extra = 10
			for i := 0; i < extra; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(app.reducedNames).Should(ContainElement(protocol.SourceSlowClient))
			Expect(h.sink.patches()).To(HaveLen(before))

			h.sendAck(uint64(window))

			// The assertion is about the value, not the frame count: how many
			// frames the resumption takes depends on how much of the mailbox
			// the actor had drained when the acknowledgement arrived, and
			// pinning that would be pinning a schedule. What must hold is that
			// the client ends up looking at current state.
			total := window/2 - 1 + window + extra
			Eventually(func() string {
				patches := h.sink.patches()
				if len(patches) == 0 {
					return ""
				}
				return patches[len(patches)-1].GetUpdates()[0].GetHtml()
			}).Should(Equal("<b>hits "+itoa(total)+"</b>"),
				"the deferred render did not come from current state")
		})

		It("tells the application when the client recovers", func() {
			toFull()
			h.sendEvent("counter.increment")
			Eventually(app.reducedNames).Should(ContainElement(protocol.SourceSlowClient))

			h.sendAck(uint64(window))

			Eventually(app.reducedNames).Should(ContainElement(protocol.SourceClientRecovered))
		})

		It("evicts a client whose window has stayed full past the grace period", func() {
			toFull()
			h.sendEvent("counter.increment")
			Eventually(app.reducedNames).Should(ContainElement(protocol.SourceSlowClient))

			h.clock.advance(31 * time.Second)
			h.tick()

			Eventually(h.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseSlowClient)))
		})
	})

	Describe("the whole ladder", func() {
		// The property the specs above share, asserted once as a sequence, so
		// that a change collapsing two stages into one fails here rather than
		// in whichever spec happens to notice.
		It("visits emit, then coalesce, then degrade, in that order", func() {
			for i := 0; i < window/2-1; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(h.sink.patches).Should(HaveLen(window/2 - 1))

			By("coalescing: transitions outnumber the patches they produce")
			at := len(h.sink.patches())
			for i := 0; i < 4; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(func() int { return len(h.sink.patches()) }).Should(Equal(at + 2))

			By("degrading: the window fills and emission stops entirely")
			for i := 0; i < 40; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(app.reducedNames).Should(ContainElement(protocol.SourceSlowClient))

			stalled := len(h.sink.patches())
			Expect(stalled).To(Equal(window-1), "the window did not stop at its bound")
			Consistently(func() int { return len(h.sink.patches()) }, 100*time.Millisecond).
				Should(Equal(stalled))
		})
	})

	Describe("the coalescing flush trigger", func() {
		// The contributing-event union has a schema ceiling that is reachable
		// in ordinary operation, so it is a flush rather than a truncation:
		// truncating would lose provenance silently and erroring would let a
		// slow client kill its own session by a path nobody designed.
		It("emits a coalesced patch rather than growing the union past its ceiling", func() {
			small := session.DefaultLimits()
			small.AckWindow = 2
			small.CoalesceFlushAt = 8
			flushApp := newTestApp()
			fh := newHarness(flushApp, small)
			fh.start()
			defer fh.stop()

			// A two-frame window is above its coalesce threshold from the
			// mount snapshot onward, so nothing here emits on the ordinary
			// path: every patch below is one the flush trigger produced.
			for i := 0; i < 40; i++ {
				fh.sendEvent("counter.increment")
			}

			Eventually(func() int { return len(fh.sink.patches()) }).Should(BeNumerically(">", 0),
				"the union grew past the flush threshold without emitting")

			for _, p := range fh.sink.patches() {
				Expect(len(p.GetOrigin().GetContributingEventIds())).
					To(BeNumerically("<=", protocol.CoalesceFlushCeiling),
						"a patch carried more contributing events than the schema permits")
			}
			Expect(fh.closeRecords()).To(BeEmpty(),
				"a slow client killed its own session through the coalescing path")
		})

		// U-3. The headroom between MaxCoalesceFlushAt and the schema ceiling is
		// exactly one element, and nothing in the repository drove the union
		// anywhere near it: the spec above asserts "<= CoalesceFlushCeiling",
		// which every union including tiny ones satisfies. This constructs the
		// worst case the derivation in limits.go describes and checks the two
		// things that make it a derivation rather than a hope — that the widest
		// legal union is exactly the ceiling, and that the outbound boundary
		// accepts it.
		//
		// The shape, term by term:
		//
		//	e0 emits, schedules the effect, and starts filling the window. Its
		//	   identifier is individually patched and therefore NOT in the
		//	   deferred set, so the scheduledBy edge the library prepends is a
		//	   new identifier rather than a duplicate.
		//	four more events finish filling the window. Above the coalesce
		//	   threshold and below full they alternate defer/emit, so each
		//	   deferral is consumed by the next emission and the deferred set is
		//	   empty again when the window fills.
		//	F - 1 events then degrade — a full window defers unconditionally and
		//	   the trigger has not fired — leaving exactly F - 1 identifiers
		//	   owed: one short of the flush, by construction.
		//	the effect's emission arrives carrying scheduledBy plus
		//	   MaxEventContributing application identifiers: 65 more.
		//
		// (F - 1) + 1 + 65 = 1024 = CoalesceFlushCeiling, and checkListBounds
		// accepts it because it refuses only a list longer than the bound.
		It("carries exactly the widest union the schema permits, and the boundary accepts it", func() {
			const appIDs = session.MaxEventContributing

			wide := session.DefaultLimits()
			// Four is the smallest window with a coalesce stage below full, so
			// the fill phase is short and the degrade phase is where the union
			// accumulates.
			wide.AckWindow = 4
			wide.CoalesceFlushAt = session.MaxCoalesceFlushAt
			// The rate limit and the mailbox are not what this spec is about,
			// and at these depths their defaults would be.
			wide.MaxEventsPerSecond = 1e6
			wide.EventBurst = 1 << 16
			wide.MailboxDepth = 1 << 12

			deferrals := session.MaxCoalesceFlushAt

			release := make(chan struct{})
			var releaseOnce sync.Once
			letGo := func() { releaseOnce.Do(func() { close(release) }) }

			widest := newTestApp()
			widest.events = map[string]bool{"counter.increment": true, "schedule": true}
			widest.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
				s := state.(counterState)
				s.N++
				if ev.Name == "schedule" {
					return s, []session.Effect[subject]{widest.effect(testEffect{Source: "test.wait"})}
				}
				return s, nil
			}
			widest.execute = func(_ context.Context, _ session.Peer[subject], _ testEffect, emit session.Emit) error {
				<-release
				// Identifiers no client event minted, so none of them is already
				// in the deferred set and the union is the sum rather than less.
				contributing := make([]uint64, 0, appIDs)
				for i := 0; i < appIDs; i++ {
					contributing = append(contributing, uint64(1_000_000+i))
				}
				return emit(session.Event{Name: "counter.increment", Contributing: contributing})
			}

			wh := newHarness(widest, wide)
			wh.start()
			defer func() { letGo(); wh.stop() }()

			// e0 plus four: the window ends full, with nothing owed.
			wh.sendEvent("schedule")
			for i := 0; i < 4; i++ {
				wh.sendEvent("counter.increment")
			}
			Eventually(wh.sink.patches).Should(HaveLen(3),
				"the window did not fill the way the ladder's stages say it does")

			for i := 0; i < deferrals; i++ {
				wh.sendEvent("counter.increment")
			}
			// At least, not exactly: the first degradation synthesizes a
			// backpressure event into the session's own mailbox, and that is a
			// transition too. It carries no event identifier, so it defers
			// without contributing to the union — which is why the arithmetic
			// below is unaffected by it and this assertion is a floor.
			Eventually(func() int { return len(widest.reducedNames()) }, 10*time.Second).
				Should(BeNumerically(">=", deferrals+5), "not every event reached the reducer")
			Consistently(wh.sink.patches, 100*time.Millisecond).Should(HaveLen(3),
				"a transition emitted before the union reached the flush trigger")

			letGo()

			Eventually(wh.sink.patches, 10*time.Second).Should(HaveLen(4))
			flushed := wh.sink.patches()[3]

			Expect(flushed.GetOrigin().GetKind()).To(Equal(pb.OriginKind_EFFECT))
			Expect(flushed.GetOrigin().GetContributingEventIds()).
				To(HaveLen(protocol.CoalesceFlushCeiling),
					"the widest legal union is not the ceiling the derivation claims")
			Expect(wh.invalidFrames()).To(BeEmpty(),
				"the outbound boundary refused the widest union the derivation permits")
			Expect(wh.sink.errors()).To(BeEmpty())
			Expect(wh.closeRecords()).To(BeEmpty())
		})

		// The derivation, as arithmetic, beside the run that exercises it. If
		// either constant moves without the other, this is the cheaper failure.
		It("leaves exactly one element of headroom in its own terms", func() {
			Expect(session.MaxCoalesceFlushAt + 1 + session.MaxEventContributing).
				To(Equal(protocol.CoalesceFlushCeiling))
		})

		// BR-4. takePending cleared the deferred state unconditionally, and
		// emitPatch has two exits after it that never emit. This is the first:
		// a flushing transition whose render is fully suppressed. The fragments
		// were marked dirty by a real state change and rendered to the bytes
		// already on the wire, so the union that had just been taken was
		// discarded with the local — absent from the wire and from the
		// provenance row, which carried the pre-union origin. H-4 calls the
		// bound "a flush trigger, never a truncation"; P5 states the union over
		// a run as set equality.
		It("carries a fully suppressed flush's provenance on the next patch", func() {
			// A fragment that renders only N, and a Label that changes state
			// without changing markup. That is what makes a real transition
			// render to bytes the client already holds.
			narrow := session.DefaultLimits()
			narrow.AckWindow = 4

			quiet := newTestApp(render.Fragment{
				ID: "counter",
				Render: func(_ context.Context, state any, w io.Writer) error {
					_, err := fmt.Fprintf(w, "<b>%d</b>", state.(counterState).N)
					return err
				},
			})
			qh := newHarness(quiet, narrow)
			qh.start()
			defer qh.stop()

			// e1 emits and takes the window to the coalesce threshold.
			qh.sendEvent("counter.increment")
			Eventually(qh.sink.patches).Should(HaveLen(1))

			// e2 changes the label — a real state change — and is deferred.
			qh.sendEvent("counter.relabel", &pb.EventField{Key: "label", Value: "a"})
			Consistently(qh.sink.patches, 100*time.Millisecond).Should(HaveLen(1))

			// e3 changes the label again and flushes, and the flush renders to
			// the bytes already on the wire, so it emits nothing.
			qh.sendEvent("counter.relabel", &pb.EventField{Key: "label", Value: "b"})
			Consistently(qh.sink.patches, 100*time.Millisecond).Should(HaveLen(1))

			// e4 moves N but the window is still at the coalesce threshold, so
			// it defers in turn; e5 is the flush that finally has markup to
			// carry. It owes e2 and e3, both of which changed state and neither
			// of which was individually patched.
			qh.sendEvent("counter.increment")
			qh.sendEvent("counter.increment")
			Eventually(qh.sink.patches).Should(HaveLen(2))

			seen := map[uint64]bool{}
			for _, p := range qh.sink.patches() {
				seen[p.GetOrigin().GetEventId()] = true
				for _, id := range p.GetOrigin().GetContributingEventIds() {
					seen[id] = true
				}
			}
			Expect(seen[2]).To(BeTrue(), "the deferred transition's contributing edge was dropped")
			Expect(seen[3]).To(BeTrue(), "the suppressed flush's own edge was dropped")
		})

		// The second exit: the frame was built, carried the whole union, and
		// the send failed survivably. The union must survive with it.
		It("carries a refused patch's provenance on the next one", func() {
			const overLimit = 1048576 + 1

			type wide struct {
				N    int
				Huge bool
			}

			narrow := session.DefaultLimits()
			narrow.AckWindow = 4

			refused := newTestApp(render.Fragment{
				ID: "counter",
				Render: func(_ context.Context, state any, w io.Writer) error {
					s := state.(wide)
					if s.Huge {
						_, err := w.Write([]byte(strings.Repeat("x", overLimit)))
						return err
					}
					_, err := fmt.Fprintf(w, "<i>%d</i>", s.N)
					return err
				},
			})
			refused.initState = wide{}
			refused.events = map[string]bool{"bump": true, "go.huge": true, "go.small": true}
			refused.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
				s := state.(wide)
				switch ev.Name {
				case "bump":
					s.N++
				case "go.huge":
					s.N++
					s.Huge = true
				case "go.small":
					s.N++
					s.Huge = false
				}
				return s, nil
			}

			rh := newHarness(refused, narrow)
			rh.start()
			defer rh.stop()

			rh.sendEvent("bump") // e1, emits, window at the coalesce threshold
			Eventually(rh.sink.patches).Should(HaveLen(1))

			rh.sendEvent("bump")    // e2, deferred
			rh.sendEvent("go.huge") // e3, flushes — and the frame is refused
			Eventually(rh.sink.errors).Should(HaveLen(1))
			Expect(rh.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
			Expect(rh.sink.patches()).To(HaveLen(1))

			// e4 renders small again but the window is still at the coalesce
			// threshold, so it defers; e5 is the flush that emits.
			rh.sendEvent("go.small")
			rh.sendEvent("bump")
			Eventually(rh.sink.patches).Should(HaveLen(2))

			seen := map[uint64]bool{}
			for _, p := range rh.sink.patches() {
				seen[p.GetOrigin().GetEventId()] = true
				for _, id := range p.GetOrigin().GetContributingEventIds() {
					seen[id] = true
				}
			}
			Expect(seen[2]).To(BeTrue(), "the union a refused frame carried was discarded with it")
			Expect(seen[3]).To(BeTrue(), "the refused transition's own edge was dropped")
			Expect(rh.closeRecords()).To(BeEmpty())
		})
	})

	Describe("deferred work with no acknowledgement coming", func() {
		It("is flushed by the heartbeat rather than waiting indefinitely", func() {
			toCoalesceThreshold()
			at := len(h.sink.patches())

			h.sendEvent("counter.increment")
			Consistently(func() int { return len(h.sink.patches()) }, 100*time.Millisecond).
				Should(Equal(at), "the transition was not deferred at all")

			h.tick()

			Eventually(func() int { return len(h.sink.patches()) }).Should(Equal(at + 1))
		})
	})

	Describe("the mailbox", func() {
		It("drops rather than blocks when it is full, and says so with a typed error", func() {
			block := make(chan struct{})
			blocked := newTestApp()
			blocked.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
				if ev.Name == "counter.relabel" {
					<-block
				}
				return state, nil
			}
			small := session.DefaultLimits()
			small.MailboxDepth = 2
			small.MaxEventsPerSecond = 1e6
			small.EventBurst = 1e6

			bh := newHarness(blocked, small)
			bh.start()
			defer func() {
				close(block)
				bh.stop()
			}()

			// Wedge the actor, then send far more than the mailbox holds. The
			// assertion that matters is that the ingress returns at all: a
			// blocking send here would stall the read pump and with it the
			// connection's own liveness handling.
			bh.sendEvent("counter.relabel")
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(done)
				for i := 0; i < 50; i++ {
					bh.sendEvent("counter.increment")
				}
			}()

			Eventually(done, 2*time.Second).Should(BeClosed(),
				"the ingress blocked on a full mailbox")
			Eventually(bh.sink.errors).ShouldNot(BeEmpty())
			Expect(bh.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_RATE_LIMITED))
		})
	})

	Describe("the acknowledgement channel", func() {
		It("drops an acknowledgement rather than blocking, because acknowledgements are cumulative", func() {
			block := make(chan struct{})
			blocked := newTestApp()
			blocked.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
				if ev.Name == "counter.relabel" {
					<-block
				}
				return state, nil
			}
			small := session.DefaultLimits()
			small.AckChannelDepth = 2

			bh := newHarness(blocked, small)
			bh.start()
			defer func() {
				close(block)
				bh.stop()
			}()

			bh.sendEvent("counter.relabel")

			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				defer close(done)
				for i := 0; i < 100; i++ {
					bh.sendAck(1)
				}
			}()

			Eventually(done, 2*time.Second).Should(BeClosed(),
				"the ingress blocked on a full acknowledgement channel")
		})
	})

	Describe("the inbound rate limit", func() {
		It("refuses events past the bucket and closes on a sustained flood", func() {
			tight := session.DefaultLimits()
			tight.MaxEventsPerSecond = 1
			tight.EventBurst = 2

			th := newHarness(newTestApp(), tight)
			th.start()
			defer th.stop()

			for i := 0; i < 12; i++ {
				th.sendEvent("counter.increment")
			}

			Eventually(th.sink.errors).ShouldNot(BeEmpty())
			Expect(th.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_RATE_LIMITED))
			Eventually(th.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseRateLimited)))
		})
	})
})

// itoa keeps the expected-markup assertions readable without pulling strconv
// into a file that is otherwise about frames.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
