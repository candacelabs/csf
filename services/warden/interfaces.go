package warden

import "context"

// ITransport sends cluster RPCs to peers. The production implementation
// (services/warden/grpctransport) carries them over the gRPC WardenService (h2c on
// the tailnet); tests substitute in-memory fakes to simulate partitions,
// delays, and node death.
type ITransport interface {
	// RequestVote sends a VoteRequest to peer and returns its response.
	// An error means the peer was unreachable or replied malformed; the
	// caller treats it as a vote not granted.
	RequestVote(ctx context.Context, peer Node, req VoteRequest) (VoteResponse, error)
	// SendHeartbeat sends a HeartbeatRequest to peer and returns its
	// response. An error means the peer was unreachable; the leader's
	// liveness tracker records the failed contact.
	SendHeartbeat(ctx context.Context, peer Node, req HeartbeatRequest) (HeartbeatResponse, error)
	// Identify asks peer for its cluster identity (used to verify that a
	// discovered node is a warden of the same cluster before treating it
	// as an observer). An error means unreachable or not a warden.
	Identify(ctx context.Context, peer Node) (IdentifyResponse, error)
}

// IPeerDiscoverer reports candidate cluster nodes. Discover returns a channel
// delivering roster snapshots until ctx ends (the implementation closes the
// channel when done and must send an initial snapshot promptly). Snapshots
// are advisory: the election manager's event loop consumes them, verifies
// candidates via ITransport.Identify, and only the LEADER turns stable,
// verified candidates into one-at-a-time membership changes. When the
// discovery source is unavailable, implementations should keep the channel
// open and simply not send (consumers fall back to the last known roster and
// the persisted membership — never to an empty set).
type IPeerDiscoverer interface {
	Discover(ctx context.Context) (<-chan Roster, error)
}

// IRPCHandler is the server side of the wire protocol, implemented by the
// election manager and served as the gRPC WardenService by services/warden/grpcserver
// (multiplexed onto the single node port with the HTTP surface by
// services/warden/grpcmux). Implementations must be safe for concurrent use.
type IRPCHandler interface {
	HandleVote(ctx context.Context, req VoteRequest) VoteResponse
	HandleHeartbeat(ctx context.Context, req HeartbeatRequest) HeartbeatResponse
	// HandleIdentify serves the cluster-identity handshake.
	HandleIdentify(ctx context.Context) IdentifyResponse
}

// INotifier delivers a watchdog Incident to the operator. Production uses
// SMTP email; tests use a recording mock; the e2e harness uses a file
// sink. Implementations must be safe for concurrent use. Notify should
// return an error only on delivery failure; the watchdog logs failures
// and retries on the next evaluation (dedup still prevents duplicate
// notifications once one delivery succeeds).
type INotifier interface {
	Notify(ctx context.Context, inc Incident) error
}

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

// IStore persists PersistentState. Save must be atomic and durable before
// returning (write-then-rename for the file implementation). Load returns
// ok == false when no state has ever been saved.
type IStore interface {
	Save(st PersistentState) error
	Load() (st PersistentState, ok bool, err error)
}

// IViewSource provides cluster view snapshots and change notifications.
// Implemented by the election manager; consumed by the watchdog,
// dashboard, and metrics packages.
type IViewSource interface {
	// View returns the current cluster view snapshot. Safe for
	// concurrent use; the returned value is a copy the caller may keep.
	View() ClusterView
	// Subscribe returns a channel that receives view snapshots after
	// state changes (role/term/leader changes, peer status transitions).
	// Delivery is best-effort: when the buffered channel is full,
	// intermediate updates are dropped, so consumers should treat a
	// receive as a change signal and may re-read View() for the latest
	// state. cancel unsubscribes and closes the channel.
	Subscribe(buf int) (ch <-chan ClusterView, cancel func())
}

// IIncidentLog exposes the incident history for the dashboard. Implemented
// by the watchdog. Returned slices are copies, most recent first.
type IIncidentLog interface {
	Incidents() []Incident
}
