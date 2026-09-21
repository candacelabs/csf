// Package grpcserver implements the candacenet.warden.v1 WardenService: the
// three unary cluster RPCs (Vote/Heartbeat/Identify) delegating to the existing
// warden.IRPCHandler through the wireconv boundary, and the server-streaming
// WatchCluster that pushes full ClusterView snapshots from a warden.IViewSource.
//
// The server holds no mutable state of its own: the unary handlers are pure
// delegations and every WatchCluster invocation is an independent consumer
// goroutine (see watch.go). Error codes follow the single table in errors.go,
// which is the package's contract with clients: every code a caller can observe
// is named there, no handler invents one inline, and a code in that table is
// wire behaviour rather than an implementation detail. Embedding the generated
// Unimplemented base means a schema that grows a new RPC still compiles here,
// so additive schema growth never breaks a node mid-upgrade.
package grpcserver

import (
	"context"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"

	core "github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
	"github.com/candacelabs/csf/services/warden/wireconv"
)

// compile-time assertion: Server satisfies the generated service interface.
var _ wardenv1.WardenServiceServer = (*Server)(nil)

// Server adapts a warden.IRPCHandler and a warden.IViewSource to the generated
// WardenService. Embedding the Unimplemented base keeps it forward-compatible
// with additive schema growth.
type Server struct {
	wardenv1.UnimplementedWardenServiceServer

	rpc   warden.IRPCHandler
	views warden.IViewSource
	// drain is canceled when the process begins a graceful shutdown; active
	// WatchCluster streams observe it and return errDraining so in-flight
	// streams end cleanly instead of being force-closed. A nil drain means
	// "never drains" (its Done channel never fires).
	drain context.Context
	log   *zerolog.Logger
}

// New builds a Server. drain is the process shutdown signal for WatchCluster
// streams; pass a never-canceled context (or nil) when there is no drain
// coordinator (e.g. focused tests). The structured logger is captured from
// core.Logger at construction, matching the election manager.
func New(rpc warden.IRPCHandler, views warden.IViewSource, drain context.Context) *Server {
	if drain == nil {
		drain = context.Background()
	}
	return &Server{rpc: rpc, views: views, drain: drain, log: core.Logger}
}

// NewGRPCServer builds a *grpc.Server for h2c (cleartext HTTP/2) serving with
// the warden panic-recovery interceptors installed and s registered. The
// listener it is served on provides transport security (the tailnet), so no
// transport credentials are configured here — see the grpcmux package.
func NewGRPCServer(s *Server) *grpc.Server {
	gs := grpc.NewServer(
		grpc.UnaryInterceptor(recoverUnary(s.log)),
		grpc.StreamInterceptor(recoverStream(s.log)),
	)
	wardenv1.RegisterWardenServiceServer(gs, s)
	return gs
}

// Vote delegates a VoteRequest to the RPCHandler. A vote naming no candidate is
// rejected with InvalidArgument (candidate_id is required); the handler itself
// never fails, so success is the only other outcome.
func (s *Server) Vote(ctx context.Context, req *wardenv1.VoteRequest) (*wardenv1.VoteResponse, error) {
	if req.GetCandidateId() == "" {
		return nil, errMissingCandidateID
	}
	resp := s.rpc.HandleVote(ctx, wireconv.VoteRequestFromProto(req))
	return wireconv.VoteResponseToProto(resp), nil
}

// Heartbeat delegates a HeartbeatRequest to the RPCHandler. A heartbeat
// asserting no leader is rejected with InvalidArgument (leader_id is required).
func (s *Server) Heartbeat(ctx context.Context, req *wardenv1.HeartbeatRequest) (*wardenv1.HeartbeatResponse, error) {
	if req.GetLeaderId() == "" {
		return nil, errMissingLeaderID
	}
	resp := s.rpc.HandleHeartbeat(ctx, wireconv.HeartbeatRequestFromProto(req))
	return wireconv.HeartbeatResponseToProto(resp), nil
}

// Identify answers the cluster-identity handshake. The request is intentionally
// empty, so there is nothing to validate.
func (s *Server) Identify(ctx context.Context, _ *wardenv1.IdentifyRequest) (*wardenv1.IdentifyResponse, error) {
	return wireconv.IdentifyResponseToProto(s.rpc.HandleIdentify(ctx)), nil
}

// recoverUnary maps a recovered handler panic to the opaque Internal code,
// mirroring the gin recovery middleware on the HTTP surface.
func recoverUnary(log *zerolog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				logPanic(log, info.FullMethod, rec)
				err = errInternal
			}
		}()
		return handler(ctx, req)
	}
}

// recoverStream is recoverUnary's streaming counterpart.
func recoverStream(log *zerolog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				logPanic(log, info.FullMethod, rec)
				err = errInternal
			}
		}()
		return handler(srv, ss)
	}
}
