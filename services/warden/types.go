package warden

import (
	"fmt"
	"sort"
	"time"
)

// NodeID uniquely identifies a node in the cluster (e.g. "node-a").
type NodeID string

// Node is a member of the static cluster peer set.
type Node struct {
	ID NodeID `json:"id" yaml:"id"`
	// Addr is the host:port the node's warden HTTP server listens on,
	// reachable over the tailnet (e.g. "203.0.113.10:7717").
	Addr string `json:"addr" yaml:"addr"`
}

// Term is a monotonically increasing election term, Raft-style. A node
// persists its current term (and vote) via IStore so terms never regress
// across restarts.
type Term uint64

// Role is a node's current role in the election state machine.
type Role string

const (
	RoleFollower  Role = "follower"
	RoleCandidate Role = "candidate"
	RoleLeader    Role = "leader"
)

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

// MemberKind classifies a node's relationship to the voting cluster.
type MemberKind string

const (
	// MemberVoter is a full member: counted in quorum, may vote and lead.
	MemberVoter MemberKind = "voter"
	// MemberObserver is an identify-verified warden node awaiting
	// admission: it receives heartbeats and views but never votes, never
	// counts toward quorum, and never starts elections.
	MemberObserver MemberKind = "observer"
	// MemberDiscovered is a node reported by the discovery source that has
	// not (yet) been identify-verified as part of this cluster.
	MemberDiscovered MemberKind = "discovered"
)

// Membership is the effective voting configuration. It is persisted, changed
// ONLY by the leader, strictly one node at a time (single-server changes keep
// any old-majority/new-majority pair overlapping, which preserves election
// safety), and disseminated via heartbeats. Quorum is ALWAYS computed over
// Voters — never over a discovery roster and never over "currently reachable
// peers" — so unreachability can never shrink the quorum denominator.
type Membership struct {
	// Version increases by one per membership change.
	Version uint64 `json:"version"`
	// CreatedInTerm is the term of the leader that minted this membership.
	// It disambiguates sibling configurations: a leader that persists a new
	// version and is deposed before disseminating it leaves a config with
	// the same Version as the one the next leader mints. Identity is the
	// (Version, CreatedInTerm) pair — see Supersedes.
	CreatedInTerm Term `json:"created_in_term"`
	// Voters is the full voting member set, sorted by ID.
	Voters []Node `json:"voters"`
}

// Supersedes reports whether m is strictly newer than other under the
// lexicographic (Version, CreatedInTerm) order. Adoption uses this — never a
// bare Version comparison — so a higher-term leader's config replaces a
// stale sibling of equal Version, and ack accounting can distinguish a node
// holding a divergent config from one holding the config being settled.
func (m Membership) Supersedes(other Membership) bool {
	if m.Version != other.Version {
		return m.Version > other.Version
	}
	return m.CreatedInTerm > other.CreatedInTerm
}

// Clone deep-copies the Membership (its Voters slice) so a snapshot can be
// persisted, disseminated, or handed out without aliasing the owner's state.
func (m Membership) Clone() Membership {
	cp := Membership{Version: m.Version, CreatedInTerm: m.CreatedInTerm}
	if len(m.Voters) > 0 {
		cp.Voters = make([]Node, len(m.Voters))
		copy(cp.Voters, m.Voters)
	}
	return cp
}

// HasVoter reports whether id is in the voting set.
func (m Membership) HasVoter(id NodeID) bool {
	for _, n := range m.Voters {
		if n.ID == id {
			return true
		}
	}
	return false
}

// Roster is a discovery snapshot: candidate cluster nodes as reported by an
// IPeerDiscoverer. It is advisory only — it never directly changes voting
// membership.
type Roster struct {
	Nodes []Node `json:"nodes"`
}

// SortPeers sorts a PeerView slice by node ID, the canonical order for
// ClusterView.Peers.
func SortPeers(peers []PeerView) {
	sort.Slice(peers, func(i, j int) bool { return peers[i].Node.ID < peers[j].Node.ID })
}

// SortNodes sorts a Node slice by ID, the canonical order for
// Membership.Voters and Roster.Nodes.
func SortNodes(ns []Node) {
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
}

// Quorum returns the majority threshold for a cluster of n nodes
// (n/2 + 1). A candidate needs at least this many votes, counting its own.
func Quorum(n int) int { return n/2 + 1 }

// IncidentType classifies watchdog incidents.
type IncidentType string

const (
	// IncidentPeerDead is raised by the leader when a peer transitions
	// to StatusDead, or when a newly elected leader first observes an
	// already-dead peer.
	IncidentPeerDead IncidentType = "peer_dead"
	// IncidentPeerRecovered is raised by the leader when a peer that had
	// a peer_dead incident becomes StatusAlive again.
	IncidentPeerRecovered IncidentType = "peer_recovered"
)

// Incident is a single watchdog event that (subject to dedup/cooldown)
// results in exactly one operator notification.
type Incident struct {
	// ID uniquely identifies the incident, e.g. "peer_dead/node-a/1721433600".
	ID   string       `json:"id"`
	Type IncidentType `json:"type"`
	// Peer is the affected node.
	Peer Node `json:"peer"`
	// Term is the reporting leader's term when the incident was detected.
	Term Term `json:"term"`
	// ReportedBy is the leader that detected the incident.
	ReportedBy NodeID `json:"reported_by"`
	// DetectedAt is when the leader detected the transition.
	DetectedAt time.Time `json:"detected_at"`
	// LastSeen is the leader's last successful contact with the peer
	// before the incident (zero if never seen).
	LastSeen time.Time `json:"last_seen"`
	// Message is a human-readable summary suitable for an email body line.
	Message string `json:"message"`
}

// NewIncidentID builds the canonical incident ID.
func NewIncidentID(t IncidentType, peer NodeID, at time.Time) string {
	return fmt.Sprintf("%s/%s/%d", t, peer, at.Unix())
}
