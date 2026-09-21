package discovery

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

var _ = Describe("Static discoverer", func() {
	// TestStaticInitialSnapshot
	It("emits the seed roster once, then stays open and quiet", func() {
		nodes := []warden.Node{
			{ID: "node-c", Addr: "203.0.113.13:7717"},
			{ID: "node-d", Addr: "203.0.113.14:7717"},
		}
		s := NewStatic(nodes)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		ch, err := s.Discover(ctx)
		Expect(err).NotTo(HaveOccurred())
		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"node-c", "node-d"}))
		// No further sends: the channel stays open and quiet.
		expectNoRoster(ch, 100*time.Millisecond)
	})

	// TestStaticCopiesInput
	It("copies the input so later caller mutations do not affect the roster", func() {
		nodes := []warden.Node{{ID: "a", Addr: "203.0.113.24:7717"}}
		s := NewStatic(nodes)
		// Mutating the caller's slice after construction must not affect the roster.
		nodes[0].ID = "mutated"

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := s.Discover(ctx)
		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"a"}))
	})

	// TestStaticClosesOnCtxEnd
	It("closes the channel when the context ends", func() {
		s := NewStatic([]warden.Node{{ID: "a", Addr: "203.0.113.24:7717"}})
		ctx, cancel := context.WithCancel(context.Background())
		ch, _ := s.Discover(ctx)
		_ = recvRoster(ch) // consume initial snapshot
		cancel()
		expectClosed(ch)
	})
})
