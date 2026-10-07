package warden

// Quorum returns the majority threshold for a cluster of n nodes
// (n/2 + 1). A candidate needs at least this many votes, counting its own.
func Quorum(n int) int { return n/2 + 1 }
