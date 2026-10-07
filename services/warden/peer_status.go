package warden

// PeerStatus is the liveness classification of a peer, derived from how
// long ago it was last seen (heartbeat response for the leader; heartbeat
// receipt for followers observing the leader).
type PeerStatus string

const (
	// StatusUnknown means the peer has never been seen since this
	// observer started.
	StatusUnknown PeerStatus = "unknown"
	// StatusAlive means the peer responded within SuspectAfter.
	StatusAlive PeerStatus = "alive"
	// StatusSuspect means no contact for at least SuspectAfter but less
	// than DeadAfter.
	StatusSuspect PeerStatus = "suspect"
	// StatusDead means no contact for at least DeadAfter. The leader's
	// watchdog raises an Incident on this transition.
	StatusDead PeerStatus = "dead"
)
