// Package livetest provides testing helpers for live applications.
//
// It exists as a package separate from live so that production binaries do
// not link testing, and with it flag, regexp, runtime/pprof and
// runtime/trace, merely because they import a UI library. The precedent is
// net/http/httptest and testing/fstest; the separation is asserted by an
// architecture test rather than left as an intention.
//
// Four kinds of helper live here.
//
// Every helper below takes a [testing.TB], and nothing here adapts one. A
// plain go test suite passes its *testing.T. A Ginkgo suite passes
// ginkgo.GinkgoTB(), which Ginkgo ships for exactly this — "a wrapper that
// exactly matches the testing.TB interface … intended to be used as a drop-in
// replacement with third party libraries that accept testing.TB". This package
// therefore imports no spec framework and needs no adapter to avoid importing
// one. It shipped such an adapter briefly, on the false premise that a Ginkgo
// suite could not produce a testing.TB — that is true of GinkgoT() and false
// of GinkgoTB(); see docs/reviews/rulings-review-wave.md, ruling 1.
//
// Construction. NewSession builds the [github.com/candacelabs/csf/pkg/gotth/live.Session]
// a Config hook is called with, so Init, Authorize, Teardown and Execute can be
// driven from a spec without a running server. A Session's fields are
// unexported because identity is bound at the handshake and nothing downstream
// may mint one; that is right for production and leaves a test unable to build
// the one value every hook takes.
//
// Determinism. ReplayN replays an event log against a reducer several times
// and fails unless the resulting state and the emitted effects are identical
// on every run. A reducer that reads a clock, a random source, or the
// iteration order of a map fails it. AssertDirtyComplete catches the dual
// mistake in rendering: a fragment that declared itself unchanged while its
// rendered bytes moved.
//
// End-to-end. NewClient dials an http.Handler and returns a Client driving a
// real session over the real wire protocol, so a spec exercises the same
// handshake, framing and acknowledgement path a browser does. It decodes with
// the library's own generated types and hands back plain values — Frame, Patch,
// Origin, Update, Error — so a spec can assert on the wire without the schema's
// generated Go reaching its import graph. It never acknowledges a patch on its
// own: "this client stopped acknowledging" is the condition every backpressure
// spec is built on, and Ack is explicit for that reason.
//
// Audit. Audit runs a scripted workload and cross-checks every signal the
// library reports about itself against an independent, out-of-process
// measurement, on the principle that a metric only the incrementing code can
// vouch for is not evidence.
//
// # Status
//
// NewSession, ReplayN, AssertDirtyComplete and Client are implemented.
//
// Audit, which cross-checks the library's self-reported signals against an
// out-of-process measurement, is not. Its named consumer is no longer the
// benchmark harness — that landed as Node and CDP and will never call a Go
// client — and no consumer has written its shape yet, so exporting a guess at
// it would be the speculation FR-65 refuses. Client's shape, by contrast, was
// written twice by hand before it was built here; see docs/api-surface.md §6.
package livetest
