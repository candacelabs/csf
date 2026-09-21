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

// PRD Phase 3, case 8. The bullet read
//
//	Duplicate/replayed event frames → defined semantics, no double state
//	transition.
//
// and its two clauses did not agree with each other in this library. The first
// spec below was written as a CHARACTERISATION for that reason: it asserted the
// documented behaviour — RFC §8.5's at-most-once plus protocol.md Q-P1, under
// which a byte-identical Event frame sent twice is two events — and it recorded
// the second clause as unsatisfiable rather than making the disagreement go
// away. QA-2's checkpoint-3 report §4.8 escalated it to PM-1 instead of filing
// it as a defect, because both halves of the library were doing what their own
// documents said.
//
// RULED, 2026-08-04 (PRD §9 v0.6 row 1, docs/pm/checkpoint-3-scope.md §1,
// protocol.md Q-P1's closing note): the second clause is STRUCK and Q-P1 stays
// closed. The bullet now reads the other way round —
//
//	A byte-identical Event frame delivered twice MUST produce two transitions
//	and run the effect twice, the library MUST NOT deduplicate.
//
// so the spec below is no longer a characterisation of behaviour nobody asked
// for. It is the spec that holds the requirement, and its assertions did not
// have to change by a line to become one: it already required the state version
// to advance and the effect to have run twice. What changed is which direction
// counts as the regression. Deduplication landing here is now a PRD violation
// and not merely an undocumented change, and PM-1 named the trigger that would
// re-open the question: an outbound retry queue in client/runtime.js, which
// would make a duplicate frame something the library itself could emit.
//
// The specs after it are the replay properties that ARE defences, and each of
// them is a real one: a frame replayed onto another session, an acknowledgement
// replayed forwards and backwards, a telemetry report for a patch that is no
// longer in the window, and a resync replayed into its own rate budget. The
// ruling left all of them gated and unchanged.
var _ = Describe("Duplicate and replayed frames (PRD case 8)", func() {

	It("treats a byte-identical replayed Event as a second event, which is what the PRD now requires", func() {
		s := serve(nil)
		w := dialWire(s.addr(), wireOpts{acks: ackAuto})

		// One frame, captured as bytes, sent twice. Nothing is regenerated
		// between the sends: this is a replay, not a resend.
		frame := w.commitBytes(4242)

		Expect(w.sendBytes(frame)).To(Succeed())
		first, err := w.await(10*time.Second, func(f *pb.Frame) bool { return f.GetPatch() != nil })
		Expect(err).NotTo(HaveOccurred())

		Expect(w.sendBytes(frame)).To(Succeed())
		second, err := w.await(10*time.Second, func(f *pb.Frame) bool {
			return f.GetPatch() != nil && f.GetPatch().GetServerSeq() > first.GetPatch().GetServerSeq()
		})
		Expect(err).NotTo(HaveOccurred(),
			"the replay produced no second patch: if this is now deduplicated, protocol.md Q-P1 and RFC §8.5 need to move with it")

		// The measurement the PRD bullet is about: the state version moved
		// twice, so the transition happened twice.
		Expect(second.GetPatch().GetStateVersion()).To(
			BeNumerically(">", first.GetPatch().GetStateVersion()),
			"the replayed frame did not advance state: deduplication has landed, which PRD Phase 3 "+
				"case 8 now forbids in as many words (v0.6) and which protocol.md Q-P1 would have to "+
				"re-open to allow")

		// And the application effect ran twice, under one ledger key, because
		// the ref came from the replayed bytes.
		settle(s.ledger)
		Expect(s.ledger.duplicates()).To(HaveKey(uint64(4242)),
			"the replayed event's effect did not run a second time")

		AddReportEntry("case 8 — replayed Event", fmt.Sprintf(
			"one frame sent twice: state_version %d -> %d, effect ran %d times for ref 4242. "+
				"That is the PRD requirement as of v0.6 (two frames are two events, no "+
				"deduplication) and RFC §8.5 plus protocol.md Q-P1 unchanged behind it.",
			first.GetPatch().GetStateVersion(), second.GetPatch().GetStateVersion(),
			s.ledger.duplicates()[4242]))
	})

	It("refuses a frame replayed onto a different session and closes that connection (H-3)", func() {
		s := serve(nil)

		victim := dialWire(s.addr(), wireOpts{acks: ackAuto})
		captured := victim.commitBytes(7)

		// A second connection, a different session identifier. The captured
		// bytes name the first session, so the second connection must refuse
		// them rather than apply them.
		attacker := dialWire(s.addr(), wireOpts{acks: ackAuto})
		Expect(attacker.sendBytes(captured)).To(Succeed())

		Eventually(attacker.isClosed, 10*time.Second).Should(BeTrue(),
			"a frame naming another session did not end the connection")
		Expect(attacker.code()).To(Equal(websocket.StatusCode(4002)),
			"H-3's close code is PROTOCOL_VIOLATION")

		// The victim's session is untouched by the attempt.
		Expect(victim.isClosed()).To(BeFalse())
		Expect(s.ledger.committed(7)).To(BeFalse(),
			"the replayed frame reached a reducer on the wrong session")
	})

	It("tolerates a replayed acknowledgement and closes on one that goes backwards (H-7)", func() {
		s := serve(nil)
		w := dialWire(s.addr(), wireOpts{acks: ackNever})

		w.commit(0)
		f, err := w.await(10*time.Second, func(f *pb.Frame) bool { return f.GetPatch() != nil })
		Expect(err).NotTo(HaveOccurred())
		seq := f.GetPatch().GetServerSeq()

		// The same acknowledgement three times. It is a cumulative high-water
		// mark, so a repeat is a no-op rather than a violation.
		for i := 0; i < 3; i++ {
			Expect(w.send(w.envelope(&pb.Ack{ServerSeq: seq}))).To(Succeed())
		}
		Consistently(w.isClosed, 1*time.Second, 100*time.Millisecond).Should(BeFalse(),
			"a repeated acknowledgement closed the session")

		// Backwards is different: H-7 says an acknowledgement never decreases.
		Expect(w.send(w.envelope(&pb.Ack{ServerSeq: seq - 1}))).To(Succeed())
		Eventually(w.isClosed, 10*time.Second).Should(BeTrue())
		Expect(w.code()).To(Equal(websocket.StatusCode(4002)))
	})

	// H-11, and what BR-1 did to it. D-32.
	//
	// Until 37df5537 an acknowledgement evicted the slot the telemetry report
	// was about to name, and the shipped client sends the ack and then the
	// report for the SAME patch (client/runtime.js applied()). The two arrive
	// on different channels with no ordering between them, so slotFor missed
	// and every legitimate report was counted as a forgery — 40 of 40 in the
	// review's own repro. BR-1 made eviction by AGE instead: the ring keeps the
	// newest window.retentionSlots() = AckWindow + 1 patches, acknowledged or
	// not.
	//
	// That reversed the first half of what this spec used to assert, and the
	// spec could not tell. It read "drops a replayed telemetry report for a
	// patch that has left the window", its comment asserted in prose that
	// "acknowledged patches leave the window", and it sent exactly one report
	// about one acknowledged patch — which at HEAD is precisely the report BR-1
	// exists to make land. It stayed green because the only thing it asserted
	// was that the connection survived, and the connection survives whichever
	// way the report is treated. Same defect class as the Fixed1 table that
	// asserted the bug and the four before it.
	//
	// Three arms now, each reading an instrument rather than the connection's
	// liveness:
	//
	//  1. a report about a patch the client has just acknowledged is USED —
	//     BR-1's whole point, asserted here on the wire rather than in
	//     internal/session's in-process harness;
	//  2. a report naming a patch that never existed is dropped and counted —
	//     H-11's defence, which BR-1 was not allowed to spend;
	//  3. a report about a patch older than the retention bound is dropped and
	//     counted — the "left the window" case this spec's title always
	//     claimed, now that leaving the window means age and not
	//     acknowledgement. It is also what says the ring is bounded at all: an
	//     unbounded one is per-session memory a client controls.
	It("uses a telemetry report about a patch the client just acknowledged, and drops a forged one and one that has aged out of the ring (H-11, BR-1)", func() {
		rec := obstest.NewMetrics()
		s := serve(func(cfg *live.Config[board, chaosUser]) { cfg.Metrics = rec })
		w := dialWire(s.addr(), wireOpts{acks: ackAuto})

		dropped := func() float64 { return rec.Total("gotthlive_client_telemetry_dropped_total") }
		used := func() int { return len(rec.Observations("gotthlive_client_morph_duration_seconds")) }

		// AckWindow is session.DefaultLimits()'s 16, so the ring remembers
		// AckWindow + 1 = 17 emitted frames. The mount Snapshot already holds
		// one of them, which is why arm 3 drives more than the bound rather
		// than exactly it.
		const ackWindow = 16
		const retention = ackWindow + 1

		patchAt := func(n int) uint64 {
			GinkgoHelper()
			Eventually(func() int { return len(w.patches()) }, 20*time.Second).
				Should(BeNumerically(">=", n), "the server emitted fewer than %d patches", n)
			return w.patches()[n-1].GetPatchId()
		}

		w.commit(0)
		first := patchAt(1)

		// 1. Acknowledged by ackAuto before this report goes out, and well
		//    inside the retention bound.
		Expect(w.send(w.envelope(&pb.ClientTelemetry{
			PatchId: first, MorphMicros: 1, ApplyMicros: 1,
		}))).To(Succeed())
		Eventually(used, 10*time.Second).Should(Equal(1),
			"a report about a patch the client had just acknowledged was not used. That is BR-1: "+
				"the acknowledgement evicted the slot the report names, so gotthlive.client.morph "+
				"was never emitted for any patch and the client half of the latency budget had no "+
				"data at all")
		Expect(dropped()).To(BeZero(),
			"the same report was ALSO counted as a forgery, which is the accusation BR-1 removed")

		// 2. A patch identifier that names nothing this session ever sent. H-11
		//    is the defence and BR-1's wider retention was not allowed to spend
		//    it.
		Expect(w.send(w.envelope(&pb.ClientTelemetry{
			PatchId: 1 << 40, MorphMicros: 1, ApplyMicros: 1,
		}))).To(Succeed())
		Eventually(dropped, 10*time.Second).Should(BeNumerically("==", 1),
			"a forged patch identifier was resolved to a real slot rather than dropped and counted")
		Expect(used()).To(Equal(1), "a forged report fabricated a client-timing observation")

		// 3. Aged out. retention + 2 further patches on top of the mount
		//    Snapshot and `first` push it out with room to spare, rather than
		//    landing the assertion exactly on the boundary where an off-by-one
		//    in either direction would decide it.
		for i := 0; i < retention+2; i++ {
			w.commit(0)
			patchAt(i + 2)
		}
		Expect(w.send(w.envelope(&pb.ClientTelemetry{
			PatchId: first, MorphMicros: 1, ApplyMicros: 1,
		}))).To(Succeed())
		Eventually(dropped, 10*time.Second).Should(BeNumerically("==", 2),
			"a report about a patch pushed out of the retention bound was still resolved. The ring "+
				"remembers AckWindow + 1 = %d frames and no more; a ring that grows instead is "+
				"per-session memory the client decides the size of", retention)
		Expect(used()).To(Equal(1))

		// Untrusted input throughout, and none of it is a protocol violation.
		Expect(w.isClosed()).To(BeFalse(),
			"an unresolvable telemetry report ended the connection")

		AddReportEntry("case 8 — replayed ClientTelemetry (H-11, BR-1)", fmt.Sprintf(
			"acknowledged and inside the ring: USED (%d client-morph observation); forged: dropped; "+
				"older than retentionSlots = AckWindow + 1 = %d after %d further patches: dropped. "+
				"gotthlive_client_telemetry_dropped_total = %.0f",
			used(), retention, retention+2, dropped()))
	})

	It("charges a replayed ResyncRequest to the resync budget rather than re-rendering (H-14)", func() {
		s := serve(nil)
		w := dialWire(s.addr(), wireOpts{acks: ackAuto})

		// Three commits, so the replayed cursor of 1 is behind what the server
		// has emitted.
		//
		// This spec's premise used to be "the resync path is the expensive one
		// rather than the no-op short circuit", and BR-9 falsified it. The
		// clamp floors last_applied_seq at max(win.ackedSeq(), lastSnapshotSeq)
		// before anything is derived from it, and ackAuto has acknowledged
		// every one of those patches — so a replay claiming to have applied 1
		// is contradicting an acknowledgement the same client already sent, the
		// clamp lifts it to the acknowledged high-water mark, and the request
		// describes no gap. It is answered with an Ack.
		//
		// The two consequences are the reason the assertions below are what
		// they are, and both are changes rather than restatements:
		//
		//   * the amplification bound this spec asserts is now STRUCTURAL, not
		//     merely budgeted. One fixed cursor replayed N times can produce at
		//     most one Snapshot ever, because the first answer moves
		//     lastSnapshotSeq past the cursor. Measured at HEAD: zero, because
		//     the acked floor gets there first.
		//   * the close is reachable at all only because BR-6 moved
		//     resyncBucket.allow ABOVE the no-op short circuit. Before it, a
		//     request that describes no gap was charged to no bucket at all —
		//     not this one and not the event bucket — so a client replaying
		//     into the short circuit could do it for ever. Every refusal
		//     counted below is therefore a refusal of a request that would have
		//     rendered nothing, which is exactly the frame kind H-14's first
		//     clause is about and exactly what the old ordering did not charge.
		for i := 0; i < 3; i++ {
			w.commit(0)
			_, err := w.await(10*time.Second, func(f *pb.Frame) bool { return f.GetPatch() != nil })
			Expect(err).NotTo(HaveOccurred())
		}
		Eventually(w.appliedSeq, 10*time.Second).Should(BeNumerically(">", 1),
			"the client has not applied past the sequence it is about to replay, so the clamp "+
				"this spec's premise now depends on would not engage")

		req := w.envelope(&pb.ResyncRequest{LastAppliedSeq: 1, Reason: pb.ResyncReason_GAP})
		snapshotsBefore := len(w.snapshots())
		acksBefore := len(acks(w))

		// Twenty replays of one request inside one second. The budget is
		// 1/s with a burst of 3, so at most 4 can be answered with a Snapshot,
		// and the ninth CONSECUTIVE denial (3 x ResyncBurst) closes the
		// connection with 4008 — which lands somewhere around the twelfth send.
		//
		// The close is the thing under test, so it is also the thing that ends
		// this loop. Requiring all twenty sends to SUCCEED made the spec race
		// its own assertion: once the server has closed, a further write fails
		// with "use of closed network connection", and whether the client got
		// all twenty out before the server acted on the twelfth is a question
		// about the scheduler. It passed twelve times out of twelve in
		// isolation and failed inside the full 42-spec -race run on a contended
		// host, which is §6.1's defect class one spec over: a check whose result
		// is the hardware's.
		//
		// So the loop stops at the close, and the number of requests that
		// actually landed becomes the PREMISE the conclusions are asserted
		// against.
		sent := 0
		for i := 0; i < 20; i++ {
			if err := w.send(req); err != nil {
				break
			}
			sent++
		}

		// Enough requests left the client for the documented close to be
		// reachable at all: three allowed by the burst, then nine consecutive
		// denials. This guards the loop above and nothing more — it says the
		// run was long enough to be about the close, not that the close was the
		// right one. The count of REFUSALS below is what says that, and the
		// distinction is worth writing down because the obvious reading of this
		// line is wrong: the client's writes are buffered, so they keep
		// succeeding for a while after the server has decided to close, and a
		// server that closed on its FIRST denial still gets all twenty sent.
		// Measured: mutating the threshold to one denial leaves this assertion
		// green and reddens the one below.
		const enoughForTheDocumentedClose = 12
		Expect(sent).To(BeNumerically(">=", enoughForTheDocumentedClose),
			"only %d of 20 replays were written before the socket went away, which is too few for "+
				"the close to be the documented one (3 allowed by the burst, then 9 consecutive "+
				"denials) — so this run cannot say whether the budget behaved", sent)

		// Sustained replay past the budget is a defined CLOSE, not an endless
		// stream of refusals: nine consecutive denials (3 × ResyncBurst) end
		// the connection with 4008.
		Eventually(w.isClosed, 10*time.Second).Should(BeTrue(),
			"%d replayed resync requests did not reach the defined close", sent)
		Expect(w.code()).To(Equal(websocket.StatusCode(4008)),
			"the close code for sustained rate-limit abuse is RATE_LIMITED")

		produced := len(w.snapshots()) - snapshotsBefore
		// One, not four. BR-9's clamp makes the bound structural: the first
		// answer moves lastSnapshotSeq past the replayed cursor, so every
		// later replay of the SAME cursor describes no gap no matter how much
		// budget it is given. Four was the budget's bound and is no longer the
		// binding one; asserting it would leave three re-renders of headroom
		// that nothing can now reach, and a bound nothing can reach is not a
		// bound.
		Expect(produced).To(BeNumerically("<=", 1),
			"%d replays of ONE cursor produced %d full re-renders. At most one is reachable: the "+
				"answering Snapshot moves lastSnapshotSeq past that cursor and BR-9's clamp turns "+
				"every later replay into a no-op. More than one means the clamp is not being "+
				"applied, and P7's non-overlap is a client's to falsify again",
			sent, produced)

		// The allowed requests WERE answered — with an Ack, which is what a
		// request describing no gap costs (H-14's second clause). This is the
		// premise under the refusal count: a budget that only guarded the
		// expensive path would answer all twenty of these for free and close
		// nothing, which is the state BR-6 found and the reason the close below
		// is reachable at all.
		Expect(len(acks(w))-acksBefore).To(BeNumerically(">=", 1),
			"not one of %d replayed resync requests was answered, so this run says nothing about "+
				"whether a request that renders nothing is still charged to the resync budget (BR-6)",
			sent)

		var rateLimited int
		for _, e := range w.errors() {
			if e.GetCode() == pb.ErrorCode_RATE_LIMITED {
				rateLimited++
			}
		}
		// The close came after the number of consecutive denials RFC §7.6
		// documents, and not before. consecutiveDenialsBeforeClose x
		// ResyncBurst is 3 x 3, and the denial that trips the close emits its
		// error before closing, so a well-behaved server owes exactly nine.
		// Written as a literal with the arithmetic in the message, as the
		// snapshot bound above is: interpolating the same constants the
		// implementation uses would agree with any value at all.
		const deniedBeforeClose = 9
		Expect(rateLimited).To(BeNumerically(">=", deniedBeforeClose),
			"the session closed 4008 after only %d typed RATE_LIMITED errors. The documented close "+
				"is consecutiveDenialsBeforeClose x ResyncBurst = 3 x 3 = %d consecutive denials, so "+
				"this session was closed on far fewer refusals than RFC §7.6 says it takes — which is "+
				"a different defect and must not pass as this one", rateLimited, deniedBeforeClose)

		AddReportEntry("case 8 — replayed ResyncRequest", fmt.Sprintf(
			"20 replays -> %d snapshots, %d Acks, %d RATE_LIMITED errors, close %d. "+
				"The snapshot count was 3 before BR-9's clamp and is structurally at most 1 after it.",
			produced, len(acks(w))-acksBefore, rateLimited, w.code()))
	})
})
