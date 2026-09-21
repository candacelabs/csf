package election

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
)

// drainViews non-blockingly reads every buffered snapshot currently on ch.
func drainViews(ch <-chan warden.ClusterView) []warden.ClusterView {
	var out []warden.ClusterView
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

var _ = Describe("view subscription", func() {
	// TestSubscribeDeliversSnapshotsOnChange
	It("delivers an initial snapshot and a fresh one on every change, and closes on cancel", func() {
		s := newSolo(GinkgoT(), store.NewMemStore())
		ctx := context.Background()

		ch, cancel := s.m.Subscribe(16)

		// Subscribing delivers an immediate snapshot.
		got := drainViews(ch)
		Expect(got).NotTo(BeEmpty(), "expected an initial snapshot on subscribe")

		// A state change (accepting a leader via heartbeat) must publish a
		// snapshot reflecting the new leader.
		s.m.HandleHeartbeat(ctx, warden.HeartbeatRequest{Term: 2, LeaderID: "b"})
		got = drainViews(ch)
		Expect(got).NotTo(BeEmpty(), "expected a snapshot after a state change")
		last := got[len(got)-1]
		Expect(last.LeaderID).To(Equal(warden.NodeID("b")), "latest snapshot should reflect leader b")
		Expect(last.Term).To(Equal(warden.Term(2)), "latest snapshot should reflect term 2")

		// cancel closes the channel (after the loop processes the unsubscribe).
		cancel()
		s.m.barrier()
		// Ranging over the channel drains any buffered snapshots and then
		// returns once it observes the close. If the channel were not closed
		// this would hang, failing the test via timeout.
		for range ch {
		}
		_, ok := <-ch
		Expect(ok).To(BeFalse(), "channel should be closed after cancel")

		// cancel is idempotent.
		cancel()
	})

	// TestSlowSubscriberDoesNotBlockManager
	It("never blocks the manager on a slow subscriber", func() {
		s := newSolo(GinkgoT(), store.NewMemStore())
		ctx := context.Background()

		// Tiny buffer, and we never read: the Manager must never block on us.
		ch, cancel := s.m.Subscribe(1)
		defer cancel()

		// Drive many state changes. If publishing blocked on the full channel,
		// these synchronous handler calls would deadlock and the test would hang.
		for i := 0; i < 100; i++ {
			term := warden.Term(i + 1)
			s.m.HandleHeartbeat(ctx, warden.HeartbeatRequest{Term: term, LeaderID: "b"})
		}

		// The subscriber still holds (at most) its buffered snapshot; the point
		// is simply that the Manager stayed responsive.
		Expect(s.m.View().Term).To(Equal(warden.Term(100)), "manager should have processed all heartbeats")
		_ = ch
	})
})
