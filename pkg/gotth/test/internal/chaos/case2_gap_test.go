package chaos_test

import (
	"fmt"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// PRD Phase 3, case 2:
//
//	Sequence gap injected → client requests resync rather than applying out of
//	order (FR-11).
//
// The gap is injected by LOSS rather than by a forged frame, and that choice is
// the point. H-9 makes the server incapable of emitting a gap — one framer, one
// writer, +1 per sequenced frame — so a gap that the server produced would be a
// different defect entirely. What a real client meets is a patch that never
// arrived, and the frame after it carrying a sequence one higher than expected.
//
// The client half of FR-11 — stop applying, ask once per gap, re-arm on the
// answering Snapshot — is DEV-2's and is asserted in
// client/test/reconnect.test.mjs. What is asserted here is the half that
// document cannot reach: that a live actor answers the request with a Snapshot
// that supersedes exactly the range that was missed, and that the markup after
// it is server truth rather than the truth the missing patch would have
// implied.
var _ = Describe("A sequence gap on the wire (PRD case 2, FR-11)", func() {

	It("is answered with a resync Snapshot superseding exactly the missed range", func() {
		s := serve(nil)

		// The client drops the third patch it sees, and asks for a resync on the
		// gap the fourth exposes.
		w := dialWire(s.addr(), wireOpts{acks: ackAuto, dropPatchAt: 3, resyncOnGap: true})

		// Serialised so each interaction produces its own patch: a burst would
		// coalesce and there would be no third patch to drop. The wait is on
		// the CAPTURE rather than on the frame channel, because a channel read
		// consumes whatever it passes over — and what it would pass over here
		// is the resync Snapshot this spec is about.
		// Four interactions, and no more: patch 3 is dropped and patch 4 is what
		// exposes the gap, so the resync Snapshot below is rendered from a state
		// no later interaction has moved. A fifth commit would leave the
		// Snapshot a historical render and the convergence comparison a race.
		for i := 0; i < 4; i++ {
			before := len(w.patches())
			w.commit(0)
			Eventually(func() int { return len(w.patches()) }, 10*time.Second).
				Should(BeNumerically(">", before))
		}

		var snap *pb.Snapshot
		Eventually(func() *pb.Snapshot {
			snap = resyncSnapshot(w)
			return snap
		}, 15*time.Second).ShouldNot(BeNil(), "the gap produced no resync Snapshot")

		Expect(w.gapCount()).To(Equal(1), "exactly one gap should have been exposed")
		Expect(w.resyncCount()).To(Equal(1), "one gap, one request")

		// H-13: both supersession bounds set, from <= through < server_seq, and
		// the origin kind is RESYNC (H-6's second event-bearing arm).
		Expect(snap.GetSupersededFromSeq()).To(BeNumerically(">", 0))
		Expect(snap.GetSupersededThroughSeq()).To(BeNumerically(">=", snap.GetSupersededFromSeq()))
		Expect(snap.GetSupersededThroughSeq()).To(BeNumerically("<", snap.GetServerSeq()))
		Expect(snap.GetOrigin().GetKind()).To(Equal(pb.OriginKind_RESYNC))
		Expect(snap.GetOrigin().GetEventId()).NotTo(BeZero(), "H-6: a RESYNC origin is event-bearing")
		Expect(snap.GetOrigin().GetClientRef()).NotTo(BeZero())

		// The dropped patch's sequence is inside the superseded range: the
		// snapshot accounts for exactly what the client missed (P7 case b).
		Expect(snap.GetSupersededFromSeq()).To(BeNumerically("<=", snap.GetSupersededThroughSeq()))

		// A resync Snapshot re-renders EVERY registered fragment, not only the
		// one the missed patch carried. That is what makes convergence
		// independent of which patch was lost.
		ids := map[string]bool{}
		for _, u := range snap.GetUpdates() {
			ids[u.GetFragmentId()] = true
		}
		Expect(ids).To(HaveKey("total"))
		Expect(ids).To(HaveKey("ticks"))
		Expect(ids).To(HaveKey("note"))

		// Convergence to server truth, computed outside the protocol.
		truth := settle(s.ledger)
		Expect(snapshotHTML(snap, "total")).To(Equal(renderTotal(truth)),
			"the resync Snapshot's markup is not the application's own truth")

		AddReportEntry("case 2", fmt.Sprintf(
			"gap at patch 3; superseded [%d,%d] of server_seq %d; %d fragments re-rendered; truth=%d",
			snap.GetSupersededFromSeq(), snap.GetSupersededThroughSeq(),
			snap.GetServerSeq(), len(snap.GetUpdates()), truth))
	})

	It("answers a resync that describes no gap with an Ack rather than a full re-render", func() {
		s := serve(nil)
		w := dialWire(s.addr(), wireOpts{acks: ackAuto})

		w.commit(0)
		Eventually(func() int { return len(w.patches()) }, 10*time.Second).Should(BeNumerically(">", 0))

		before := len(w.snapshots())
		// last_applied_seq at the current high-water mark: there is no gap, so
		// H-14's short circuit applies and the expensive path must not run.
		Expect(w.send(w.envelope(&pb.ResyncRequest{
			LastAppliedSeq: w.appliedSeq(),
			Reason:         pb.ResyncReason_CLIENT_REQUEST,
		}))).To(Succeed())

		Eventually(func() int { return len(acks(w)) }, 5*time.Second).Should(BeNumerically(">", 0),
			"a no-op resync was not answered with an Ack")
		Expect(acks(w)[len(acks(w))-1].GetServerSeq()).To(Equal(w.appliedSeq()))
		Expect(w.snapshots()).To(HaveLen(before), "a no-op resync produced a full Snapshot")
	})

	It("does not apply a patch from beyond the gap before the Snapshot arrives", func() {
		s := serve(nil)
		w := dialWire(s.addr(), wireOpts{acks: ackAuto, dropPatchAt: 2, resyncOnGap: true})

		for i := 0; i < 3; i++ {
			before := len(w.patches())
			w.commit(0)
			Eventually(func() int { return len(w.patches()) }, 10*time.Second).
				Should(BeNumerically(">", before))
		}

		var snap *pb.Snapshot
		Eventually(func() *pb.Snapshot {
			snap = resyncSnapshot(w)
			return snap
		}, 15*time.Second).ShouldNot(BeNil())

		// Every patch inside the superseded range was received-and-not-applied
		// or never received. The applied high-water mark before the Snapshot is
		// exactly one below the range's start, which is FR-11's "MUST NOT apply
		// out of order" expressed in the only number that can carry it.
		Expect(snap.GetSupersededFromSeq()).To(BeNumerically(">", 0))
		appliedBeforeResync := snap.GetSupersededFromSeq() - 1
		for _, p := range w.patches() {
			if p.GetServerSeq() > appliedBeforeResync && p.GetServerSeq() <= snap.GetSupersededThroughSeq() {
				// It arrived. FR-11 requires it not to have been applied, and
				// the client's applied sequence is the record of that.
				Expect(appliedBeforeResync).To(BeNumerically("<", p.GetServerSeq()))
			}
		}
		Expect(w.appliedSeq()).To(BeNumerically(">=", snap.GetServerSeq()),
			"the Snapshot did not re-establish the sequence")
	})
})

// D-29, THE PRE-FIX COST. Read the note at the top before this spec's failure
// messages: c3a91af8 closed D-29 in client/runtime.js, and this spec's client
// is the runtime as it was BEFORE that, kept deliberately.
//
// It is kept because the fix's whole value is the difference between two
// numbers, and one of them has to still be measurable. What this spec holds is
// what a client without the re-arm costs — the freeze, and the eviction that
// ends it — and it is the control for the "D-29 re-verified" spec in
// reverify_test.go, which puts the FIXED client's behaviour in front of the
// same server and measures 2.5 s of stall and no eviction where this one
// measures 6 s and a 4009.
//
// So its failure messages are worded for the wrong reader if taken at face
// value. They say "D-29 is fixed" because they were written when it was not;
// what they mean now is "this harness client no longer models the pre-fix
// runtime, so the control has stopped being a control". The library assertions
// in here — that the refusal is answered with RATE_LIMITED and no render, and
// that a silent client is evicted with 4009 — are live claims about this
// module either way.
//
// The defect, as reported: a legitimate client whose resync request was refused
// by the resync budget never asked again, and nothing in either half of the
// protocol re-armed it. The page was frozen until the SLOW-CLIENT EVICTION
// happened to close the connection, which is a recovery nobody designed for
// this.
//
// The mechanism is one line of state on each side. The shipped runtime latches
// a requested resync — client/runtime.js resync(): `if (gap || !seq) return` —
// and clears the latch only in applied(), which runs on a Patch or a Snapshot.
// The server answers a rate-limited resync with `Error{RATE_LIMITED}` and NO
// RENDER (RFC §7.6), which is neither; runtime.js's error branch dispatches a
// DOM CustomEvent and touches no state. So the latch stays set, every
// subsequent patch fails the `server_seq === seq + 1` test and is discarded by
// the same early return, and the client stops acknowledging as a consequence of
// having stopped applying.
//
// That last consequence is what ends it, and it ends it by accident: with the
// client no longer acknowledging, the outbound window fills, stays full, and
// RFC §7.4's eviction closes the connection with 4009 SLOW_CLIENT after
// slow_client_grace. The client then reconnects, mounts, and recovers. So the
// user-visible cost of one refused resync is
//
//	(time for the outbound window to fill) + slow_client_grace
//
// of a frozen page followed by a full re-mount — thirty seconds at the
// defaults, for a refusal RFC §7.6 describes as costing "no render".
//
// The wire client here implements exactly runtime.js's PRE-FIX latch, and it is
// the harness's most load-bearing line: without it the client would send a
// request per frame and this would be a measurement of an abusive client rather
// than a legitimate one. QA3-2 measures how often the refusal reaches a
// legitimate client on a lossy link at the dashboard workload — 20–25 % of
// requests at 5–25 % patch loss — and that number is a property of the SERVER's
// budget, so it is unchanged by the client-side fix and was re-measured
// identical at ce52d2f9.
var _ = Describe("A legitimate client refused by the resync budget, without the re-arm (D-29, the pre-fix cost)", func() {

	It("stops asking, stops applying, and is recovered only by the slow-client eviction", func() {
		// Five seconds rather than the default thirty, so the spec is seconds
		// rather than half a minute. The arithmetic is what is under test.
		const grace = 5 * time.Second
		s := serve(func(cfg *live.Config[board, chaosUser]) {
			cfg.Logger = nil
			// A budget of one, refilling once a minute, so the second gap inside
			// the spec is certainly refused. The DEFAULTS produce the same
			// outcome on a lossy link — QA3-2's 20–25 % — this only makes the
			// arrival deterministic.
			cfg.Limits.ResyncBurst = 1
			cfg.Limits.MinResyncInterval = time.Minute
			cfg.Limits.AckWindow = 16
			cfg.Limits.SlowClientGrace = grace
			cfg.Limits.HeartbeatInterval = time.Second
			cfg.Limits.HeartbeatTimeout = 5 * time.Minute
			cfg.Limits.IdleTimeout = 5 * time.Minute
		})

		// Half the patches are lost, so gaps arrive quickly and repeatedly.
		w := dialWire(s.addr(), wireOpts{acks: ackAuto, resyncOnGap: true, lossPercent: 50})
		w.startTicks(20*time.Millisecond, 5000, false)

		// The first gap is answered; the second is refused.
		Eventually(func() int {
			var limited int
			for _, e := range w.errors() {
				if e.GetCode() == pb.ErrorCode_RATE_LIMITED {
					limited++
				}
			}
			return limited
		}, 30*time.Second).Should(BeNumerically(">", 0),
			"the resync budget never refused anything, so there is no refusal to be stuck on")

		refusedAt := time.Now()
		requestsAtRefusal := w.resyncCount()
		appliedAtRefusal := w.appliedSeq()

		// Nothing re-arms the client. It never asks again and never applies
		// again, right up to the moment the server gives up on it.
		Consistently(w.resyncCount, 3*time.Second, 200*time.Millisecond).
			Should(Equal(requestsAtRefusal),
				"the client asked again after being refused, so this harness client is no longer the "+
					"pre-fix runtime and has stopped being the control reverify_test.go measures against")
		Consistently(w.appliedSeq, 3*time.Second, 200*time.Millisecond).
			Should(Equal(appliedAtRefusal),
				"the client resumed applying after being refused, so this harness client is no longer "+
					"the pre-fix runtime and has stopped being the control")

		// And the recovery, such as it is: the eviction the client's silence
		// provoked.
		Eventually(w.isClosed, grace+15*time.Second).Should(BeTrue(),
			"the frozen session was never closed either, so the page is stale indefinitely and D-29 "+
				"is worse than reported")
		frozenFor := time.Since(refusedAt)
		Expect(w.code()).To(Equal(websocket.StatusCode(4009)),
			"the frozen session closed with %d rather than SLOW_CLIENT, so the recovery path is not "+
				"the one D-29 describes", w.code())

		AddReportEntry("D-29", fmt.Sprintf(
			"one refused resync with grace at %s: applied sequence frozen at %d, resync requests frozen "+
				"at %d, page stale for %s, then closed 4009 and the client reconnects. At the default "+
				"30 s grace the freeze is about thirty seconds. This is the PRE-FIX cost; the same "+
				"server with c3a91af8's re-arm in front of it is reverify_test.go's D-29 spec.",
			grace, appliedAtRefusal, requestsAtRefusal, frozenFor.Round(time.Millisecond)))
	})
})

// resyncSnapshot returns the first captured Snapshot carrying a supersession
// range, or nil. It reads the capture rather than the frame channel because a
// channel read consumes every frame it passes over, and a spec that waited for
// a patch would eat the Snapshot it was about to assert on.
func resyncSnapshot(w *wire) *pb.Snapshot {
	for _, s := range w.snapshots() {
		if s.GetSupersededFromSeq() != 0 {
			return s
		}
	}
	return nil
}

// acks returns every Ack the server sent.
func acks(w *wire) []*pb.Ack {
	var out []*pb.Ack
	for _, f := range w.captured() {
		if a := f.GetAck(); a != nil {
			out = append(out, a)
		}
	}
	return out
}
