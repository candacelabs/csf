// Package sampling holds FR-36 clause 4's falsifier and nothing else.
//
// Clause 4 says the server-side event path MUST be exactly one sampling
// decision, and PM-1's ruling (checkpoint-2 gate §5.2) attaches a falsifier to
// it that is a spec rather than a review: over N interactions at any
// 0 < p < 1, the number of PARTIAL server-side graphs must be 0 — each
// interaction records the whole path or none of it.
//
// # Why this is a separate module
//
// A sampling decision is made by an SDK sampler, and this library depends on
// the OpenTelemetry API submodules only. That is an architectural constraint
// and not a preference (instrumentation §3.4, L9-1 D1, dependencies.md §2):
// Config.Tracer takes a provider explicitly so the library never reads the
// OTel global, which is what lets it require go.opentelemetry.io/otel/trace
// instead of the root module. Go resolves module requirements at module
// granularity, so a test-only import of go.opentelemetry.io/otel/sdk in the
// main module's go.mod would put the SDK — and its whole transitive weight —
// into the build list of everybody who requires gotth-live, whether or not
// they ever enable tracing. It would also spend the pre-registered 8-module
// fallback budget instrumentation §3.4 condition 3 fixed in advance.
//
// So this follows test/routers exactly, and for the same reason chi and gin
// live there: its own go.mod, its own build list, its own invocation in
// ci.sh. The measured cost to a consumer is zero, because nothing a consumer
// requires requires this.
//
// # Why the conformance suite could not hold this
//
// internal/obstest is a recording provider, not an SDK. It stamps one
// hard-coded TraceID on every span it records (QA-1 defect D-11), so it cannot
// express a sampling decision at all: nothing in it ever declines to record.
// The conformance suite's trace specs assert the STRUCTURE that makes one
// decision possible — one root per interaction, reached by parent edges alone
// — and that is the strongest thing obstest can say. The rate is this
// module's, against a real ParentBased(TraceIDRatioBased(p)).
//
// # Why it imports the library's internal packages, where test/routers refuses
//
// The opposite call from test/routers, for the opposite reason. That suite's
// subject is what an application OUTSIDE this library can do with it, so
// proving it with the library's private codec would prove it with a tool no
// reader can pick up. This suite's subject is the server's own span graph.
// The frames are a means of provoking one, not the thing under test, so the
// generated types are the honest tool and a hand-rolled protowire reader would
// be ceremony that can itself be wrong.
package sampling
