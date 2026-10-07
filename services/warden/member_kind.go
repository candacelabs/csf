package warden

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
