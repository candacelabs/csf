// Package wsx is the WebSocket transport, and the only place it exists.
//
// This package is the sole importer of the WebSocket library in this module.
// Nothing in the reducer, render, protocol, or provenance path may reference
// it, directly or transitively; an architecture test asserts that, and the
// assertion is what a second transport would be built against. The core talks
// to a connection through channels and a framer function value, not through
// an interface with one implementation.
//
// # Establishment
//
// The order of the handshake is the security property, not an implementation
// detail: origin allowlist, then authentication against the HTTP request,
// then the CSRF token, then subprotocol negotiation, and only then the
// upgrade. No per-session memory is allocated before authentication succeeds,
// so a rejected origin costs an HTTP response and nothing else.
//
// # Lifetime
//
// ServeHTTP RETURNS at the upgrade, and the session runs on a goroutine this
// package owns. The obvious shape — serving the session inline, so the handler
// returns when the connection ends — keeps net/http's whole per-request working
// set alive for the life of the session: a *conn[I] with two 4 KiB bufio buffers,
// a *response with a third, and the *Request with its header map, none of which
// return to net/http's pools because hijacking skips the call that returns
// them. Returning is what lets net/http collect them, and it is what lets this
// package hand the transport buffers it sized itself (hijack.go). Two
// consequences: the session runs under context.WithoutCancel, because the
// request context is cancelled the moment ServeHTTP returns; and net/http's
// recover is no longer behind the read pump, so the session's teardown carries
// its own. The goroutine COUNT is unchanged at two per session — this one is
// the read pump, and net/http's has gone home.
//
// # Reading
//
// The inbound size cap is applied to the connection before any payload is
// allocated, so an oversize frame is refused rather than buffered. The read
// pump parses, hands events to the single mailbox ingress, and never blocks
// on a full channel: a flood is dropped with a typed error, because blocking
// the read pump would stall the connection's own liveness detection.
//
// # Liveness and closing
//
// Liveness is the protocol's own heartbeat frame, not an RFC 6455 ping; this
// package never initiates ping or pong. Every close names a code from the
// protocol's enumeration, and a close without one is a defect — a test walks
// every call site to prove it.
//
// # Status
//
// Implemented: the ordered handshake, the read pump, the write path, session
// limits, and draining.
package wsx
