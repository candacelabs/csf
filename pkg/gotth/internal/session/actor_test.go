package session_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/render"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

var _ = Describe("A session actor", func() {
	var (
		app *testApp
		h   *harness
	)

	BeforeEach(func() {
		app = newTestApp()
		h = newHarness(app, session.DefaultLimits())
		h.start()
		DeferCleanup(h.stop)
	})

	Describe("establishment", func() {
		It("emits a snapshot as the first frame, carrying the session parameters", func() {
			snaps := h.sink.snapshots()
			Expect(snaps).To(HaveLen(1))

			s := snaps[0]
			Expect(s.GetServerSeq()).To(Equal(uint64(1)))
			Expect(s.GetPatchId()).To(Equal(uint64(1)))
			Expect(s.GetTransitionId()).To(Equal(uint64(1)))
			Expect(s.GetStateVersion()).To(Equal(uint64(1)))
			Expect(s.GetOrigin().GetKind()).To(Equal(pb.OriginKind_MOUNT))
			Expect(s.GetOrigin().GetSource()).To(Equal(protocol.SourceMount))
			Expect(s.GetHeartbeatIntervalMs()).To(Equal(uint32(20000)))
			Expect(s.GetAckWindow()).To(Equal(uint32(16)))
			Expect(s.GetUpdates()).To(HaveLen(1))
			Expect(s.GetUpdates()[0].GetHtml()).To(Equal("<b>hits 0</b>"))
		})

		It("carries no supersession range on a session's first snapshot", func() {
			s := h.sink.snapshots()[0]
			Expect(s.GetSupersededFromSeq()).To(BeZero())
			Expect(s.GetSupersededThroughSeq()).To(BeZero())
		})

		It("closes the session when the mount hook fails, rather than serving a half-built one", func() {
			failing := newTestApp()
			failing.initErr = errors.New("the database is down")
			broken := newHarness(failing, session.DefaultLimits())
			go broken.actor.Run(broken.ctx)

			Eventually(broken.closeRecords).Should(ContainElement(closeRecord{
				Code: protocol.CloseInternalError, Reason: "mount failed",
			}))
			Expect(broken.sink.errors()).NotTo(BeEmpty())
		})

		// BR-5, and the negative form of H-10: a snapshot that cannot be sent
		// leaves a connection with no first frame, so it must be closed rather
		// than served. The result of emitSnapshot used to be discarded, and Run
		// releases the read pump immediately afterwards, so the session sat open
		// with server_seq at 0 — the shipped client correctly sends nothing
		// without a sequence — until IdleTimeout evicted it thirty minutes later
		// as 4011 session_evicted, a code describing the wrong thing.
		It("closes the session when the mount snapshot cannot be sent, rather than serving a connection with no snapshot on it", func() {
			const overLimit = 1048576 + 1

			oversize := newTestApp(render.Fragment{
				ID: "counter",
				Render: func(_ context.Context, _ any, w io.Writer) error {
					_, err := w.Write([]byte(strings.Repeat("x", overLimit)))
					return err
				},
			})
			zombie := newHarness(oversize, session.DefaultLimits())
			go zombie.actor.Run(zombie.ctx)

			Eventually(zombie.closeRecords).Should(ContainElement(
				HaveField("Code", protocol.CloseInternalError)))
			Expect(zombie.sink.snapshots()).To(BeEmpty(),
				"a session with no snapshot on it reported one")

			errs := zombie.sink.errors()
			Expect(errs).NotTo(BeEmpty())
			Expect(errs[len(errs)-1].GetFatal()).To(BeTrue(),
				"the client was not told the session is over")
			Expect(errs[len(errs)-1].GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
		})
	})

	Describe("one transition", func() {
		It("advances the causal chain and patches the fragment that changed", func() {
			h.sendEvent("counter.increment")

			Eventually(h.sink.patches).Should(HaveLen(1))
			p := h.sink.patches()[0]

			Expect(p.GetServerSeq()).To(Equal(uint64(2)))
			Expect(p.GetPatchId()).To(Equal(uint64(2)))
			Expect(p.GetTransitionId()).To(Equal(uint64(2)))
			Expect(p.GetStateVersion()).To(Equal(uint64(2)))
			Expect(p.GetOrigin().GetKind()).To(Equal(pb.OriginKind_CLIENT_EVENT))
			Expect(p.GetOrigin().GetSource()).To(Equal("event:counter.increment"))
			Expect(p.GetOrigin().GetEventId()).To(Equal(uint64(1)))
			Expect(p.GetUpdates()[0].GetHtml()).To(Equal("<b>hits 1</b>"))
		})

		It("does not raise the state version for a transition that changed nothing", func() {
			h.sendEvent("counter.increment")
			Eventually(h.sink.patches).Should(HaveLen(1))

			h.sendEvent("counter.noop")
			h.sendEvent("counter.increment")
			Eventually(h.sink.patches).Should(HaveLen(2))

			Expect(h.sink.patches()[1].GetStateVersion()).To(Equal(uint64(3)),
				"a no-change transition raised the state version")
			Expect(h.sink.patches()[1].GetTransitionId()).To(Equal(uint64(4)),
				"a no-change transition did not advance the transition counter")
		})

		// BR-7. A pointer IS comparable in Go's sense, so the change-detection
		// fast path used to compare identity rather than value: for S = *Foo a
		// reducer that mutated in place and returned the same pointer — the
		// ordinary Go mistake, and the one the purity rule exists to forbid —
		// got "unchanged" for a change that happened. state_version froze at 1
		// and P4 was false on the wire, which is the unsafe direction
		// sameState's own comment says was avoided.
		//
		// What this does NOT repair is the aliasing: Mark still receives two
		// handles to one mutated value, so a Dirty declaration compares
		// something against itself and the fragment is never marked. That is
		// why this spec uses a fragment with no declaration — where every
		// fragment is force-marked — and why the constraint is documented at
		// live.Config's type parameter rather than worked around here. Only the
		// reducer can avoid it, by returning a new value.
		It("raises the state version for a pointer state a reducer mutated in place", func() {
			type ptrState struct{ N int }

			aliased := newTestApp(render.Fragment{
				ID: "counter",
				Render: func(_ context.Context, state any, w io.Writer) error {
					_, err := fmt.Fprintf(w, "<b>%d</b>", state.(*ptrState).N)
					return err
				},
			})
			aliased.pointerState = true
			aliased.initState = &ptrState{}
			aliased.reduce = func(state any, _ session.Event) (any, []session.Effect[subject]) {
				s := state.(*ptrState)
				s.N++
				return s, nil
			}

			ph := newHarness(aliased, session.DefaultLimits())
			ph.start()
			defer ph.stop()

			ph.sendEvent("counter.increment")
			Eventually(ph.sink.patches).Should(HaveLen(1))
			ph.sendEvent("counter.increment")
			Eventually(ph.sink.patches).Should(HaveLen(2))

			Expect(ph.sink.patches()[0].GetStateVersion()).To(Equal(uint64(2)),
				"a real state change left the state version frozen")
			Expect(ph.sink.patches()[1].GetStateVersion()).To(Equal(uint64(3)))
		})

		It("emits no patch when the render produced the bytes it last sent", func() {
			h.sendEvent("counter.relabel", &pb.EventField{Key: "label", Value: "hits"})

			Consistently(h.sink.patches, 100*time.Millisecond).Should(BeEmpty())
			Eventually(app.reducedNames).Should(ContainElement("counter.relabel"))
		})

		It("delivers the event's fields to the reducer, copied out of the wire data", func() {
			h.sendEvent("counter.relabel", &pb.EventField{Key: "label", Value: "clicks"})

			Eventually(h.sink.patches).Should(HaveLen(1))
			Expect(h.sink.patches()[0].GetUpdates()[0].GetHtml()).To(Equal("<b>clicks 0</b>"))
		})
	})

	Describe("the sequence", func() {
		// P3: per session, server_seq starts at 1 and rises by exactly one
		// across every sequenced frame.
		It("rises by exactly one across every sequenced frame", func() {
			// Six events, against a default window of sixteen: enough to see
			// the sequence advance and few enough to stay below the coalesce
			// threshold, which has its own specs and would otherwise merge
			// transitions here and make this spec about two things.
			for i := 0; i < 6; i++ {
				h.sendEvent("counter.increment")
			}
			Eventually(h.sink.patches).Should(HaveLen(6))

			seqs := []uint64{h.sink.snapshots()[0].GetServerSeq()}
			for _, p := range h.sink.patches() {
				seqs = append(seqs, p.GetServerSeq())
			}
			for i, s := range seqs {
				Expect(s).To(Equal(uint64(i+1)), "sequence %d is out of order", i)
			}
		})
	})

	Describe("acknowledgements", func() {
		It("closes the connection when a client acknowledges a patch that was never sent", func() {
			h.sendAck(99)

			Eventually(h.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseProtocolViolation)))
		})

		It("accepts a cumulative acknowledgement that repeats the high-water mark", func() {
			h.sendEvent("counter.increment")
			Eventually(h.sink.patches).Should(HaveLen(1))

			h.sendAck(2)
			h.sendAck(2)

			Consistently(h.closeRecords, 100*time.Millisecond).Should(BeEmpty())
		})
	})

	Describe("client telemetry", func() {
		It("drops a report naming a patch this session did not send", func() {
			h.sendTelemetry(9999)

			Consistently(h.closeRecords, 50*time.Millisecond).Should(BeEmpty())
			Expect(h.sink.errors()).To(BeEmpty(),
				"a forged telemetry report produced a client-visible error rather than being counted")
		})

		It("accepts a report naming a patch still inside the window", func() {
			h.sendEvent("counter.increment")
			Eventually(h.sink.patches).Should(HaveLen(1))

			h.sendTelemetry(h.sink.patches()[0].GetPatchId())

			Consistently(h.closeRecords, 50*time.Millisecond).Should(BeEmpty())
		})

		// BR-1. The order below is the shipped client's, not a convenience:
		// runtime.js applied() sends the acknowledgement and then the telemetry
		// report for the same patch. They arrive on different channels and the
		// actor's select imposes no order between them, so swapping the two
		// sends would not fix anything either — which is why this is pinned
		// server-side. Before the window stopped evicting on acknowledgement,
		// every one of these forty reports was counted as a forgery and drew a
		// Warn record accusing the client.
		It("accepts the report the shipped client sends, which acknowledges the patch first", func() {
			const patches = 40
			for i := 0; i < patches; i++ {
				h.sendEvent("counter.increment")
				Eventually(h.sink.patches).Should(HaveLen(i + 1))

				p := h.sink.patches()[i]
				h.sendAck(p.GetServerSeq())
				h.sendTelemetry(p.GetPatchId())
			}

			Eventually(func() int {
				return len(h.metrics.Observations("gotthlive_client_morph_duration_seconds"))
			}).Should(Equal(patches), "the client-side half of the latency budget recorded no data")

			Consistently(func() float64 {
				return h.metrics.Total("gotthlive_client_telemetry_dropped_total")
			}, 100*time.Millisecond).Should(BeZero(),
				"a legitimate telemetry report was counted as naming an unknown patch")
			Expect(h.logs.warnings()).NotTo(ContainElement(ContainSubstring("did not send")),
				"the client was accused of forging a report it sent correctly")
		})

		// The other half of H-11: retention is a bound, not an amnesty. A patch
		// evicted by age is as unknown as one that was never sent.
		It("still drops a report naming a patch older than the retention bound", func() {
			lim := session.DefaultLimits()
			for i := 0; i < lim.AckWindow+4; i++ {
				h.sendEvent("counter.increment")
				Eventually(h.sink.patches).Should(HaveLen(i + 1))
				h.sendAck(h.sink.patches()[i].GetServerSeq())
			}

			h.sendTelemetry(h.sink.patches()[0].GetPatchId())

			Eventually(func() float64 {
				return h.metrics.Total("gotthlive_client_telemetry_dropped_total")
			}).Should(Equal(float64(1)))
			Expect(h.sink.errors()).To(BeEmpty(),
				"a stale telemetry report produced a client-visible error rather than being counted")
		})
	})

	Describe("liveness and eviction", func() {
		It("sends a heartbeat on every tick", func() {
			h.tick()

			Eventually(func() int {
				n := 0
				for _, f := range h.sink.all() {
					if f.GetHeartbeat() != nil {
						n++
					}
				}
				return n
			}).Should(Equal(1))
		})

		It("closes a connection that has gone quiet past the heartbeat timeout", func() {
			h.clock.advance(51 * time.Second)
			h.tick()

			Eventually(h.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseHeartbeatTimeout)))
		})

		It("evicts a session that has been idle past the idle timeout", func() {
			h.clock.advance(31 * time.Minute)
			// Keep liveness fresh, so the eviction under test is idleness and
			// not a dead peer.
			h.sendAck(1)
			h.tick()

			Eventually(h.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseSessionEvicted)))
		})
	})

	Describe("when the transport fails under a write", func() {
		// The path a slow client reaches by a different route: the window is
		// not full, the connection simply broke. It must still close with a
		// code from the enumeration rather than ending unlabelled, because a
		// close nobody can name is the failure the enumeration exists to
		// prevent.
		It("closes with an enumerated code rather than ending silently", func() {
			h.sink.failWith(errors.New("connection reset by peer"))

			h.sendEvent("counter.increment")

			Eventually(h.closeRecords).ShouldNot(BeEmpty())
			Expect(h.closeRecords()[0].Code.Valid()).To(BeTrue(),
				"the connection ended with a code outside the enumeration")
			Expect(h.closeRecords()[0].Reason).NotTo(BeEmpty())
		})
	})

	// BR-3. The render pass used to clear the dirty bit and install the new
	// hash before the send, and Actor[subject].send's *InvalidFrameError branch
	// deliberately keeps the session alive. So a survivable send failure left
	// the renderer believing the client held markup that was never written to
	// the socket, and every later render of that fragment producing the same
	// bytes was suppressed — the region stale for the life of the connection,
	// with no metric saying so and no resync triggered.
	//
	// The trigger here is one fragment in the pass exceeding
	// FragmentUpdate.html's bound, which refuses the whole frame and takes the
	// other fragments' markup down with it. That is the general shape: the
	// fragment that goes silently stale need not be the one that broke.
	Describe("when a patch is refused after its fragments have rendered", func() {
		const overLimit = 1048576 + 1

		var stale *harness

		BeforeEach(func() {
			type wide struct {
				N    int
				Huge bool
			}

			blob := render.Fragment{
				ID: "blob",
				Render: func(_ context.Context, state any, w io.Writer) error {
					if state.(wide).Huge {
						_, err := w.Write([]byte(strings.Repeat("x", overLimit)))
						return err
					}
					_, err := io.WriteString(w, "<i>small</i>")
					return err
				},
			}
			counter := render.Fragment{
				ID: "counter",
				Render: func(_ context.Context, state any, w io.Writer) error {
					_, err := fmt.Fprintf(w, "<b>%d</b>", state.(wide).N)
					return err
				},
			}

			wideApp := newTestApp(counter, blob)
			wideApp.initState = wide{}
			wideApp.events = map[string]bool{"go.huge": true, "go.small": true}
			wideApp.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
				s := state.(wide)
				switch ev.Name {
				case "go.huge":
					s.N++
					s.Huge = true
				case "go.small":
					s.Huge = false
				}
				return s, nil
			}

			stale = newHarness(wideApp, session.DefaultLimits())
			stale.start()
			DeferCleanup(stale.stop)
		})

		It("renders those fragments again rather than suppressing them as already delivered", func() {
			stale.sendEvent("go.huge")

			// The frame is refused, the session survives, and the client is told
			// so much and no more.
			Eventually(stale.sink.errors).Should(HaveLen(1))
			Expect(stale.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
			Expect(stale.sink.patches()).To(BeEmpty())
			Expect(stale.closeRecords()).To(BeEmpty())

			// "counter" rendered <b>1</b> in that pass and the client never
			// received it. The next pass renders exactly the same bytes.
			stale.sendEvent("go.small")

			// Under the old commit-before-send, both fragments were suppressed
			// here and the pass produced no patch at all: this Eventually is
			// where it failed. "blob" is legitimately absent — the mount
			// snapshot delivered <i>small</i> and it has not moved since — and
			// that is the distinction the fix has to keep.
			Eventually(stale.sink.patches).Should(HaveLen(1))
			p := stale.sink.patches()[0]
			Expect(fragmentIDsOf(p.GetUpdates())).To(ConsistOf("counter"),
				"a fragment whose patch was refused was suppressed on the retry, "+
					"so that region is stale for the life of the connection")
			Expect(p.GetUpdates()[0].GetHtml()).To(Equal("<b>1</b>"))
		})

		It("says which regions are outstanding rather than going quiet", func() {
			stale.sendEvent("go.huge")

			Eventually(stale.logs.warnings).Should(ContainElement(
				ContainSubstring("a patch did not reach the client")))
		})
	})

	Describe("shutdown", func() {
		It("runs the teardown hook once, with the final state", func() {
			h.sendEvent("counter.increment")
			Eventually(h.sink.patches).Should(HaveLen(1))

			h.stop()

			app.mu.Lock()
			defer app.mu.Unlock()
			Expect(app.tornDown).To(BeTrue())
			Expect(app.finalState).To(Equal(counterState{N: 1, Label: "hits"}))
		})
	})

	Describe("the tracked memory figure", func() {
		It("sizes only the structures the library owns and can size exactly", func() {
			// Window 17 x 48, mailbox 64 x 8, acks 32 x 8, one fragment hash.
			//
			// Seventeen and not sixteen: the retained ring is AckWindow + 1, the
			// overshoot a provenance flush has always been allowed (BR-1). The
			// figure used to say sixteen, which understated a slot push could
			// already allocate.
			//
			// Forty-eight and not sixty-four: a slot is two sequence numbers and
			// a span reference. The uint32 of frame bytes and the int64 emitted
			// timestamp were written twice per patch and read nowhere, and the
			// eviction clock the slow-client stages actually read is
			// window.fullSince (REV-DEL finding 2).
			Expect(h.actor.TrackedBytes()).To(Equal(int64(816 + 512 + 256 + 8)))
		})
	})
})

