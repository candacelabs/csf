package wsx

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
)

// The two buffer sizes this library hands the transport, in place of the ones
// net/http sizes for parsing and writing HTTP responses.
//
// `websocket.Accept` takes the connection through `http.Hijacker`, and net/http
// hands over its OWN `*bufio.Reader` and `*bufio.Writer` — 4,096 B each
// (`net/http/server.go`: `newBufioReader`, `newBufioWriterSize(…, 4<<10)`),
// sized for parsing request headers and writing response headers. The transport
// then retains both for the connection's life (`conn.br`, `conn.bw`). They are
// two of the three largest lines in the per-component heap profile of
// docs/bench/g2-baseline.md §7.5, and §6.2 of RFC-0001 has a line for neither
// the writer nor a reader this large.
//
// # Why these sizes and what the smaller ones cost
//
// A `bufio.Reader` costs nothing in syscalls for a large read: `Read` with
// `len(p) >= len(buf)` and an empty buffer reads STRAIGHT into p, bypassing the
// buffer entirely (`bufio/bufio.go`, the "Large read, empty buffer" branch). The
// buffer's only job is batching small reads, and every inbound frame in this
// protocol is small: `Event`, `Ack`, `Heartbeat`, `ResyncRequest`
// (protocol.md §3). 512 B holds a frame header and several of them.
//
// A `bufio.Writer` of size B costs exactly ONE extra `write(2)` per frame whose
// payload is in `(B−10, 4086]`, and never more than that. The transport writes
// the ≤10-byte server frame header into the buffer and then the payload, and
// flushes on the final frame (`coder/websocket/write.go: writeFrame`), so the
// payload never meets an empty buffer and never takes bufio's direct-write fast
// path; `header+payload ≤ B` is one syscall and anything larger is two, at any
// B. Outbound payloads are rendered fragments, so the band that pays is real.
//
// That cost is MEASURED, by BenchmarkFrameWrite in this package, over a
// loopback TCP socket on the contended host of docs/bench/g2-baseline.md §3
// (medians of three, 200k frames each):
//
//	payload   1,024 B buffer   4,096 B buffer   difference
//	     64        2,100 ns         2,041 ns    none — one syscall either way
//	    512        2,126 ns         1,977 ns    none — one syscall either way
//	  2,048        4,233 ns         2,433 ns    +1,800 ns — the extra write
//	  4,000        4,746 ns         2,253 ns    +2,493 ns — the extra write
//	  8,192        6,153 ns         5,625 ns    none — two syscalls either way
//
// So the price is ≈2 µs on a frame whose payload falls between 1,014 and 4,086
// bytes, and nothing at all outside that band. PRD G1 budgets event→paint at
// 50 ms p50; the nearest measured figure in this repository is checkpoint 1's
// 3.20 ms p50 on loopback. 2 µs is 0.06 % of that measurement and 0.004 % of
// the budget, against 3,072 B per session of live heap — which the `GOGC=100`
// headroom doubles.
//
// Neither number is a benchmark tuning: they are the same for every workload,
// they are not reachable from configuration, and the idle-memory measurement
// they were chosen against (docs/bench/g2-baseline.md) uses an application whose
// frames are far below both.
const (
	readBufferBytes  = 512
	writeBufferBytes = 1024
)

// rightSized wraps w so that the transport's `http.Hijacker` call yields
// buffers this library sized rather than the ones net/http sized.
//
// It is `websocket.Accept`'s hijacker that is being intercepted, and the
// interception is legitimate rather than clever: `hijacker()` in
// coder/websocket walks `Unwrap` but tests `http.Hijacker` FIRST, so a wrapper
// that implements `Hijack` is the documented way to supply one.
//
// The wrapper lives exactly as long as the `Accept` call. Nothing else in this
// library or any application ever sees it.
func rightSized(w http.ResponseWriter) http.ResponseWriter {
	hj, ok := hijackerOf(w)
	if !ok {
		// Not hijackable: hand it back untouched and let Accept produce its own
		// diagnostic. Wrapping it would only change which error the caller sees.
		return w
	}
	return sizedHijacker{ResponseWriter: w, hijacker: hj}
}

