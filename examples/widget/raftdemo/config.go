package raftdemo

import (
	"errors"
	"fmt"
	"time"
)

// The faults this package reports. Each is a sentinel because a caller that
// wants to tell "you asked for too many nodes" from "you named a node that does
// not exist" should not have to read an English sentence to do it.
var (
	// ErrNodeCount is a node count outside [1, maxNodes].
	ErrNodeCount = errors.New("raftdemo: the node count is out of range")

	// ErrHeartbeat is a heartbeat interval that is not positive.
	ErrHeartbeat = errors.New("raftdemo: the heartbeat interval is not positive")

	// ErrElectionTimeout is an election timeout that does not clear the
	// heartbeat interval by enough for a leader to keep its followers quiet.
	ErrElectionTimeout = errors.New("raftdemo: the election timeout does not exceed twice the heartbeat interval")

	// ErrElectionJitter is the livelock: a multi-node cluster whose nodes all
	// time out at exactly the same moment splits its vote every term.
	ErrElectionJitter = errors.New("raftdemo: a cluster of more than one node needs a positive election jitter")

	// ErrUnknownNode names a node this cluster does not have.
	ErrUnknownNode = errors.New("raftdemo: no such node")

	// ErrAlreadyRunning is a second call to Run. One cluster runs once: its
	// goroutines are started against the context Run was given, so a second
	// call would start a second set against a second context and leave two
	// networks delivering into one set of inboxes.
	ErrAlreadyRunning = errors.New("raftdemo: the cluster is already running")
)

// maxNodes is the largest cluster this engine runs.
//
// The ceiling is a bitmask: a node tracks who voted for it and who acknowledged
// its last heartbeat as two uint64 words, which keeps nodeState a copyable value
// and therefore keeps every transition a pure function of one. A demo cluster is
// three nodes; the limit is stated and refused rather than left to overflow into
// a node that silently never counts.
const maxNodes = 64

// Config is one cluster's shape and pace.
//
// Every field is required: there is no zero value that means "work it out". A
// heartbeat interval a library chose would decide how fast the picture beside it
// moves, and an election timeout a library chose would decide how long an outage
// looks like a pause. [DefaultConfig] is where the demo's own answers live.
type Config struct {
	// Nodes is how many members the cluster has. Quorum is Nodes/2+1, so an
	// even count buys no extra fault tolerance over the odd one below it.
	Nodes int

	// Heartbeat is how often a leader broadcasts, and therefore how often one
	// snapshot reaches a subscriber: one heartbeat round is one snapshot is one
	// round of pulses in the widget.
	Heartbeat time.Duration

	// ElectionTimeout is how long a follower waits to hear from a leader before
	// standing for election. It must exceed twice Heartbeat or a healthy leader
	// cannot keep its followers from campaigning over it.
	ElectionTimeout time.Duration

	// ElectionJitter is the width of the random interval added to
	// ElectionTimeout, per node, per wait. It must be positive for any cluster
	// larger than one node: equal timers are the classic Raft livelock, where
	// every follower campaigns at the same instant, every term splits its vote,
	// and no leader is ever elected.
	ElectionJitter time.Duration

	// Seed seeds the per-node jitter streams. Equal seeds give equal jitter
	// sequences; they do not give an equal scheduler, so a run is reproducible
	// in its timeouts and not in its winner.
	Seed int64
}

// DefaultConfig is the demo's own pace: three nodes, a heartbeat slow enough
// that one round of pulses is watchable, and an election timeout far enough
// above it that a healthy leader is never campaigned over.
func DefaultConfig() Config {
	return Config{
		Nodes:           3,
		Heartbeat:       900 * time.Millisecond,
		ElectionTimeout: 2500 * time.Millisecond,
		ElectionJitter:  1200 * time.Millisecond,
		Seed:            1,
	}
}

// Validate reports the first fault in a configuration.
//
// It is called by [New] rather than by the caller, so a cluster that exists is a
// cluster whose configuration was checked — and the checks are the ones whose
// violation is a cluster that runs and never elects, which is the failure that
// looks like a hang rather than like an error.
func (config Config) Validate() error {
	switch {
	case config.Nodes < 1 || config.Nodes > maxNodes:
		return fmt.Errorf("%w: %d, which is not in [1, %d]", ErrNodeCount, config.Nodes, maxNodes)
	case config.Heartbeat <= 0:
		return fmt.Errorf("%w: %s", ErrHeartbeat, config.Heartbeat)
	case config.ElectionTimeout <= 2*config.Heartbeat:
		return fmt.Errorf("%w: %s against a %s heartbeat",
			ErrElectionTimeout, config.ElectionTimeout, config.Heartbeat)
	case config.Nodes > 1 && config.ElectionJitter <= 0:
		return fmt.Errorf("%w: %s across %d nodes", ErrElectionJitter, config.ElectionJitter, config.Nodes)
	}
	return nil
}

// quorum is the majority this cluster needs to elect and to alert: n/2+1,
// counting the node itself. Two majorities of one set overlap on at least one
// member, and a member votes at most once per term, so at most one leader can
// exist per term — which is why this one expression is the whole of the
// split-brain argument.
func (config Config) quorum() int { return config.Nodes/2 + 1 }