var _ = Describe("The authorization hook", func() {
	// The property is structural rather than incidental: an event is
	// authorized before it occupies a mailbox slot, and there is no other way
	// into the mailbox from the wire.
	var (
		app *testApp
		h   *harness
	)

	BeforeEach(func() {
		app = newTestApp()
		h = newHarness(app, session.DefaultLimits())
	})

	AfterEach(func() { h.stop() })

	It("runs before the reducer for every event", func() {
		h.start()

		h.sendEvent("counter.increment")

		Eventually(app.authorizedNames).Should(ContainElement("counter.increment"))
		Eventually(app.reducedNames).Should(ContainElement("counter.increment"))
	})

	It("keeps a denied event out of the reducer entirely", func() {
		app.authorize = func(ctx context.Context, peer session.Peer[subject], event session.Event) error {
			return &session.DenyError{Reason: "not yours"}
		}
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_UNAUTHORIZED))
		Expect(h.sink.errors()[0].GetFatal()).To(BeFalse())
		Consistently(app.reducedNames, 100*time.Millisecond).Should(BeEmpty())
		Expect(h.sink.patches()).To(BeEmpty(), "a denied event produced a patch")
		Expect(h.closeRecords()).To(BeEmpty(), "an ordinary denial closed the connection")
	})

	It("closes the connection on a fatal denial", func() {
		app.authorize = func(ctx context.Context, peer session.Peer[subject], event session.Event) error {
			return &session.FatalDenyError{Reason: "forged identity"}
		}
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseUnauthorized)))
		Expect(app.reducedNames()).To(BeEmpty())
	})

	It("refuses an unregistered event name rather than ignoring it", func() {
		h.start()

		h.send(&pb.Frame{
			ProtocolVersion: protocol.Version,
			SessionId:       sessionIDBytes(),
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 1, Name: "counter.unknown", FragmentId: "counter", SeenServerSeq: 1,
			}},
		})

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_UNKNOWN_EVENT))
		Expect(app.authorizedNames()).To(BeEmpty(),
			"an unregistered name reached the authorization hook rather than being refused before it")
		Expect(app.reducedNames()).To(BeEmpty())
	})

	It("refuses an event naming a fragment the application does not declare", func() {
		h.start()

		h.send(&pb.Frame{
			ProtocolVersion: protocol.Version,
			SessionId:       sessionIDBytes(),
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 1, Name: "counter.increment", FragmentId: "nosuch", SeenServerSeq: 1,
			}},
		})

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_UNKNOWN_FRAGMENT))
		Expect(app.reducedNames()).To(BeEmpty())
	})

	It("refuses an event claiming to have seen a patch the session never sent", func() {
		h.start()

		h.send(&pb.Frame{
			ProtocolVersion: protocol.Version,
			SessionId:       sessionIDBytes(),
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 1, Name: "counter.increment", FragmentId: "counter", SeenServerSeq: 500,
			}},
		})

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_INVALID_FRAME))
		Expect(h.sink.patches()).To(BeEmpty())
	})

	It("authorizes a resync as a distinguished event kind rather than exempting it", func() {
		h.start()
		h.sendEvent("counter.increment")
		Eventually(h.sink.patches).Should(HaveLen(1))

		h.sendResync(1)

		Eventually(app.authorizedNames).Should(ContainElement(session.ResyncEventName))
	})

	It("does not authorize the frames that cannot reach a reducer", func() {
		h.start()
		h.sendEvent("counter.increment")
		Eventually(h.sink.patches).Should(HaveLen(1))

		before := len(app.authorizedNames())
		h.sendAck(2)
		h.sendTelemetry(2)
		h.send(&pb.Frame{
			ProtocolVersion: protocol.Version,
			SessionId:       sessionIDBytes(),
			Payload:         &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{Nonce: 1, IntervalMs: 20000}},
		})

		Consistently(func() int { return len(app.authorizedNames()) }, 100*time.Millisecond).
			Should(Equal(before))
	})
})

