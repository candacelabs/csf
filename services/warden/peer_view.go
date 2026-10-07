package warden

import (
	"sort"
	"time"
)

// PeerView is one row of a ClusterView: a single peer as observed by the
// view's Source node.
type PeerView struct {
	Node   Node       `json:"node"`
	Status PeerStatus `json:"status"`
	// LastSeen is the Source's last successful contact with this peer
	// (zero time if never seen).
	LastSeen time.Time `json:"last_seen"`
	// LatencyMS is the most recent heartbeat round-trip time in
	// milliseconds as measured by the Source (0 if unknown).
	LatencyMS float64 `json:"latency_ms"`
	// Member classifies the node's membership standing (voter, observer,
	// discovered). Producers must always set it; an empty value is treated
	// as MemberVoter for backward compatibility.
	Member MemberKind `json:"member,omitempty"`
}

// SortPeers sorts a PeerView slice by node ID, the canonical order for
// ClusterView.Peers.
func SortPeers(peers []PeerView) {
	sort.Slice(peers, func(i, j int) bool { return peers[i].Node.ID < peers[j].Node.ID })
}
