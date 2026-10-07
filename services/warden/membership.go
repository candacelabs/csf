package warden

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