var _ = Describe("Panic containment", func() {
	var (
		app *testApp
		h   *harness
	)

	AfterEach(func() {
		if h != nil {
			h.stop()
		}
	})

	It("keeps the pre-transition state when a reducer panics", func() {
		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			if ev.Name == "counter.increment" {
				panic("reducer exploded")
			}
			return state, nil
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
		Expect(h.sink.errors()[0].GetEventId()).To(Equal(uint64(1)),
			"the error frame lost the causal chain")
		Expect(h.sink.patches()).To(BeEmpty())
		Expect(h.closeRecords()).To(BeEmpty(), "one panic closed the session")
	})

	It("closes the session once one site exhausts its panic budget", func() {
		app = newTestApp()
		app.reduce = func(state any, event session.Event) (any, []session.Effect[subject]) { panic("always") }
		lim := session.DefaultLimits()
		lim.PanicBudget = 3
		h = newHarness(app, lim)
		h.start()

		for i := 0; i < 3; i++ {
			h.sendEvent("counter.increment")
		}

		Eventually(h.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseInternalError)))
	})

	It("patches every other fragment when one fragment's render panics", func() {
		bad := render.Fragment{
			ID:     "bad",
			Render: func(ctx context.Context, state any, w io.Writer) error { panic("render exploded") },
		}
		app = newTestApp(bad, counterFragment())
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.patches).Should(HaveLen(1))
		Expect(h.sink.patches()[0].GetUpdates()).To(HaveLen(1))
		Expect(h.sink.patches()[0].GetUpdates()[0].GetFragmentId()).To(Equal("counter"))
	})
})

