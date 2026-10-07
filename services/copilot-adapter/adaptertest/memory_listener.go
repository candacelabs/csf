// Copyright 2026 Candace Labs

package adaptertest

import (
	"context"
	stdnet "net"
	"sync"
)

// memoryListener is the in-memory network the adapter's specs serve over: a
// listener whose every connection is a net.Pipe pair, dialed through
// DialContext and accepted through Accept.
//
// It is net.Pipe rather than gRPC's bufconn because net/http depends on one
// property of its connection that bufconn does not keep. When a handler
// returns, net/http expires the connection's read deadline to stop its
// background read and then clears the deadline. bufconn's expired timer can
// still be running when the deadline is cleared, and it then marks the
// connection timed out with no deadline set. The next background read returns
// that timeout at once, net/http takes it for a closed connection and cancels
// the context of a request that is still being served, so a store call in the
// handler fails with "context canceled". net.Pipe settles a cleared deadline
// exactly.
type memoryListener struct {
	connections chan stdnet.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newMemoryListener() *memoryListener {
	return &memoryListener{connections: make(chan stdnet.Conn), closed: make(chan struct{})}
}

// Accept blocks until a dial hands over the server half of a connection, or
// the listener closes.
func (listener *memoryListener) Accept() (stdnet.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, stdnet.ErrClosed
	}
}

// Close stops the listener; closing it again does nothing.
func (listener *memoryListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

// Addr reports the listener's address, which no resolver ever sees.
func (listener *memoryListener) Addr() stdnet.Addr { return memoryAddress{} }

// DialContext opens a connection and waits for Accept to take its server half.
// It returns early, with both halves closed, when ctx ends or the listener
// closes first.
func (listener *memoryListener) DialContext(ctx context.Context) (stdnet.Conn, error) {
	server, client := stdnet.Pipe()
	var refusal error
	select {
	case listener.connections <- server:
		return client, nil
	case <-ctx.Done():
		refusal = ctx.Err()
	case <-listener.closed:
		refusal = stdnet.ErrClosed
	}
	_ = server.Close()
	_ = client.Close()
	return nil, refusal
}

// memoryNetworkName names the address of a listener that has none.
const memoryNetworkName = "memory"

// memoryAddress is the address of a listener that has none.
type memoryAddress struct{}

func (memoryAddress) Network() string { return memoryNetworkName }
func (memoryAddress) String() string  { return memoryNetworkName }
