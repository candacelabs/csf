package warden

// Term is a monotonically increasing election term, Raft-style. A node
// persists its current term (and vote) via IStore so terms never regress
// across restarts.
type Term uint64
