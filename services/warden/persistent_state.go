package warden

// PersistentState is the durable election state, per Raft: persisting the
// current term and the vote cast in it guarantees a node can never vote
// twice in the same term across restarts, which is what makes a majority
// quorum imply at most one leader per term.
type PersistentState struct {
	CurrentTerm Term   `json:"current_term"`
	VotedFor    NodeID `json:"voted_for"`
	// Membership is the effective voting configuration, persisted so a
	// restart resumes with the same quorum denominator (nil in state files
	// written before membership support; the config seed applies then).
	Membership *Membership `json:"membership,omitempty"`
}
