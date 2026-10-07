package warden

import "time"

// ClusterView is a point-in-time snapshot of the cluster as known by one
// node. The leader produces authoritative views from its own liveness
// tracking and piggybacks them on heartbeats; followers cache the most
// recent leader view so every node's dashboard shows the same cluster
// state. When no leader view is fresh (leaderless, or partitioned away
// from the leader), a node falls back to its own local observations with
// Authoritative == false.
type ClusterView struct {
	// Self is the node rendering/returning this view.
	Self NodeID `json:"self"`
	// Role is Self's current election role.
	Role Role `json:"role"`
	// Term is Self's current term.
	Term Term `json:"term"`
	// LeaderID is the current known leader, or "" if unknown/leaderless.
	LeaderID NodeID `json:"leader_id"`
	// Source is the node whose observations produced Peers (the leader
	// for authoritative views; Self for local fallback views).
	Source NodeID `json:"source"`
	// Authoritative is true when Peers reflects the current leader's
	// liveness tracking (either Self is the leader, or the view was
	// received from the leader within the freshness window).
	Authoritative bool `json:"authoritative"`
	// UpdatedAt is when Source produced the peer observations.
	UpdatedAt time.Time `json:"updated_at"`
	// Peers contains every cluster member including Self and Source,
	// sorted by Node.ID (see SortPeers).
	Peers []PeerView `json:"peers"`
	// ElectionsStarted counts elections Self has started since boot
	// (exported as the warden_elections_total metric).
	ElectionsStarted uint64 `json:"elections_started"`
	// Membership is the effective voting configuration this view was
	// rendered under.
	Membership Membership `json:"membership"`
}
