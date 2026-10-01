package discovery

import (
	"context"

	"github.com/candacelabs/csf/services/warden"
)

// Static is a PeerDiscoverer that reports one fixed roster and then stays quiet.
// It is the explicit "static" discovery mode (the configured peer seed is the
// roster and never changes) and a deterministic stand-in in tests.
type Static struct {
	nodes []warden.Node
}

var _ warden.IPeerDiscoverer = (*Static)(nil)

// NewStatic returns a Static discoverer over a copy of nodes.
func NewStatic(nodes []warden.Node) *Static {
	return &Static{nodes: copyNodes(nodes)}
}

// Discover sends exactly one Roster (a copy of the configured nodes) promptly,
// keeps the channel open with no further sends, and closes it when ctx ends.
func (s *Static) Discover(ctx context.Context) (<-chan warden.Roster, error) {
	ch := make(chan warden.Roster, 1)
	// Buffered send: prompt and non-blocking even if no consumer is reading yet.
	ch <- warden.Roster{Nodes: copyNodes(s.nodes)}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}
