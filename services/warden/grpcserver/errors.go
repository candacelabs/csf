package grpcserver

import (
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Canonical gRPC status-code mapping for the warden RPC surface. This table is
// the SINGLE authority for how a server-side condition becomes a gRPC code; no
// handler emits a code via an ad-hoc literal. Context-derived codes (a client
// cancel or an elapsed RPC deadline) are produced by status.FromContextError at
// the point they occur and so are documented here but not pre-built.
//
//	Condition                                          Code
//	------------------------------------------------   ----------------
//	Vote request omits candidate_id                    InvalidArgument
//	Heartbeat request omits leader_id                  InvalidArgument
//	handler panics (unexpected internal fault)         Internal
//	client cancels / RPC deadline elapses              Canceled / DeadlineExceeded
//	server draining an active WatchCluster stream      Unavailable
//	ViewSource closed the subscription mid-stream      Unavailable
//	success                                            OK
const (
	msgMissingCandidateID = "warden: vote request missing candidate_id"
	msgMissingLeaderID    = "warden: heartbeat request missing leader_id"
	msgDraining           = "warden: server draining; reopen the watch on another node"
	msgViewSourceClosed   = "warden: cluster view source closed"
	msgInternal           = "warden: internal error"
)

var (
	// errMissingCandidateID rejects a vote that names no candidate.
	errMissingCandidateID = status.Error(codes.InvalidArgument, msgMissingCandidateID)
	// errMissingLeaderID rejects a heartbeat that asserts no leader.
	errMissingLeaderID = status.Error(codes.InvalidArgument, msgMissingLeaderID)
	// errDraining ends an active WatchCluster stream when the node begins a
	// graceful shutdown; the client should reopen the watch on another node.
	errDraining = status.Error(codes.Unavailable, msgDraining)
	// errViewSourceClosed ends a WatchCluster stream when the underlying
	// ViewSource closes the subscription (the election loop shut down).
	errViewSourceClosed = status.Error(codes.Unavailable, msgViewSourceClosed)
	// errInternal is the opaque code returned after a recovered handler panic;
	// the detail is logged, never leaked to the caller.
	errInternal = status.Error(codes.Internal, msgInternal)
)

// logPanic records a recovered handler panic through the structured logger.
func logPanic(log *zerolog.Logger, method string, rec any) {
	if log == nil {
		return
	}
	log.Error().Str("method", method).Interface("panic", rec).Msg("warden grpc: recovered from panic")
}