// hijackerOf finds the ResponseWriter's Hijacker, FOLLOWING `Unwrap`.
//
// This is C-36, and the finding was that the wrapper knew how to walk `Unwrap`
// — it implements `Unwrap` for exactly that reason — and did not use it. Since
// Go 1.20 the documented way to write ResponseWriter middleware is to implement
// `Unwrap() http.ResponseWriter` and let `http.ResponseController` find
// capabilities, and such wrappers routinely do NOT implement `Hijack`. A direct
// `w.(http.Hijacker)` test therefore declined behind ordinary middleware, the
// original writer went back, coder/websocket's own walk found net/http's
// hijacker past us, and the session paid 4,096 + 4,096 for its life instead of
// 512 + 1,024. L9-1 measured exactly that: **6,656 B per session, lost
// silently** — no log, no metric, no failing spec.
//
// The walk is the same one `http.ResponseController` and coder/websocket's
// `hijacker()` perform, and it tests `http.Hijacker` BEFORE `Unwrap` at each
// level, so a middleware that implements its own `Hijack` still wins — which is
// the behaviour it is entitled to.
func hijackerOf(w http.ResponseWriter) (http.Hijacker, bool) {
	for {
		switch t := w.(type) {
		case http.Hijacker:
			return t, true
		case interface{ Unwrap() http.ResponseWriter }:
			w = t.Unwrap()
		default:
			return nil, false
		}
	}
}

// writeHeaderNowOf finds a gin-shaped `WriteHeaderNow` through the same walk,
// for the same reason: a gin writer behind an `Unwrap`-only wrapper is still a
// gin writer, and its 101 still has to be flushed before the hijack.
func writeHeaderNowOf(w http.ResponseWriter) (interface{ WriteHeaderNow() }, bool) {
	for {
		switch t := w.(type) {
		case interface{ WriteHeaderNow() }:
			return t, true
		case interface{ Unwrap() http.ResponseWriter }:
			w = t.Unwrap()
		default:
			return nil, false
		}
	}
}

// sizedHijacker is the wrapper. It forwards everything and replaces only the
// buffers `Hijack` returns.
type sizedHijacker struct {
	http.ResponseWriter
	// hijacker is the Hijacker found by walking Unwrap, which is not
	// necessarily ResponseWriter itself (C-36).
	hijacker http.Hijacker
}

// Unwrap is what `http.ResponseController` and coder/websocket's `hijacker()`
// follow. It is here so that wrapping cannot hide a capability from anything
// that looks for one; `Hijack` is still ours, because both walkers test
// `http.Hijacker` before they test `Unwrap`.
func (s sizedHijacker) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// WriteHeaderNow forwards gin's out-of-band flush.
//
// coder/websocket type-asserts this method on the ResponseWriter it was given
// and calls it after `WriteHeader(101)`, because gin's ResponseWriter only
// RECORDS a status until something flushes it — and gin's own `Hijack` does not
// (`gin/response_writer.go`). Without this forward, mounting under gin would
// hijack a connection whose 101 had never been written. It is a no-op on a
// ResponseWriter that has no such method, which is what net/http and chi are.
func (s sizedHijacker) WriteHeaderNow() {
	if n, ok := writeHeaderNowOf(s.ResponseWriter); ok {
		n.WriteHeaderNow()
	}
}

// Hijack takes net/http's buffers and hands back this library's.
//
// The bytes net/http had already buffered are carried across explicitly. The
// transport, immediately after this returns, does
//
//	b, _ := brw.Reader.Peek(brw.Reader.Buffered())
//	brw.Reader.Reset(io.MultiReader(bytes.NewReader(b), netConn))
//
// which reads whatever the reader has BUFFERED — not whatever its source has
// left. A fresh reader over the carried bytes would report zero buffered and the
// transport would reset that source away, silently dropping any frame a client
// pipelined behind its upgrade request. So the replacement reader is PRIMED
// with a `Peek` before it is handed over, and the transport then finds exactly
// the bytes net/http had.
//
// If the pipelined bytes do not fit the smaller buffer, net/http's own buffers
// are returned unchanged. Correctness is never traded for the memory.
func (s sizedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	netConn, brw, err := s.hijacker.Hijack()
	if err != nil {
		return netConn, brw, err
	}

	buffered := brw.Reader.Buffered()
	if buffered > readBufferBytes {
		return netConn, brw, nil
	}

	var src io.Reader = netConn
	if buffered > 0 {
		carried, peekErr := brw.Reader.Peek(buffered)
		if peekErr != nil {
			return netConn, brw, nil
		}
		// Peek's slice aliases the buffer being abandoned, so it is copied.
		pipelined := make([]byte, buffered)
		copy(pipelined, carried)
		src = io.MultiReader(bytes.NewReader(pipelined), netConn)
	}

	br := bufio.NewReaderSize(src, readBufferBytes)
	if buffered > 0 {
		// Prime, so that Buffered() reports what the transport is about to ask
		// for. The first Read on src returns the carried bytes and no more, so
		// this cannot block on the network.
		if _, peekErr := br.Peek(buffered); peekErr != nil {
			return netConn, brw, nil
		}
	}

	return netConn, bufio.NewReadWriter(br, bufio.NewWriterSize(netConn, writeBufferBytes)), nil
}
