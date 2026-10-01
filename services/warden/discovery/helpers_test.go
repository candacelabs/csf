package discovery

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// recvTimeout is generous relative to the short (10ms) poll intervals used in
// tests, so a correct source never times out even on a busy/-race machine.
const recvTimeout = 2 * time.Second

// recvRoster receives one roster or fails on timeout / unexpected close.
func recvRoster(ch <-chan warden.Roster) warden.Roster {
	GinkgoHelper()
	select {
	case r, ok := <-ch:
		Expect(ok).To(BeTrue(), "channel closed while waiting for a roster")
		return r
	case <-time.After(recvTimeout):
		Fail(fmt.Sprintf("timed out after %s waiting for a roster", recvTimeout))
		return warden.Roster{}
	}
}

// expectNoRoster asserts that no roster (and no close) arrives within window.
func expectNoRoster(ch <-chan warden.Roster, window time.Duration) {
	GinkgoHelper()
	select {
	case r, ok := <-ch:
		if !ok {
			Fail("channel closed unexpectedly while expecting silence")
		}
		Fail(fmt.Sprintf("unexpected roster while expecting silence: %+v", r))
	case <-time.After(window):
	}
}

// expectClosed drains any already-buffered roster and asserts the channel then
// closes within recvTimeout.
func expectClosed(ch <-chan warden.Roster) {
	GinkgoHelper()
	deadline := time.After(recvTimeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			Fail(fmt.Sprintf("channel not closed within %s", recvTimeout))
		}
	}
}

// ids extracts the node IDs from a roster for order-sensitive comparison.
func ids(r warden.Roster) []warden.NodeID {
	out := make([]warden.NodeID, len(r.Nodes))
	for i, n := range r.Nodes {
		out[i] = n.ID
	}
	return out
}

// addrOf returns the Addr of the node with id, or "" if absent.
func addrOf(r warden.Roster, id warden.NodeID) string {
	for _, n := range r.Nodes {
		if n.ID == id {
			return n.Addr
		}
	}
	return ""
}
