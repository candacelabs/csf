package warden

import "context"

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