// RFC §9 requires an Error frame carrying the causal ID from every recovery,
// and until C-26 a render panic was the one recovery that produced none: it
// was logged and counted and the client was told nothing at all, so a region
// silently stopped updating.
var _ = Describe("The Error frame a render panic produces", func() {
	var h *harness

	AfterEach(func() {
		if h != nil {
			h.stop()
		}
	})

	It("names the event whose transition the render failed on", func() {
		h = newHarness(newTestApp(panickingRender("bad", "render exploded"), counterFragment()),
			generousBudget())
		h.start()
		// The mount snapshot renders every fragment, so the bad one has already
		// failed once. Everything after this is about the event's transition.
		Eventually(h.sink.errors).Should(HaveLen(1))

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(2))
		e := h.sink.errors()[1]
		Expect(e.GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
		Expect(e.GetEventId()).To(Equal(uint64(1)), "the frame lost the causal chain")
		Expect(e.GetClientRef()).To(Equal(uint64(1)))
		Expect(e.GetFatal()).To(BeFalse(), "a stale region is not a dead session")
		Expect(h.closeRecords()).To(BeEmpty())
	})

	It("carries no identifiers at all when no client frame caused the render", func() {
		h = newHarness(newTestApp(panickingRender("bad", "render exploded"), counterFragment()),
			generousBudget())
		h.start()

		Eventually(h.sink.errors).Should(HaveLen(1))
		e := h.sink.errors()[0]
		Expect(e.GetEventId()).To(BeZero(), "H-12: the mount snapshot has no causing event")
		Expect(e.GetClientRef()).To(BeZero())
	})

	// H-10 is the reason the report is deferred rather than made where the
	// failure is found: a fragment that panics during mount must not put an
	// Error frame in front of the Snapshot that establishes the session.
	It("still lets the snapshot be the first frame on the connection", func() {
		h = newHarness(newTestApp(panickingRender("bad", "render exploded"), counterFragment()),
			generousBudget())
		h.start()

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.all()[0].GetSnapshot()).NotTo(BeNil(),
			"H-10: the first frame on a connection is the Snapshot")
	})

	It("names the resync request when a resync snapshot's render panics", func() {
		h = newHarness(newTestApp(panickingRender("bad", "render exploded"), counterFragment()),
			generousBudget())
		h.start()
		Eventually(h.sink.errors).Should(HaveLen(1))

		h.sendEvent("counter.increment")
		Eventually(h.sink.patches).Should(HaveLen(1))
		Eventually(h.sink.errors).Should(HaveLen(2))

		h.sendResync(1)

		Eventually(h.sink.snapshots).Should(HaveLen(2))
		Eventually(h.sink.errors).Should(HaveLen(3))
		Expect(h.sink.errors()[2].GetEventId()).To(Equal(uint64(2)),
			"a resync is event-bearing (H-6), so the error it produced names its request")
		Expect(h.sink.errors()[2].GetClientRef()).To(Equal(uint64(2)))
	})

	It("reports a panicking dirty declaration as well as a panicking render", func() {
		h = newHarness(newTestApp(panickingDirty("bad", "dirty exploded"), counterFragment()),
			generousBudget())
		h.start()
		Consistently(h.sink.errors, 50*time.Millisecond).Should(BeEmpty(),
			"a dirty declaration is not consulted for a snapshot")

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetEventId()).To(Equal(uint64(1)))
		Expect(h.logs.renderFailures()).To(HaveLen(1))
		Expect(h.logs.renderFailures()[0]["site"]).To(Equal("dirty"),
			"the log keeps the distinction the frame does not")
	})

	// One frame per pass, not one per fragment. The alternative lets a single
	// broken helper shared by every fragment write once per fragment into a
	// connection it is about to close, and says nothing the first frame did not.
	It("emits one frame for a pass however many fragments failed in it", func() {
		h = newHarness(newTestApp(
			panickingRender("bad1", "boom one"),
			panickingRender("bad2", "boom two"),
			panickingRender("bad3", "boom three"),
			counterFragment()), generousBudget())
		h.start()

		Eventually(h.sink.errors).Should(HaveLen(1))
		Consistently(h.sink.errors, 50*time.Millisecond).Should(HaveLen(1))

		Expect(h.logs.renderFailures()).To(HaveLen(3),
			"every failure keeps its own log record")
	})

	// Coalescing the frames must not quietly coalesce the budget with them.
	// Four is chosen so the arithmetic distinguishes the two: three fragments
	// fail on the mount pass and three more on the event's pass, which is six
	// charges and a close if the budget counts failures, and two charges and a
	// session still serving if it had started counting frames.
	It("charges the budget once per failure, not once per frame", func() {
		lim := session.DefaultLimits()
		lim.PanicBudget = 4
		h = newHarness(newTestApp(
			panickingRender("bad1", "boom one"),
			panickingRender("bad2", "boom two"),
			panickingRender("bad3", "boom three"),
			counterFragment()), lim)
		h.start()
		Expect(h.closeRecords()).To(BeEmpty(), "three failures did not exhaust a budget of four")

		h.sendEvent("counter.increment")

		Eventually(h.closeRecords).Should(ContainElement(HaveField("Code", protocol.CloseInternalError)))
	})
})

