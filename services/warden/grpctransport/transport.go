// Package grpctransport is the gRPC client side of the warden cluster wire
// protocol: it implements warden.ITransport (RequestVote/SendHeartbeat/Identify)
// over the candacenet.warden.v1 WardenService, replacing the retired HTTP/JSON
// HTTPTransport. It dials each peer with h2c prior-knowledge and insecure
// credentials — the tailnet is the transport-security boundary (see
// services/warden/grpcmux for the matching rationale).
//
// # Connection lifecycle (CSP, no mutex)
//
// One grpc.ClientConn is kept per peer address and reused across RPCs. The
// cache is owned by a single goroutine reached over a request/reply channel;
// grpc.NewClient is lazy (it dials on first RPC, never blocks), so vending a
// connection from the owner goroutine performs no I/O. Close cancels the owner,
// which closes every connection. No shared mutable state, no mutex.
//
// # Deadlines
//
// Per-request deadlines mirror the retired HTTPTransport exactly: a
// caller-supplied context deadline is always honoured as-is; the configured
// default timeout is applied ONLY when the caller's context carries no deadline
// of its own.
package grpctransport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/candacelabs/csf/services/warden"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
	"github.com/candacelabs/csf/services/warden/wireconv"
)

// errClosed is returned when an RPC is attempted after Close.
var errClosed = errors.New("grpctransport: closed")

// Reconnect policy. gRPC keeps one long-lived HTTP/2 connection per peer and,
// on failure, retries with exponential backoff. The default backoff (BaseDelay
// 1s, MaxDelay 120s) is far too slow for a heartbeat-driven cluster: when a
// peer restarts, a standing leader must re-reach it within roughly an election
// timeout or the peer, hearing no heartbeat, needlessly self-elects. These
// values keep reconnection prompt — matching the effectively-zero reconnect
// delay of the retired HTTP transport (which simply re-dialled on the next
// request) — so a recovered peer rejoins as a follower.
const (
	reconnectBaseDelay         = 50 * time.Millisecond
	reconnectMaxDelay          = 250 * time.Millisecond
	reconnectMultiplier        = 1.5
	reconnectJitter            = 0.2
	reconnectMinConnectTimeout = 1 * time.Second
)

var _ warden.ITransport = (*Transport)(nil)

// connReq asks the owner goroutine for the ClientConn to addr.
type connReq struct {
	addr  string
	reply chan connResult
}

type connResult struct {
	conn *grpc.ClientConn
	err  error
}

// Transport is the gRPC implementation of warden.ITransport. Construct with New;
// release with Close. It is safe for concurrent use.
type Transport struct {
	timeout   time.Duration
	dialOpts  []grpc.DialOption
	reqs      chan connReq
	done      chan struct{}
	closeOnce chan struct{} // capacity-1 guard so Close is idempotent
}

// New returns a Transport whose default per-request timeout is applied only when
// a caller passes a context without its own deadline. It starts the connection
// owner goroutine; call Close to release all peer connections.
func New(timeout time.Duration) *Transport {
	t := &Transport{
		timeout: timeout,
		dialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithConnectParams(grpc.ConnectParams{
				Backoff: backoff.Config{
					BaseDelay:  reconnectBaseDelay,
					Multiplier: reconnectMultiplier,
					Jitter:     reconnectJitter,
					MaxDelay:   reconnectMaxDelay,
				},
				MinConnectTimeout: reconnectMinConnectTimeout,
			}),
		},
		reqs:      make(chan connReq),
		done:      make(chan struct{}),
		closeOnce: make(chan struct{}, 1),
	}
	go t.run()
	return t
}

// run is the connection-cache owner: it exclusively owns the addr->conn map,
// answering connReqs until Close, then closes every connection.
func (t *Transport) run() {
	conns := make(map[string]*grpc.ClientConn)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for {
		select {
		case req := <-t.reqs:
			c, ok := conns[req.addr]
			if !ok {
				nc, err := grpc.NewClient(req.addr, t.dialOpts...)
				if err != nil {
					req.reply <- connResult{err: fmt.Errorf("dialing %s: %w", req.addr, err)}
					continue
				}
				conns[req.addr] = nc
				c = nc
			}
			req.reply <- connResult{conn: c}
		case <-t.done:
			return
		}
	}
}

// Close releases every peer connection and stops the owner goroutine. It is
// idempotent and safe to call concurrently.
func (t *Transport) Close() error {
	select {
	case t.closeOnce <- struct{}{}:
		close(t.done)
	default:
	}
	return nil
}

// conn returns the (lazily created, cached) ClientConn for addr, honouring both
// caller cancellation and Close.
func (t *Transport) conn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	reply := make(chan connResult, 1)
	select {
	case t.reqs <- connReq{addr: addr, reply: reply}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, errClosed
	}
	select {
	case res := <-reply:
		return res.conn, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, errClosed
	}
}

// withTimeout applies the default timeout only when ctx has no deadline, exactly
// matching the retired HTTPTransport semantics.
func (t *Transport) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); !ok && t.timeout > 0 {
		return context.WithTimeout(ctx, t.timeout)
	}
	return ctx, func() {}
}

// client resolves the WardenService client for peer, applying the deadline
// policy. The returned cancel must always be called.
func (t *Transport) client(ctx context.Context, peer warden.Node) (wardenv1.WardenServiceClient, context.Context, context.CancelFunc, error) {
	ctx, cancel := t.withTimeout(ctx)
	cc, err := t.conn(ctx, peer.Addr)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	return wardenv1.NewWardenServiceClient(cc), ctx, cancel, nil
}

// RequestVote sends a VoteRequest to peer and returns its VoteResponse.
func (t *Transport) RequestVote(ctx context.Context, peer warden.Node, req warden.VoteRequest) (warden.VoteResponse, error) {
	cl, ctx, cancel, err := t.client(ctx, peer)
	if err != nil {
		return warden.VoteResponse{}, err
	}
	defer cancel()
	resp, err := cl.Vote(ctx, wireconv.VoteRequestToProto(req))
	if err != nil {
		return warden.VoteResponse{}, fmt.Errorf("vote to %s: %w", peer.ID, err)
	}
	return wireconv.VoteResponseFromProto(resp), nil
}

// SendHeartbeat sends a HeartbeatRequest to peer and returns its response.
func (t *Transport) SendHeartbeat(ctx context.Context, peer warden.Node, req warden.HeartbeatRequest) (warden.HeartbeatResponse, error) {
	cl, ctx, cancel, err := t.client(ctx, peer)
	if err != nil {
		return warden.HeartbeatResponse{}, err
	}
	defer cancel()
	resp, err := cl.Heartbeat(ctx, wireconv.HeartbeatRequestToProto(req))
	if err != nil {
		return warden.HeartbeatResponse{}, fmt.Errorf("heartbeat to %s: %w", peer.ID, err)
	}
	return wireconv.HeartbeatResponseFromProto(resp), nil
}

// Identify asks peer for its cluster identity.
func (t *Transport) Identify(ctx context.Context, peer warden.Node) (warden.IdentifyResponse, error) {
	cl, ctx, cancel, err := t.client(ctx, peer)
	if err != nil {
		return warden.IdentifyResponse{}, err
	}
	defer cancel()
	resp, err := cl.Identify(ctx, &wardenv1.IdentifyRequest{})
	if err != nil {
		return warden.IdentifyResponse{}, fmt.Errorf("identify %s: %w", peer.ID, err)
	}
	return wireconv.IdentifyResponseFromProto(resp), nil
}
