package chaos_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// PRD Phase 3, case 1 — the SERVER-AND-WIRE half.
//
//	Connection dropped mid-patch → reconnect → resync → DOM converges to
//	server truth; no duplicated or lost application effect.
//
// The client's half of this — backoff, jitter, visibility pausing, "a
// reconnect is a new session" — is DEV-2's and is covered by
// client/test/reconnect.test.mjs. What is here is the half a browser cannot
// decide: what the server does with a session whose socket died while it was
// writing, what the next connection's Snapshot contains, and what happened to
// the effects the dead session had already scheduled.
//
// "Converges to server truth" is checked against a value computed OUTSIDE the
// protocol — renderTotal(ledger.total()) — rather than against a frame the
// server sent. Comparing the snapshot to another frame would only prove the
// server is self-consistent.
var _ = Describe("A connection dropped mid-patch (PRD case 1)", func() {

	It("converges the next session's snapshot to server truth, with no duplicated effect", func() {
		s := serve(nil)
		r := newRelay(s.addr())

		first := dialWire(r.addr(), wireOpts{acks: ackAuto})

		// Enough interactions that patches are certainly in flight when the
		// socket dies, and a commit delay wide enough that some effects are
		// still running at the cut. That window is the one RFC §8.5 describes:
		// an effect may commit externally even though its patch never arrived.
		// Two waves, and the split is what keeps both halves of the case
		// non-vacuous. The first wave commits and SETTLES before the cut, so
		// "no duplicated effect" has something to be true of; the second is
		// still inside its effect's delay when the socket dies, so "an
		// in-flight effect at disconnect" is exercised rather than assumed.
		const settled, inFlight = 10, 30
		const interactions = settled + inFlight
		for i := 0; i < settled; i++ {
			first.commit(0)
		}
		Eventually(s.ledger.total, 20*time.Second).Should(Equal(settled),
			"the first wave never committed, so there is no settled truth to protect")
		committedBeforeCut := s.ledger.total()

		for i := 0; i < inFlight; i++ {
			first.commit(200 * time.Millisecond)
		}

		// Cut as soon as the server has started answering, so the drop lands in
		// the middle of the patch stream rather than after it.
		_, err := first.await(10*time.Second, func(f *pb.Frame) bool { return f.GetPatch() != nil })
		Expect(err).NotTo(HaveOccurred(), "the server never patched, so nothing was dropped mid-patch")
		r.cut()

		Eventually(first.isClosed, 10*time.Second).Should(BeTrue(),
			"the client never noticed the cut")

		// The session must be gone from the server's registry: a dropped
		// connection is a closed session (FR-22), not a retained one.
		Eventually(s.ledger.liveSessions, 20*time.Second).Should(BeZero(),
			"a session outlived the connection that owned it")

		// Let every effect the dead session scheduled finish or be abandoned.
		// Quiescence is measured rather than assumed: the ledger stops moving.
		truth := settle(s.ledger)

		// Reconnect. A new connection, so a new actor, a new Mount and a fresh
		// Snapshot — the ONE recovery path RFC §8.1 keeps.
		second := dialWire(s.addr(), wireOpts{acks: ackAuto})

		Expect(second.snapshot.GetServerSeq()).To(Equal(uint64(1)),
			"a reconnect is a new session and its sequence restarts at 1")
		Expect(second.snapshot.GetSupersededFromSeq()).To(BeZero())
		Expect(second.snapshot.GetSupersededThroughSeq()).To(BeZero())
		Expect(second.snapshot.GetOrigin().GetKind()).To(Equal(pb.OriginKind_MOUNT))

		// Convergence, against a value the protocol did not produce.
		Expect(snapshotHTML(second.snapshot, "total")).To(Equal(renderTotal(truth)),
			"the reconnected session's markup does not match the application's own truth")

		// No duplicated application effect.
		Expect(s.ledger.duplicates()).To(BeEmpty(),
			"an interaction committed more than once across the drop")

		// The "lost effect" half, stated at the boundary the library actually
		// draws rather than at the one the PRD sentence reads as.
		//
		// RFC §8.5 cancels the session context at disconnect and says effects
		// observe cancellation, so an effect that had NOT yet committed when the
		// socket died is permitted to be abandoned — this application's executor
		// abandons it, an application that used context.WithoutCancel would not,
		// and neither choice is the library's. What the library must not do is
		// UNDO one that already committed, or run one twice.
		//
		// The measured consequence is recorded in the report rather than
		// asserted away: some interactions were patched to the client and then
		// abandoned, so the browser showed a total the server never committed
		// until the resync corrected it. RFC §8.5 documents the opposite
		// direction — committed but never delivered — and not this one. That is
		// D-25.
		acked := appliedRefs(first)
		Expect(acked).NotTo(BeEmpty(), "no patch named an interaction, so nothing was checkable")

		committedBefore := s.ledger.callCount()
		var patchedButNotCommitted []uint64
		for _, ref := range acked {
			if !s.ledger.committed(ref) {
				patchedButNotCommitted = append(patchedButNotCommitted, ref)
			}
		}

		// Nothing is undone: the commits that had settled before the cut are
		// still there afterwards.
		Expect(truth).To(BeNumerically(">=", committedBeforeCut),
			"the drop un-did %d commits that had already happened", committedBeforeCut-truth)
		Expect(s.ledger.callCount()).To(Equal(committedBefore),
			"an effect ran after the ledger had settled")

		AddReportEntry("case 1", fmt.Sprintf(
			"%d interactions sent, %d patched before the cut, %d committed, %d duplicated, %d patched-but-never-committed (D-25), truth=%d and the reconnected Snapshot matched it",
			interactions, len(acked), s.ledger.callCount(), len(s.ledger.duplicates()),
			len(patchedButNotCommitted), truth))
		Expect(committedBeforeCut).To(Equal(settled))
	})

	It("keeps the sessions of other clients alive when one connection is cut", func() {
		s := serve(nil)
		r := newRelay(s.addr())

		victim := dialWire(r.addr(), wireOpts{acks: ackAuto})
		bystander := dialWire(s.addr(), wireOpts{acks: ackAuto})

		victim.commit(0)
		bystander.commit(0)
		_, err := bystander.await(10*time.Second, func(f *pb.Frame) bool { return f.GetPatch() != nil })
		Expect(err).NotTo(HaveOccurred())

		r.cut()
		Eventually(victim.isClosed, 10*time.Second).Should(BeTrue())

		// The bystander is still live and still being served.
		bystander.commit(0)
		f, err := bystander.await(10*time.Second, func(f *pb.Frame) bool {
			return f.GetPatch() != nil && patchHTML(f.GetPatch(), "total") != ""
		})
		Expect(err).NotTo(HaveOccurred(), "cutting one connection stopped another session being served")
		Expect(f.GetPatch().GetServerSeq()).To(BeNumerically(">", 1))
		Expect(bystander.isClosed()).To(BeFalse())
	})
})

// settle waits for the ledger to stop moving and returns the total. It is a
// measurement of quiescence rather than a sleep: an effect that is still
// running when the assertion is made would make "server truth" a moving target
// and the spec flaky in the direction that reads as a library defect.
func settle(l *ledger) int {
	GinkgoHelper()
	var last, stable int
	Eventually(func() int {
		n := l.callCount()
		if n == last {
			stable++
		} else {
			stable = 0
			last = n
		}
		return stable
	}, 30*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", 5),
		"the ledger never stopped moving, so there is no settled truth to compare against")
	return l.total()
}

// appliedRefs returns the client_refs the server named in patches this client
// actually received. It is the set "the user was shown this happened".
func appliedRefs(w *wire) []uint64 {
	var out []uint64
	seen := map[uint64]struct{}{}
	for _, p := range w.patches() {
		ref := p.GetOrigin().GetClientRef()
		if ref == 0 {
			continue
		}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}