// FR-23: "In dev mode the developer sees the stack; in prod mode the client
// sees a generic error." Both directions, over one code path, because a field
// that gates nothing reads as a shipped safety control (C-26).
var _ = Describe("Config.Dev", func() {
	var h *harness

	AfterEach(func() {
		if h != nil {
			h.stop()
		}
	})

	It("keeps a reducer's panic value and stack off the wire in production", func() {
		h = newHarness(panickingReducerApp("reducer exploded"), session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetMessage()).To(Equal("the transition failed"),
			"a fixed generic message, byte for byte")
	})

	It("puts a reducer's panic value and stack on the wire in dev mode", func() {
		h = newDevHarness(panickingReducerApp("reducer exploded"), session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(1))
		msg := h.sink.errors()[0].GetMessage()
		Expect(msg).To(HavePrefix("the transition failed\n"),
			"dev mode appends to the production message rather than replacing it")
		Expect(msg).To(ContainSubstring("reducer exploded"))
		Expect(msg).To(ContainSubstring("goroutine "), "no stack reached the frame")
	})

	It("keeps a render's panic value and stack off the wire in production", func() {
		h = newHarness(newTestApp(panickingRender("bad", "render exploded"), counterFragment()),
			generousBudget())
		h.start()

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetMessage()).
			To(Equal("a region of the page could not be rendered and is stale"))
	})

	It("puts a render's fragment, panic value and stack on the wire in dev mode", func() {
		h = newDevHarness(newTestApp(panickingRender("bad", "render exploded"), counterFragment()),
			generousBudget())
		h.start()

		Eventually(h.sink.errors).Should(HaveLen(1))
		msg := h.sink.errors()[0].GetMessage()
		Expect(msg).To(HavePrefix("a region of the page could not be rendered and is stale\n"))
		Expect(msg).To(ContainSubstring("fragment bad (render): render exploded"))
		Expect(msg).To(ContainSubstring("goroutine "))
	})

	It("says how many other fragments failed in the same pass", func() {
		h = newDevHarness(newTestApp(
			panickingRender("bad1", "boom one"),
			panickingRender("bad2", "boom two"),
			counterFragment()), generousBudget())
		h.start()

		Eventually(h.sink.errors).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetMessage()).
			To(ContainSubstring("(and 1 more in this pass; the log has them all)"))
	})

	// The schema bounds Error.message at 512 bytes and the framer re-checks
	// every constructed frame against it (§5.3), so a dev-mode stack must
	// arrive truncated rather than costing the frame.
	It("keeps a dev-mode frame inside the schema's message bound", func() {
		h = newDevHarness(panickingReducerApp(strings.Repeat("verbose panic value ", 200)),
			session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(1))
		msg := h.sink.errors()[0].GetMessage()
		Expect(len(msg)).To(BeNumerically("<=", 512))
		Expect(msg).To(HaveSuffix("..."))
		Expect(utf8.ValidString(msg)).To(BeTrue())
	})

	It("survives a panic value that is not ASCII", func() {
		h = newDevHarness(panickingReducerApp(strings.Repeat("é", 400)), session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.errors).Should(HaveLen(1))
		msg := h.sink.errors()[0].GetMessage()
		Expect(utf8.ValidString(msg)).To(BeTrue(),
			"a cut through a multi-byte rune would have failed the marshal, not the assertion")
		Expect(len(msg)).To(BeNumerically("<=", 512))
	})
})

