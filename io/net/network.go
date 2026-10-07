// Copyright 2026 Candace Labs

// Package net is the socket capability: the kernel I/O tier's network
// boundary. Opening a listener or dialing a connection crosses out of the
// process's shared memory into the kernel, so it happens only through a
// capability granted by the binary that owns the process.
//
// A binary constructs one [HostNetwork] and passes it, as an [IListener] or an
// [IDialer], to whatever needs it; a service receives the capability in its
// constructor and never calls net.Listen or net.Dial itself. The narrower
// protocol capabilities, candace/ipc/net/http and candace/ipc/net/grpc, are
// built on these two interfaces, which is also what lets a test replace the
// kernel with an in-memory pipe.
package net

import (
	"context"
	"fmt"
	stdnet "net"
)

// IListener opens listening sockets.
type IListener interface {
	Listen(ctx context.Context, network string, address string) (stdnet.Listener, error)
}

// IDialer opens outbound connections.
type IDialer interface {
	DialContext(ctx context.Context, network string, address string) (stdnet.Conn, error)
}

// HostNetwork is this host's kernel network stack, granted as a capability.
type HostNetwork struct {
	listen stdnet.ListenConfig
	dial   stdnet.Dialer
}

// NewHostNetwork returns the host's network capability with the standard
// library's defaults.
func NewHostNetwork() *HostNetwork {
	return &HostNetwork{}
}

// Listen opens a listener on address.
func (network *HostNetwork) Listen(ctx context.Context, kind string, address string) (stdnet.Listener, error) {
	listener, err := network.listen.Listen(ctx, kind, address)
	if err != nil {
		return nil, fmt.Errorf("ipc/net: listen %s %s: %w", kind, address, err)
	}
	return listener, nil
}

// DialContext opens a connection to address.
func (network *HostNetwork) DialContext(ctx context.Context, kind string, address string) (stdnet.Conn, error) {
	connection, err := network.dial.DialContext(ctx, kind, address)
	if err != nil {
		return nil, fmt.Errorf("ipc/net: dial %s %s: %w", kind, address, err)
	}
	return connection, nil
}
