// Package grpcmux multiplexes the warden gRPC plane and the existing HTTP
// surface onto a SINGLE bound port using soheilhy/cmux. Connections whose first
// frames are HTTP/2 carrying content-type application/grpc (h2c, prior
// knowledge) are routed to the gRPC server; everything else falls through to the
// HTTP/1.1 handler (the gin dashboard/metrics/api/status engine).
//
// # Transport security
//
// The listener is served in cleartext (h2c): the gRPC server uses no transport
// credentials and the client dials with insecure credentials. This is
// deliberate and safe HERE because every warden node reaches its peers over the
// tailnet (WireGuard), which already authenticates and encrypts node-to-node
// traffic — the same trust boundary the HTTP/JSON transport relied on. warden
// terminates no public TLS of its own; the public edge (Caddy) is a separate
// process on a separate host.
//
// # Graceful shutdown
//
// Shutdown drains without dropping in-flight work: it first signals active
// WatchCluster streams to end (so gRPC GracefulStop is not blocked by a
// long-lived stream), lets in-flight unary RPCs finish under a bounded grace
// (hard Stop as a fallback), drains the HTTP server, then closes the mux. Serve
// awaits all three sub-servers so none outlives it.
package grpcmux

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"

	core "github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/grpcserver"
)

const (
	// grpcContentTypeHeader / grpcContentTypeValue are the HTTP/2 header field
	// cmux matches to identify a gRPC connection. gRPC always sends
	// content-type: application/grpc on its request HEADERS frame.
	grpcContentTypeHeader = "content-type"
	grpcContentTypeValue  = "application/grpc"

	// muxMatchTimeout bounds how long cmux waits while sniffing a new
	// connection's opening bytes, so a client that connects but sends nothing
	// cannot wedge a matcher indefinitely.
	muxMatchTimeout = 10 * time.Second

	// httpReadHeaderTimeout preserves the pre-mux server hardening: a slow or
	// absent request header cannot hold a connection open unboundedly.
	httpReadHeaderTimeout = 5 * time.Second
)

// Config wires a Server. All fields are required.
type Config struct {
	// Listener is the single bound port (e.g. from net.Listen on cfg.Bind).
	Listener net.Listener
	// HTTP is the gin engine serving the dashboard, /api/status, and /metrics.
	HTTP http.Handler
	// RPC and Views back the gRPC WardenService (unary handlers and the
	// WatchCluster stream, respectively).
	RPC   warden.IRPCHandler
	Views warden.IViewSource
}

// Server owns the cmux, the gRPC server, and the HTTP server sharing one port.
type Server struct {
	lis     net.Listener
	mux     cmux.CMux
	grpcL   net.Listener
	httpL   net.Listener
	grpcSrv *grpc.Server
	httpSrv *http.Server
	// drainCancel ends active WatchCluster streams at the start of shutdown.
	drainCancel context.CancelFunc
	log         *zerolog.Logger
}

// New builds a Server over cfg.Listener. It does not start serving; call Serve.
func New(cfg Config) *Server {
	drainCtx, drainCancel := context.WithCancel(context.Background())

	svc := grpcserver.New(cfg.RPC, cfg.Views, drainCtx)
	grpcSrv := grpcserver.NewGRPCServer(svc)

	m := cmux.New(cfg.Listener)
	m.SetReadTimeout(muxMatchTimeout)
	// gRPC is matched FIRST (HTTP/2 + application/grpc), using the
	// send-settings matcher so the client is not left waiting on the server's
	// SETTINGS frame; every other connection falls through to HTTP/1.1.
	grpcL := m.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings(grpcContentTypeHeader, grpcContentTypeValue))
	httpL := m.Match(cmux.Any())

	httpSrv := &http.Server{Handler: cfg.HTTP, ReadHeaderTimeout: httpReadHeaderTimeout}

	return &Server{
		lis:         cfg.Listener,
		mux:         m,
		grpcL:       grpcL,
		httpL:       httpL,
		grpcSrv:     grpcSrv,
		httpSrv:     httpSrv,
		drainCancel: drainCancel,
		log:         core.Logger,
	}
}

// Addr reports the bound listener address (useful when the port was :0).
func (s *Server) Addr() net.Addr { return s.lis.Addr() }

// Serve runs the mux and both sub-servers until Shutdown (or a fatal listener
// error). It blocks, and returns only once all three have stopped, so no
// goroutine outlives it. Benign shutdown sentinels are folded to nil.
func (s *Server) Serve() error {
	grpcDone := make(chan error, 1)
	httpDone := make(chan error, 1)
	go func() { grpcDone <- s.grpcSrv.Serve(s.grpcL) }()
	go func() { httpDone <- s.httpSrv.Serve(s.httpL) }()

	muxErr := s.mux.Serve()

	// Shutdown (or a root-listener failure) closes the child listeners, so both
	// sub-server Serve calls return; await them before returning.
	gErr := <-grpcDone
	hErr := <-httpDone
	return firstReal(muxErr, gErr, hErr)
}

// Shutdown gracefully drains the gRPC server, the HTTP server, and the mux,
// bounded by ctx. See the package doc for the ordering rationale.
func (s *Server) Shutdown(ctx context.Context) error {
	// 1. End active WatchCluster streams so GracefulStop is not blocked by a
	//    long-lived stream.
	s.drainCancel()

	// 2. Finish in-flight unary RPCs, bounded by ctx; hard-stop as a fallback so
	//    a stuck handler cannot hold shutdown open past the grace window.
	stopped := make(chan struct{})
	go func() { s.grpcSrv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-ctx.Done():
		s.grpcSrv.Stop()
		<-stopped
	}

	// 3. Drain the HTTP server (bounded by the same ctx).
	httpErr := s.httpSrv.Shutdown(ctx)

	// 4. Stop the mux acceptor and close the root listener; this unblocks Serve.
	s.mux.Close()

	// httpSrv.Shutdown closes httpL, a cmux child listener that shares the one
	// root listener; if the graceful gRPC stop already closed the root, this
	// surfaces a benign net.ErrClosed. Only a real error propagates.
	if httpErr != nil && !isBenignClose(httpErr) {
		return httpErr
	}
	return nil
}

// firstReal returns the first non-benign error among the sub-server results.
// The listeners are torn down by the graceful path, so their "closed" sentinels
// are expected and folded to nil.
func firstReal(errs ...error) error {
	for _, err := range errs {
		if err == nil || isBenignClose(err) {
			continue
		}
		return err
	}
	return nil
}

// isBenignClose reports whether err is an expected shutdown sentinel from cmux,
// grpc, net/http, or the net package (all raised when a listener is closed
// during the graceful path).
func isBenignClose(err error) bool {
	switch {
	case errors.Is(err, cmux.ErrServerClosed),
		errors.Is(err, cmux.ErrListenerClosed),
		errors.Is(err, grpc.ErrServerStopped),
		errors.Is(err, http.ErrServerClosed),
		errors.Is(err, net.ErrClosed):
		return true
	default:
		return false
	}
}