// generousBudget takes the panic budget out of the way of a spec that is about
// what the client is told rather than about when a session closes.
func generousBudget() session.Limits {
	lim := session.DefaultLimits()
	lim.PanicBudget = 1000
	return lim
}

func panickingRender(id, message string) render.Fragment {
	return render.Fragment{
		ID:     id,
		Render: func(ctx context.Context, state any, w io.Writer) error { panic(message) },
	}
}

func panickingDirty(id, message string) render.Fragment {
	return render.Fragment{
		ID: id,
		Render: func(_ context.Context, _ any, w io.Writer) error {
			_, err := io.WriteString(w, "<i>fine</i>")
			return err
		},
		Dirty: func(prev, next any) bool { panic(message) },
	}
}

func panickingReducerApp(message string) *testApp {
	app := newTestApp()
	app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
		if ev.Name == "counter.increment" {
			panic(message)
		}
		return state, nil
	}
	return app
}

var _ = Describe("Effects", func() {
	var (
		app *testApp
		h   *harness
	)

	AfterEach(func() {
		if h != nil {
			h.stop()
		}
	})

	It("executes an effect at the actor boundary and folds its result back in as an event", func() {
		app = newTestApp()
		app.events["counter.increment"] = true
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			s := state.(counterState)
			switch ev.Name {
			case "counter.increment":
				return s, []session.Effect[subject]{app.effect(testEffect{Source: "test.fetch", Reply: "fetched"})}
			case "test.result":
				s.Label = ev.Fields[0].Value
				return s, nil
			}
			return s, nil
		}
		app.execute = func(_ context.Context, _ session.Peer[subject], e testEffect, emit session.Emit) error {
			return emit(session.Event{
				Name:   "test.result",
				Fields: []session.Field{{Key: "label", Value: e.Reply}},
			})
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.patches).Should(HaveLen(1))
		p := h.sink.patches()[0]
		Expect(p.GetOrigin().GetKind()).To(Equal(pb.OriginKind_EFFECT))
		Expect(p.GetOrigin().GetSource()).To(Equal("effect:test.fetch"))
		Expect(p.GetOrigin().GetEventId()).To(BeZero(),
			"a server-initiated patch claimed a client event identifier")
		Expect(p.GetUpdates()[0].GetHtml()).To(Equal("<b>fetched 0</b>"))
	})

	It("turns a panicking effect into a failure event rather than silence", func() {
		var seen []session.Event
		var mu sync.Mutex

		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			mu.Lock()
			seen = append(seen, ev)
			mu.Unlock()
			if ev.Name == "counter.increment" {
				return state, []session.Effect[subject]{app.effect(testEffect{Source: "test.explode"})}
			}
			return state, nil
		}
		app.execute = func(ctx context.Context, peer session.Peer[subject], effect testEffect, emit session.Emit) error {
			panic("effect exploded")
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(func() []string {
			mu.Lock()
			defer mu.Unlock()
			var names []string
			for _, e := range seen {
				names = append(names, e.Name)
			}
			return names
		}).Should(ContainElement(session.EffectFailedEvent))
	})

	// FR-58: "every library-produced error MUST name the session, the causal
	// ID where one exists, and the actionable next step." The effect-panic
	// record named the session and the effect and stopped there, so the one
	// identifier that reaches the interaction — the event whose transition
	// returned the effect — was held as a parameter of the function doing the
	// logging and never written down.
	//
	// The identifier is read out of the reducer rather than assumed, because
	// it is server-minted: comparing the log against a constant would assert
	// that the field is *a* number, which is the assertion that passes when
	// the wrong number is logged.
	It("names the event that scheduled a panicking effect in the log record", func() {
		var scheduling uint64
		var mu sync.Mutex

		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			if ev.Name == "counter.increment" {
				mu.Lock()
				scheduling = ev.ID
				mu.Unlock()
				return state, []session.Effect[subject]{app.effect(testEffect{Source: "test.explode"})}
			}
			return state, nil
		}
		app.execute = func(ctx context.Context, peer session.Peer[subject], effect testEffect, emit session.Emit) error {
			panic("effect exploded")
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(func() []map[string]any { return h.logs.bySite("effect") }).Should(HaveLen(1))
		rec := h.logs.bySite("effect")[0]

		mu.Lock()
		want := scheduling
		mu.Unlock()
		Expect(want).NotTo(BeZero(), "the harness never saw the scheduling event, so this spec proves nothing")

		Expect(rec).To(HaveKeyWithValue("scheduled_by", want),
			"the effect-panic record names the effect and not the event that scheduled it, so an "+
				"operator reading it can reach %q and cannot reach the interaction behind it (FR-58)",
			rec["effect_source"])
		Expect(rec).To(HaveKeyWithValue("effect_source", "test.explode"))
		Expect(rec).To(HaveKey("session_id"))
	})

	It("reports a failing effect to the reducer with its source and error", func() {
		var failure session.Event
		var mu sync.Mutex

		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			if ev.Name == session.EffectFailedEvent {
				mu.Lock()
				failure = ev
				mu.Unlock()
			}
			if ev.Name == "counter.increment" {
				return state, []session.Effect[subject]{app.effect(testEffect{Source: "test.fail"})}
			}
			return state, nil
		}
		app.execute = func(ctx context.Context, peer session.Peer[subject], effect testEffect, emit session.Emit) error {
			return errors.New("upstream refused")
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(func() string {
			mu.Lock()
			defer mu.Unlock()
			return failure.Name
		}).Should(Equal(session.EffectFailedEvent))

		mu.Lock()
		defer mu.Unlock()
		Expect(failure.Fields).To(ContainElement(session.Field{Key: "source", Value: "test.fail"}))
		Expect(failure.Fields).To(ContainElement(session.Field{Key: "error", Value: "upstream refused"}))
		Expect(failure.Fields).To(ContainElement(session.Field{Key: "retryable", Value: "false"}),
			"an unclassified failure must not invite a retry: the effect may have committed before it failed")
	})

	// BR-2. EffectSource() has no registration step and no bound anywhere
	// (protocol.md §3.3), and it is one half of an Origin.source the outbound
	// boundary refines. An effect that reports "Chat.Broadcast" used to run,
	// emit, and have its patch dropped on the actor goroutine as an
	// Error{INTERNAL} the application never heard about — the D-18 failure one
	// field over from where D-18 closed it. It is now the effect's own
	// deterministic failure, which is the contract every other effect failure
	// already has.
	DescribeTable("refuses an effect whose source cannot name an origin",
		func(source string) {
			var failure session.Event
			var mu sync.Mutex

			app = newTestApp()
			app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
				if ev.Name == session.EffectFailedEvent {
					mu.Lock()
					failure = ev
					mu.Unlock()
				}
				if ev.Name == "counter.increment" {
					return state, []session.Effect[subject]{app.effect(testEffect{Source: source})}
				}
				return state, nil
			}
			app.execute = func(ctx context.Context, peer session.Peer[subject], effect testEffect, emit session.Emit) error {
				return nil
			}
			h = newHarness(app, session.DefaultLimits())
			h.start()

			h.sendEvent("counter.increment")

			Eventually(func() string {
				mu.Lock()
				defer mu.Unlock()
				return failure.Name
			}).Should(Equal(session.EffectFailedEvent),
				"the application was told nothing about an effect that could never have patched")

			mu.Lock()
			defer mu.Unlock()
			Expect(failure.Fields).To(ContainElement(session.Field{Key: "source", Value: source}),
				"the failure event did not name the offending source")
			Expect(failure.Fields).To(ContainElement(session.Field{Key: "retryable", Value: "false"}),
				"a malformed source will be malformed again: retrying makes no progress")
			Expect(app.executeSeen).To(BeEmpty(), "the effect ran despite being unable to name its own origin")
			Expect(h.invalidFrames()).To(BeEmpty(),
				"application input reached the outbound boundary and was refused there")
			Expect(h.sink.errors()).To(BeEmpty(),
				"the client received an INTERNAL error about the application's own naming mistake")
		},
		Entry("upper case", "Chat.Broadcast"),
		Entry("a space", "chat broadcast"),
		Entry("a slash-free non-ASCII byte", "chat.caf\u00e9"),
		Entry("one byte past what the prefix leaves",
			strings.Repeat("s", protocol.MaxOriginSource-len(protocol.SourceEffectPrefix)+1)),
	)

	It("still patches for an effect source exactly at the bound the prefix leaves", func() {
		source := strings.Repeat("s", protocol.MaxOriginSource-len(protocol.SourceEffectPrefix))

		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			switch ev.Name {
			case "counter.increment":
				return state, []session.Effect[subject]{app.effect(testEffect{Source: source, Reply: "done"})}
			case "test.result":
				s := state.(counterState)
				s.Label = ev.Fields[0].Value
				return s, nil
			}
			return state, nil
		}
		app.execute = func(_ context.Context, _ session.Peer[subject], e testEffect, emit session.Emit) error {
			return emit(session.Event{
				Name:   "test.result",
				Fields: []session.Field{{Key: "label", Value: e.Reply}},
			})
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(h.sink.patches).Should(HaveLen(1))
		Expect(h.sink.patches()[0].GetOrigin().GetSource()).To(Equal("effect:" + source))
		Expect(h.invalidFrames()).To(BeEmpty())
	})

	// The other arm, and the one that has to be an explicit claim. A reducer
	// cannot tell a transient failure from a permanent one by looking at a
	// message, so the classification travels as a field and only an effect
	// that knows it is safe to run twice sets it.
	It("carries an effect's own transient classification through to the reducer", func() {
		var failure session.Event
		var mu sync.Mutex

		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			if ev.Name == session.EffectFailedEvent {
				mu.Lock()
				failure = ev
				mu.Unlock()
			}
			if ev.Name == "counter.increment" {
				return state, []session.Effect[subject]{app.effect(testEffect{Source: "test.flaky"})}
			}
			return state, nil
		}
		app.execute = func(ctx context.Context, peer session.Peer[subject], effect testEffect, emit session.Emit) error {
			// Wrapped, because an application reports its own context around a
			// failure and the mark has to survive that.
			return fmt.Errorf("publishing: %w",
				&session.RetryableError{Err: errors.New("the broker is reconnecting")})
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(func() string {
			mu.Lock()
			defer mu.Unlock()
			return failure.Name
		}).Should(Equal(session.EffectFailedEvent))

		mu.Lock()
		defer mu.Unlock()
		Expect(failure.Fields).To(ContainElement(session.Field{Key: "retryable", Value: "true"}))
		Expect(failure.Fields).To(ContainElement(
			session.Field{Key: "error", Value: "publishing: the broker is reconnecting"}),
			"the mark is a classification, not a prefix on the message")
	})

	// A panic is the one failure that is terminal on its own merits rather
	// than by default: re-running it re-runs the bug.
	It("classifies a panicking effect as terminal", func() {
		var failure session.Event
		var mu sync.Mutex

		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			if ev.Name == session.EffectFailedEvent {
				mu.Lock()
				failure = ev
				mu.Unlock()
			}
			if ev.Name == "counter.increment" {
				return state, []session.Effect[subject]{app.effect(testEffect{Source: "test.explode"})}
			}
			return state, nil
		}
		app.execute = func(ctx context.Context, peer session.Peer[subject], effect testEffect, emit session.Emit) error {
			panic("effect exploded")
		}
		h = newHarness(app, session.DefaultLimits())
		h.start()

		h.sendEvent("counter.increment")

		Eventually(func() string {
			mu.Lock()
			defer mu.Unlock()
			return failure.Name
		}).Should(Equal(session.EffectFailedEvent))

		mu.Lock()
		defer mu.Unlock()
		Expect(failure.Fields).To(ContainElement(session.Field{Key: "retryable", Value: "false"}))
	})

	It("does not block shutdown on an effect that will not return", func() {
		release := make(chan struct{})
		app = newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			return state, []session.Effect[subject]{app.effect(testEffect{Source: "test.hang"})}
		}
		app.execute = func(ctx context.Context, _ session.Peer[subject], _ testEffect, _ session.Emit) error {
			<-release
			return nil
		}
		lim := session.DefaultLimits()
		lim.EffectDrainTimeout = 50 * time.Millisecond
		h = newHarness(app, lim)
		h.start()

		h.sendEvent("counter.increment")
		Eventually(app.executeCount).Should(Equal(1))

		start := time.Now()
		h.stop()
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))

		close(release)
		h = nil
	})
})

