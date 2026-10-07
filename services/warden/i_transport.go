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
