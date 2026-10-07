// Copyright 2026 Candace Labs

// Package claudecode is the Claude Code provider: a Claude Code session is an
// agent loop in its own process on the operator's machine. The package holds
// that session's agent address and [ClaudeCodeBrain], the model.IBrain that
// drives Claude Code as a turn executor: CSF owns the session, and each turn
// runs one Claude Code process through the ipc/proc capability, forwarding
// every input and every stream-json event unchanged and reporting each one to
// a structured log under the turn's trace.
//
// A Claude Code session reaches CSF as a host-tier agent: it calls CSF's
// per-agent authenticated MCP endpoint over a local socket (a unix socket, or
// loopback), registers its [HostAddress], and fetches and acknowledges its
// inbox through MCP tools.
package claudecode

import (
	"errors"
	"fmt"
	stdnet "net"
	"net/netip"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/io"
	"github.com/candacelabs/csf/io/net/model"
)

// ProviderName names the Claude Code provider in addresses.
const ProviderName = "claudecode"

const (
	// SocketUnix is a unix domain socket, named by its absolute path.
	SocketUnix = "unix"
	// SocketLoopback is a TCP socket on a loopback address.
	SocketLoopback = "tcp"

	unixEndpointPrefix = "unix:"
	// localhostName is the loopback name RFC 6761 reserves. It is accepted by
	// name rather than resolved: a resolver could map it anywhere, and the
	// tier of an address must not depend on one.
	localhostName = "localhost"
)

var (
	// ErrNoSession reports an address built without a session identifier.
	ErrNoSession = errors.New("claudecode address: a nonzero session identifier is required")
	// ErrNotLocal reports a socket another machine could reach, which a
	// host-tier address may not name.
	ErrNotLocal = errors.New("claudecode address: the socket must be a unix socket or a loopback address")
)

// LocalSocket is a socket only processes on this machine can reach.
type LocalSocket struct {
	// Network is [SocketUnix] or [SocketLoopback].
	Network string
	// Address is an absolute path for a unix socket and a loopback
	// host:port for TCP.
	Address string
}

// ParseLocalSocket reads "unix:/absolute/path", "/absolute/path" or a
// loopback "host:port", where the host is a loopback IP or "localhost". Anything another machine could reach is rejected:
// the tier of a Claude Code address is decided by the socket it registers,
// never inferred from where a request happened to arrive.
func ParseLocalSocket(endpoint string) (LocalSocket, error) {
	path := strings.TrimPrefix(endpoint, unixEndpointPrefix)
	if filepath.IsAbs(path) {
		return LocalSocket{Network: SocketUnix, Address: filepath.Clean(path)}, nil
	}
	host, _, err := stdnet.SplitHostPort(endpoint)
	if err != nil {
		return LocalSocket{}, fmt.Errorf("%w: %q: %w", ErrNotLocal, endpoint, err)
	}
	if strings.EqualFold(host, localhostName) {
		return LocalSocket{Network: SocketLoopback, Address: endpoint}, nil
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return LocalSocket{}, fmt.Errorf("%w: %q", ErrNotLocal, endpoint)
	}
	return LocalSocket{Network: SocketLoopback, Address: endpoint}, nil
}

// String is the socket in the form ParseLocalSocket reads.
func (socket LocalSocket) String() string {
	if socket.Network == SocketUnix {
		return unixEndpointPrefix + socket.Address
	}
	return socket.Address
}

// HostAddress is a Claude Code session on this machine: its session
// identifier and the local socket it is reached on.
type HostAddress struct {
	model.IpcTier
	Session uuid.UUID
	Socket  LocalSocket
}

// NewHostAddress addresses session at the local socket endpoint.
func NewHostAddress(session uuid.UUID, endpoint string) (HostAddress, error) {
	if session == uuid.Nil {
		return HostAddress{}, ErrNoSession
	}
	socket, err := ParseLocalSocket(endpoint)
	if err != nil {
		return HostAddress{}, err
	}
	return HostAddress{Session: session, Socket: socket}, nil
}

// Provider is [ProviderName].
func (address HostAddress) Provider() string { return ProviderName }

// Key is claudecode/host/<socket>/<session>.
func (address HostAddress) Key() string {
	return model.AddressKey(ProviderName, io.TierIpc, address.Socket.String(), address.Session.String())
}