func (t *testApp) executeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.executeSeen)
}

var _ = Describe("Concurrent event injection", func() {
	// The race detector proves nothing about code it never ran, so this spec
	// drives every input the actor has at once: events and acknowledgements
	// from several goroutines, effect results from the effect goroutines, and
	// ticks, all while the actor reduces and renders.
	It("keeps one owner for session state under every input at once", func() {
		app := newTestApp()
		app.reduce = func(state any, ev session.Event) (any, []session.Effect[subject]) {
			s := state.(counterState)
			switch ev.Name {
			case "counter.increment":
				s.N++
				return s, []session.Effect[subject]{app.effect(testEffect{Source: "test.echo", Reply: fmt.Sprint(s.N)})}
			case "test.result":
				s.Label = ev.Fields[0].Value
			}
			return s, nil
		}
		app.events["test.result"] = true
		app.execute = func(_ context.Context, _ session.Peer[subject], e testEffect, emit session.Emit) error {
			return emit(session.Event{
				Name:   "test.result",
				Fields: []session.Field{{Key: "label", Value: e.Reply}},
			})
		}

		h := newHarness(app, session.DefaultLimits())
		h.start()
		defer h.stop()

		const writers = 8
		var wg sync.WaitGroup
		var ingress sync.Mutex

		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				for i := 0; i < 40; i++ {
					// The read pump is one goroutine by construction, so the
					// specs hold to that here rather than testing a shape the
					// transport does not produce.
					ingress.Lock()
					h.sendEvent("counter.increment")
					ingress.Unlock()
				}
			}()
		}
		wg.Wait()

		Eventually(func() int { return len(h.sink.patches()) }, 3*time.Second).
			Should(BeNumerically(">", 0))
		Expect(h.closeRecords()).To(BeEmpty())
	})
})
