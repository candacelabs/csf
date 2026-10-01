// Package memory is the G2 idle-connection memory harness: the arithmetic
// half of equivalence-spec §3.6, with the three commands beside it supplying
// the server under test, the synthetic session driver, and the report.
//
// # What this measures
//
//	mem_per_session = ( M(N) − M(0) ) / N
//
// where M(x) is the median of 60 samples taken at 1 Hz over the last 60 s of a
// five-minute steady-state window, and M(x) is the serving container's cgroup
// v2 memory.current minus memory.stat's file, read from OUTSIDE the process.
// equivalence-spec §3.6 is the normative text and this package does not
// restate it; what lives here is the part that must be executable and
// therefore testable — median over an even-length sample set, the subtraction,
// the division, and the refusal to produce a figure from a window that is not
// the window §3.6 specified.
//
// The sampling itself is measure.sh, on the host, because the numbers are
// files under /sys/fs/cgroup that the measured container must not be the one
// reading. Bash reads them; this package turns the CSV into the figure, so the
// only arithmetic in the shell is a loop counter.
//
// # Why this is a SEPARATE module
//
// Same reason as test/routers, examples/counter and examples/chat: a
// requirement in gotth-live/go.mod enters the build list of everybody who
// requires gotth-live, because Go resolves requirements at module
// granularity. The server under test wires the OpenTelemetry metric and trace
// SDKs — equivalence-spec §5.6 puts default-on observability inside the
// headline configuration, so the measured binary has to have a real provider
// rather than a nil one — and the SDKs are needed by nothing else in this
// repository. A benchmark's dependencies must not be a consumer's.
//
// It also leaves internal/arch's two-exported-package cap alone, which lists
// every package under the module path: a package here would be counted as a
// third, and widening that assertion to let a benchmark through would weaken
// the thing it was written to catch.
//
// # Where this module DOES what test/routers refuses to do
//
// test/routers is lexically under gotth-live/ and could therefore import
// gotth-live/internal/..., and deliberately does not: its subject is what a
// consumer can do from outside, so proving it with the library's own private
// codec would prove it with a tool no reader can pick up.
//
// cmd/memdrv does import the internal protobuf types, and the difference is
// which side of the wire it stands on. The driver is not a consumer of the
// library; it stands in for the CLIENT RUNTIME, which is part of the library
// and is written in JavaScript. §3.6 requires the driver to speak "the actual
// protocol — real handshake, real events", and a second, hand-rolled encoder
// of gotthlive.v1.Frame would be a copy of the wire format that could drift
// from the schema while still passing every test in this module. Reading the
// generated types is what makes "real handshake" checkable rather than
// asserted.
//
// cmd/memsrv, by contrast, uses only the exported live API, because it stands
// in for an application and the per-session cost being measured is the one an
// application pays.
package memory
