// Copyright 2026 Candace Labs

package http

import (
	"context"
	"errors"
	"fmt"
	stdnet "net"
	stdhttp "net/http"
	"time"

	ipcnet "github.com/candacelabs/csf/ipc/net"
)

// DefaultClientTimeout bounds one whole request, response body included,
// when a client is given no timeout of its own.
const DefaultClientTimeout = 30 * time.Second

// IHTTPClient sends HTTP requests: the outbound capability a service that
// calls a remote HTTP API takes in its constructor. The client
// [NewHTTPClient] returns satisfies it, and so does a test's double.
type IHTTPClient interface {
	Do(request *stdhttp.Request) (*stdhttp.Response, error)
}

var _ IHTTPClient = (*stdhttp.Client)(nil)

// ClientOption configures the client [NewHTTPClient] builds.
type ClientOption func(options *clientOptions)

type clientOptions struct {
	timeout time.Duration
}

// WithClientTimeout bounds one whole request, response body included.
func WithClientTimeout(timeout time.Duration) ClientOption {
	return func(options *clientOptions) { options.timeout = timeout }
}

// NewHTTPClient returns an HTTP client whose every connection is dialed
// through dialer: the outbound half of this capability. A service that calls
// a remote HTTP API receives the client (or the dialer it is built from) in
// its constructor and never builds a transport that dials on its own.
//
// The transport is the standard library's default shape — keep-alive
// pooling, HTTP/2 negotiated over TLS, the system root certificates — with
// one deliberate difference: it never consults HTTP_PROXY or HTTPS_PROXY,
// because the process environment is read only by candace/runtime/config. A
// binary that needs a proxy grants a dialer that reaches it.
func NewHTTPClient(dialer ipcnet.IDialer, options ...ClientOption) (*stdhttp.Client, error) {
	if dialer == nil {
		return nil, errors.New("ipc/net/http: a dialer capability is required")
	}
	configured := clientOptions{timeout: DefaultClientTimeout}
	for _, option := range options {
		if option != nil {
			option(&configured)
		}
	}
	if configured.timeout <= 0 {
		return nil, fmt.Errorf("ipc/net/http: client timeout must be positive, got %s", configured.timeout)
	}
	defaults, ok := stdhttp.DefaultTransport.(*stdhttp.Transport)
	if !ok {
		return nil, errors.New("ipc/net/http: the default transport has been replaced; cannot derive a client transport")
	}
	transport := defaults.Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network string, address string) (stdnet.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}
	return &stdhttp.Client{Transport: transport, Timeout: configured.timeout}, nil
}
