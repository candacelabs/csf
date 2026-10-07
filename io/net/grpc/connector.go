// Copyright 2026 Candace Labs

// Package grpc is the gRPC client-connection capability. A service that calls
// a remote gRPC API receives an [IClientConnector] in its constructor and asks
// it for connections; it never calls grpc.NewClient itself. Every connection
// the connector opens dials through the socket capability it was built on,
// which is what keeps the boundary crossing in candace/ipc and lets a test
// substitute an in-memory transport.
package grpc

import (
	"context"
	"errors"
	"fmt"
	stdnet "net"

	upstream "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ionet "github.com/candacelabs/csf/io/net"
)

const tcp = "tcp"

// IClientConnector opens gRPC client connections. The returned connection is
// lazy, as grpc.NewClient's is: it dials on the first RPC, and the caller
// closes it.
type IClientConnector interface {
	NewClient(target string) (*upstream.ClientConn, error)
}

// ClientConnector opens gRPC clients through a socket capability.
type ClientConnector struct {
	dialer  ionet.IDialer
	options []upstream.DialOption
}

// NewPlaintextClientConnector returns a connector for peers reached over an
// already-encrypted private network, such as a WireGuard tailnet, where the
// transport itself carries no TLS. That is the trust model of the network it
// is granted for, not a default for the public internet.
func NewPlaintextClientConnector(dialer ionet.IDialer, options ...upstream.DialOption) (*ClientConnector, error) {
	if dialer == nil {
		return nil, errors.New("ipc/net/grpc: a dialer capability is required")
	}
	connector := &ClientConnector{dialer: dialer}
	connector.options = append([]upstream.DialOption{
		upstream.WithTransportCredentials(insecure.NewCredentials()),
		upstream.WithContextDialer(connector.dial),
	}, options...)
	return connector, nil
}

func (connector *ClientConnector) dial(ctx context.Context, address string) (stdnet.Conn, error) {
	return connector.dialer.DialContext(ctx, tcp, address)
}

// NewClient opens a lazy client connection to target.
func (connector *ClientConnector) NewClient(target string) (*upstream.ClientConn, error) {
	connection, err := upstream.NewClient(target, connector.options...)
	if err != nil {
		return nil, fmt.Errorf("ipc/net/grpc: open client for %s: %w", target, err)
	}
	return connection, nil
}
