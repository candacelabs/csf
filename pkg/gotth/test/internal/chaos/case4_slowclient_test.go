package chaos_test

import (
	"fmt"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/obstest"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// PRD Phase 3, case 4:
//
//	Slow client (throttled to a stated bandwidth) → server queue bounded,
//	server memory bounded, other sessions unaffected, slow session degraded or
//	closed per a defined policy — never the process (FR-51).
//
// The stated bandwidth is 2,048 bytes per second on the server→client
// direction, applied at a TCP relay rather than by a client that reads slowly.
// That distinction is what makes the case real: a client that simply does not
// call Read still drains its kernel receive buffer, so the server's writes keep
// succeeding for as long as that buffer holds, and the backpressure the test
// claims to be applying arrives late and in a lump. Pacing the bytes downstream
// is what puts the stall where the protocol has to meet it.
//
// # What the measurement found, and why this file is shaped around it
//
// RFC §7.4's ladder has three stages: coalesce at half the window, degrade at a
// full window, evict at 4009 when the window has been "continuously full" past
// slow_client_grace. The first two fire exactly as designed against a client at
// 2 KB/s. The third does not fire at all, and cannot: window.noteFullness
// clears fullSince the moment depth drops below the bound, Actor.onAck calls it
// on every acknowledgement, and a client that acknowledges at ANY nonzero rate
// therefore resets the grace clock before it can expire. Measured: 24 s at
// 2 KB/s with grace at 3 s, depth pinned at 16, 55 synthesized slow-client
// events, 406 coalesced patches, and no close.
//
// That is D-26. FR-51's own disjunction — "degraded or closed" — is satisfied by
// the degrade stage, so the requirement holds; RFC §7.4's third row does not
// describe what the code does against the client it was written for. The two
// specs below measure the boundary from both sides, so the reachable and
// unreachable halves are each held by something that can fail.
var _ = Describe("A slow client (PRD case 4, FR-51)", func() {

	const throttleBytesPerSecond = 2048

	It("bounds the queue, the heap and the blast radius against a client at a stated 2 KB/s, and does not evict it (D-26)", func() {
		rec := obstest.NewMetrics()
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Metrics = rec
			cfg.Logger = nil
			cfg.Limits.AckWindow = 16
			// Three seconds rather than the default thirty, so that "the grace
			// period never expires" is a claim about the mechanism and not about
			// the spec being too short to see it.
			cfg.Limits.SlowClientGrace = 3 * time.Second
			cfg.Limits.WriteDeadline = 10 * time.Second
			cfg.Limits.HeartbeatInterval = time.Second // D-23: one second is the protocol floor
			cfg.Limits.HeartbeatTimeout = 120 * time.Second
		})

		r := newRelay(s.addr())
		r.throttleTo(throttleBytesPerSecond)

		slow := dialWire(r.addr(), wireOpts{acks: ackAuto})
		fast := dialWire(s.addr(), wireOpts{acks: ackAuto})

		// A high-frequency server-initiated update stream on the slow session:
		// FR-62's dashboard shape, which is the workload the backpressure ladder
		// was designed against.
		slow.startTicks(5*time.Millisecond, 4000, false)
		// Enough ticks to outlast the whole spec: the fast session's job is to
		// still be advancing at the END, so a stream that runs out is a false
		// failure that reads as "the slow client starved it".
		fast.startTicks(20*time.Millisecond, 100000, false)

		// Long enough for the grace period to have expired four times over.
		const observation = 15 * time.Second
		time.Sleep(observation)

		// The queue bound. gotthlive_outbound_window_depth is the exported
		// signal, and the claim is that it never exceeded the configured window
		// by more than the one frame a provenance flush is allowed to push past
		// it (RFC §7.4, window.push).
		var maxDepth float64
		for _, m := range rec.Observations("gotthlive_outbound_window_depth") {
			if m.Value > maxDepth {
				maxDepth = m.Value
			}
		}
		Expect(maxDepth).To(BeNumerically("<=", 17),
			"the outbound window reached %v against a configured bound of 16", maxDepth)
		Expect(maxDepth).To(BeNumerically(">=", 16),
			"the window never filled, so nothing about backpressure was exercised")

		// Degraded, per §7.4's second stage and §7.5's application-visible half.
		Expect(rec.Total("gotthlive_slow_client_events_total")).To(BeNumerically(">", 0),
			"the degrade stage never synthesized a backpressure event")
		Expect(rec.Total("gotthlive_patches_coalesced_total")).To(BeNumerically(">", 0),
			"the coalesce stage never engaged")

		// D-26, asserted as the measured behaviour so that it goes red the day
		// the eviction becomes reachable here.
		Expect(slow.isClosed()).To(BeFalse(),
			"the slow session WAS evicted after %s with grace at 3 s. If window.noteFullness no longer "+
				"resets on every acknowledgement, D-26 is fixed and this spec should assert the "+
				"eviction rather than its absence", observation)

		// Other sessions unaffected: the fast client is still live and its
		// sequence is still advancing.
		Expect(fast.isClosed()).To(BeFalse(), "the fast session died with the slow one")
		before := fast.appliedSeq()
		Eventually(fast.appliedSeq, 20*time.Second).Should(BeNumerically(">", before),
			"the fast session stopped being patched while the slow one was stalling")

		// Never the process: a fresh connection is still served.
		after := dialWire(s.addr(), wireOpts{acks: ackAuto})
		Expect(after.snapshot.GetServerSeq()).To(Equal(uint64(1)))

		delivered := r.deliveredToClient()
		AddReportEntry("case 4 — throttled and acknowledging", fmt.Sprintf(
			"%d B/s throttle, %s observed with grace 3 s: max window depth %.0f of 16, %.0f slow-client events, "+
				"%.0f coalesced patches, %d B delivered downstream (%.0f B/s), NOT evicted (D-26), "+
				"other session still advancing, process still accepting",
			throttleBytesPerSecond, observation, maxDepth,
			rec.Total("gotthlive_slow_client_events_total"),
			rec.Total("gotthlive_patches_coalesced_total"),
			delivered, float64(delivered)/observation.Seconds()))
	})

	// The memory half, in its own spec and with the metric recorder OFF.
	//
	// obstest.Metrics retains every observation it is given, the window depth is
	// recorded once per emission, and a stalled session emits thousands of them
	// — so a heap measurement taken with the recorder attached measures the
	// recorder. Measured with it attached: 7.9 MB retained, of which the
	// library's share is unknown. Measured without: the number below.
	It("keeps the server heap bounded under a stalled client, because backpressure is a dirty bitset and not a queue", func() {
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Metrics = nil
			cfg.Limits.AckWindow = 16
			cfg.Limits.SlowClientGrace = 5 * time.Minute
			cfg.Limits.WriteDeadline = 30 * time.Second
			cfg.Limits.HeartbeatInterval = time.Second
			cfg.Limits.HeartbeatTimeout = 5 * time.Minute
			cfg.Limits.IdleTimeout = 5 * time.Minute
		})

		r := newRelay(s.addr())
		r.throttleTo(throttleBytesPerSecond)
		w := dialWire(r.addr(), wireOpts{acks: ackAuto, noCapture: true})

		baseline := liveHeap()
		w.startTicks(2*time.Millisecond, 100000, false)
		time.Sleep(15 * time.Second)

		// RFC §7.3: when the window is full the actor keeps reducing, marks
		// fragments dirty and skips render+emit, so memory under backpressure is
		// O(number of fragments) and not O(pending patches). Three fragments and
		// roughly 7,500 stalled updates is the case that would show the
		// difference: a per-update queue would be megabytes.
		const heapBudget = 4 << 20
		var retained int64
		Eventually(func() int64 {
			retained = liveHeap() - baseline
			return retained
		}, 60*time.Second, 2*time.Second).Should(BeNumerically("<=", int64(heapBudget)),
			"a stalled session retained %d bytes of live heap", retained)

		Expect(w.isClosed()).To(BeFalse(), "the session ended before the measurement finished")
		AddReportEntry("case 4 — heap under backpressure", fmt.Sprintf(
			"%d B/s throttle, 15 s stalled, metrics recorder OFF: live heap retained %d B of a %d B budget",
			throttleBytesPerSecond, retained, heapBudget))
	})

	It("evicts with 4009 within the grace period plus one heartbeat interval once the client stops acknowledging", func() {
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			cfg.Limits.AckWindow = 16
			cfg.Limits.SlowClientGrace = 3 * time.Second
			cfg.Limits.WriteDeadline = 10 * time.Second
			cfg.Limits.HeartbeatInterval = time.Second
			// Far above the eviction bound, so that the close this spec observes
			// can only be the slow-client one.
			cfg.Limits.HeartbeatTimeout = 120 * time.Second
		})

		r := newRelay(s.addr())
		r.throttleTo(throttleBytesPerSecond)

		w := dialWire(r.addr(), wireOpts{acks: ackNever})
		w.startTicks(5*time.Millisecond, 4000, false)

		start := time.Now()
		// grace (3 s) + one tick interval (1 s), because onTick is where the
		// grace is checked, plus a scheduling margin.
		const bound = 3*time.Second + time.Second
		Eventually(w.isClosed, bound+6*time.Second).Should(BeTrue(),
			"a client that stopped acknowledging entirely was never evicted")
		evictedAfter := time.Since(start)

		Expect(w.code()).To(Equal(websocket.StatusCode(4009)),
			"the eviction close code is SLOW_CLIENT")
		Eventually(s.ledger.liveSessions, 20*time.Second).Should(BeZero(),
			"the evicted session's actor did not finish")

		AddReportEntry("case 4 — eviction", fmt.Sprintf(
			"%d B/s throttle and no acknowledgements: closed with %d after %s, against a bound of %s (grace 3 s + one 1 s tick)",
			throttleBytesPerSecond, w.code(), evictedAfter.Round(time.Millisecond), bound))
	})

	It("coalesces without losing provenance, and flushes at the configured trigger rather than truncating", func() {
		const flushAt = 64
		rec := obstest.NewMetrics()
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Metrics = rec
			cfg.Logger = nil
			cfg.Limits.AckWindow = 8
			cfg.Limits.CoalesceFlushAt = flushAt
			// Long, so the ladder's third stage cannot end the measurement.
			cfg.Limits.SlowClientGrace = 5 * time.Minute
			cfg.Limits.HeartbeatInterval = time.Second
			cfg.Limits.HeartbeatTimeout = 5 * time.Minute
			cfg.Limits.IdleTimeout = 5 * time.Minute
		})

		// A client that never acknowledges reaches the coalesce and degrade
		// stages deterministically and stays there, which is the condition the
		// flush trigger exists for.
		w := dialWire(s.addr(), wireOpts{acks: ackNever})
		w.startTicks(2*time.Millisecond, 400, true)

		// The flush: a patch whose contributing union reached the trigger, sent
		// even though the window is full.
		var flushed *pb.Patch
		Eventually(func() *pb.Patch {
			for _, p := range w.patches() {
				if len(p.GetOrigin().GetContributingEventIds()) >= flushAt {
					return p
				}
			}
			return nil
		}, 30*time.Second, 100*time.Millisecond).ShouldNot(BeNil(),
			"no patch ever carried a union reaching CoalesceFlushAt=%d: the trigger did not fire", flushAt)
		for _, p := range w.patches() {
			if len(p.GetOrigin().GetContributingEventIds()) >= flushAt {
				flushed = p
				break
			}
		}

		// The trigger is a flush, never a truncation: the frame carries at most
		// the trigger plus the deferred transition's own identifier plus one
		// event's Contributing (session.MaxCoalesceFlushAt's arithmetic), and
		// always under H-4's ceiling.
		Expect(len(flushed.GetOrigin().GetContributingEventIds())).To(
			BeNumerically("<=", flushAt+1+64),
			"the flushed frame carried more than the trigger's arithmetic allows")
		Expect(len(flushed.GetOrigin().GetContributingEventIds())).To(BeNumerically("<=", 1024),
			"H-4 bounds contributing_event_ids at 1024")

		// Provenance across coalescing: no zero identifier, no repeat.
		var maxUnion int
		for _, p := range w.patches() {
			ids := p.GetOrigin().GetContributingEventIds()
			if len(ids) > maxUnion {
				maxUnion = len(ids)
			}
			seen := map[uint64]bool{}
			for _, id := range ids {
				Expect(id).NotTo(BeZero(), "a contributing list carried the zero identifier")
				Expect(seen[id]).To(BeFalse(), "a contributing list repeated identifier %d", id)
				seen[id] = true
			}
		}

		AddReportEntry("case 4 — coalescing", fmt.Sprintf(
			"CoalesceFlushAt=%d, AckWindow=8, client never acknowledges: flush fired with a union of %d, "+
				"largest union over the run %d, %.0f coalesced, %.0f slow-client events",
			flushAt, len(flushed.GetOrigin().GetContributingEventIds()), maxUnion,
			rec.Total("gotthlive_patches_coalesced_total"),
			rec.Total("gotthlive_slow_client_events_total")))
	})
})

// startTicks asks the application to begin a server-initiated update stream on
// this session.
//
// It goes through an ordinary registered event because that is how an
// application starts one: the reducer returns an effect, the actor runs it at
// the boundary, and the effect emits back into the mailbox. Nothing here
// reaches around the protocol.
func (w *wire) startTicks(every time.Duration, count int, contributing bool) {
	GinkgoHelper()
	w.mu.Lock()
	w.ref++
	ref := w.ref
	seen := w.seq
	w.mu.Unlock()

	contrib := "false"
	if contributing {
		contrib = "true"
	}
	Expect(w.send(w.envelope(&pb.Event{
		ClientRef:     ref,
		Name:          "chaos.ticks",
		FragmentId:    "ticks",
		SeenServerSeq: seen,
		Fields: []*pb.EventField{
			{Key: "contributing", Value: contrib},
			{Key: "count", Value: fmt.Sprint(count)},
			{Key: "every", Value: every.String()},
		},
	}))).To(Succeed())
}
