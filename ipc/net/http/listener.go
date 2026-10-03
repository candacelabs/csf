// Copyright 2026 Candace Labs

// Package http is the HTTP capability: one server bound to one address,
// mounted into the process runtime as a service, and the outbound client
// ([NewHTTPClient]) whose every connection is dialed through a granted socket
// capability.
//
// A binary builds the handler (a Gin engine with every service's routes
// registered on it), grants a socket capability, and mounts the resulting
// [HTTPListener] last, so the runtime's reverse shutdown stops accepting
// requests before it stops the services those requests use. The server shape
// and the graceful drain are candace/pkg/httpserver's: this package adds the
// capability boundary and the runtime scope, not a second lifecycle.
package http

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	stdnet "net"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	ipcnet "github.com/candacelabs/csf/ipc/net"
	"github.com/candacelabs/csf/pkg/httpserver"
	"github.com/candacelabs/csf/runtime"
)

const (
	tcp  = "tcp"
	unix = "unix"
)

// DefaultShutdownBudget bounds the drain of in-flight requests when a
// listener is given no budget of its own.
const DefaultShutdownBudget = 10 * time.Second

// staleSocketProbe bounds the dial that tells a stale unix socket from a live
// one.
const staleSocketProbe = time.Second

// ListenerOption configures an [HTTPListener].
type ListenerOption func(options *listenerOptions)

type listenerOptions struct {
	shutdownBudget time.Duration
}

// WithShutdownBudget bounds how long shutdown waits for in-flight requests
// before closing them. A binary sets it so its whole stop sequence fits its
// supervisor's grace period.
func WithShutdownBudget(budget time.Duration) ListenerOption {
	return func(options *listenerOptions) { options.shutdownBudget = budget }
}

func configure(options []ListenerOption) (listenerOptions, error) {
	configured := listenerOptions{shutdownBudget: DefaultShutdownBudget}
	for _, option := range options {
		if option != nil {
			option(&configured)
		}
	}
	if configured.shutdownBudget <= 0 {
		return listenerOptions{}, fmt.Errorf("ipc/net/http: shutdown budget must be positive, got %s", configured.shutdownBudget)
	}
	return configured, nil
}

// HTTPListener serves one handler on one address.
type HTTPListener struct {
	network        ipcnet.IListener
	kind           string
	address        string
	server         *stdhttp.Server
	listener       stdnet.Listener
	shutdownBudget time.Duration
}

// NewHTTPListener prepares a streaming-safe server for handler on address,
// opened through network. Nothing is bound until the runtime starts it.
func NewHTTPListener(network ipcnet.IListener, address string, handler stdhttp.Handler, options ...ListenerOption) (*HTTPListener, error) {
	configured, err := configure(options)
	if err != nil {
		return nil, err
	}
	if network == nil {
		return nil, errors.New("ipc/net/http: a network capability is required")
	}
	if handler == nil {
		return nil, errors.New("ipc/net/http: a handler is required")
	}
	if _, _, err := stdnet.SplitHostPort(address); err != nil {
		return nil, fmt.Errorf("ipc/net/http: address %q: %w", address, err)
	}
	return &HTTPListener{
		network:        network,
		kind:           tcp,
		address:        address,
		server:         httpserver.NewStreamingServer(address, handler),
		shutdownBudget: configured.shutdownBudget,
	}, nil
}

// NewUnixSocketHTTPListener prepares the same server on the unix domain socket
// at path: the host tier's listener, reachable only by processes on this
// machine that can open the path. The caller owns the path's directory and
// permissions; the socket file is removed when the listener closes.
func NewUnixSocketHTTPListener(network ipcnet.IListener, path string, handler stdhttp.Handler, options ...ListenerOption) (*HTTPListener, error) {
	configured, err := configure(options)
	if err != nil {
		return nil, err
	}
	if network == nil {
		return nil, errors.New("ipc/net/http: a network capability is required")
	}
	if handler == nil {
		return nil, errors.New("ipc/net/http: a handler is required")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("ipc/net/http: unix socket path %q must be absolute", path)
	}
	return &HTTPListener{
		network:        network,
		kind:           unix,
		address:        path,
		server:         httpserver.NewStreamingServer(path, handler),
		shutdownBudget: configured.shutdownBudget,
	}, nil
}

// Start binds the socket synchronously — so a busy port fails startup rather
// than a goroutine later — and then serves it from the scope until the scope
// is canceled, when it drains in-flight requests within the shared budget.
func (listener *HTTPListener) Start(scope *runtime.Scope) error {
	if listener.listener != nil {
		return fmt.Errorf("ipc/net/http: %s is already started", listener.address)
	}
	if listener.kind == unix {
		if err := removeStaleSocket(scope.Context(), listener.address); err != nil {
			return err
		}
	}
	bound, err := listener.network.Listen(scope.Context(), listener.kind, listener.address)
	if err != nil {
		return err
	}
	restore := httpserver.BindLifecycle(scope.Context(), listener.server)
	// The drain is triggered by the scope's cancellation through
	// context.AfterFunc rather than by a second scope goroutine, so the
	// serving goroutine is the only one Start asks for: if the scope refuses
	// it, nothing has started that could wait on it, and the socket is
	// released here.
	drained := make(chan error, 1)
	stopDrain := context.AfterFunc(scope.Context(), func() {
		drained <- httpserver.ShutdownWithin(listener.server, listener.shutdownBudget)
	})
	if err := scope.GoOwner("serve "+listener.address, func(_ context.Context) error {
		served := listener.server.Serve(bound)
		if stopDrain() {
			// Serve failed on its own, before any shutdown began; Serve has
			// already closed the socket.
			restore()
			return served
		}
		drainErr := <-drained
		restore()
		if errors.Is(served, stdhttp.ErrServerClosed) {
			return drainErr
		}
		return errors.Join(served, drainErr)
	}); err != nil {
		stopDrain()
		_ = bound.Close()
		restore()
		return err
	}
	listener.listener = bound
	return nil
}

// removeStaleSocket deletes the unix socket at path only when it is a socket
// that nothing answers: the file a crashed process leaves behind, which would
// otherwise fail every restart with EADDRINUSE. A live socket, a path that is
// not a socket, or a probe that fails for any reason other than a refused
// connection is left for Listen to report.
func removeStaleSocket(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ipc/net/http: inspect unix socket %s: %w", path, err)
	}
	if info.Mode().Type() != fs.ModeSocket {
		return nil
	}
	probeContext, cancel := context.WithTimeout(ctx, staleSocketProbe)
	defer cancel()
	var dialer stdnet.Dialer
	connection, err := dialer.DialContext(probeContext, unix, path)
	if err == nil {
		return connection.Close()
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("ipc/net/http: remove stale unix socket %s: %w", path, err)
	}
	return nil
}

// Addr is the bound address once the listener has started, which is how a
// caller that asked for port 0 learns the port. It is nil before Start.
func (listener *HTTPListener) Addr() stdnet.Addr {
	if listener.listener == nil {
		return nil
	}
	return listener.listener.Addr()
}
