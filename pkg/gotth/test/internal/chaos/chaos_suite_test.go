// Package chaos holds QA-2's checkpoint-3 chaos suite: the eight minimum cases
// PRD §6 "Phase 3 — Resilience" gates checkpoint 3 on, plus the three
// equivalence-spec Appendix B measurements (QA3-1, QA3-2, QA3-3) that move
// numbers the Phase 5 benchmark publishes.
//
// # What this suite is, and what it deliberately is not
//
// It is the SERVER-AND-WIRE half of resilience. The client runtime's own
// reconnect state machine — RFC §8.4 backoff with full jitter, visibility
// pausing, terminal-versus-retried close codes, and "a reconnect is a new
// session" — is DEV-2's, and client/test/reconnect.test.mjs holds 35 specs for
// it. Re-asserting those here in Go against a hand-written client would be a
// second implementation of the same claim checked against itself. What is here
// instead is everything the browser cannot decide alone: what the SERVER does
// when a socket dies mid-patch, what it does when a process is killed under
// load, whether its queue and its heap stay bounded against a client that will
// not read, whether a half-open connection is ever noticed, and what a replayed
// frame does to state.
//
// # Why it lives under test/internal/
//
// The same reason test/internal/conformance does: internal/arch requires this
// module's non-internal package set to be exactly {live, live/livetest}, and
// `go list ./...` reports a directory of _test.go files as a package. Nesting
// under test/internal/ keeps the arch walk (which skips any path containing
// "/internal/") true while leaving the suite inside `go test ./...`.
//
// # Why it is not a separate module
//
// test/routers, test/sampling and test/memory each carry their own go.mod
// because each needs a dependency the library must not carry — chi and gin, the
// OpenTelemetry SDK, a metric exporter. This suite needs none: every fault it
// injects is built from net, net/http and the WebSocket library the module
// already depends on, and every assertion is over frames the module already
// decodes. A satellite module here would buy separation from nothing and cost
// a second invocation in CI, so the module stays one.
//
// # Cost classes
//
// Specs carrying Label("soak") are skipped unless GOTTHLIVE_SOAK is set: the
// ten-thousand-cycle abrupt-disconnect churn and the thirty-second coalescing
// sweeps are minutes each and do not belong in every `go test ./...`.
//
// Specs carrying Label("measure") are the Appendix B measurements. They are
// measurements rather than pass/fail cases — they assert only the properties
// that must hold for the number to mean anything, and they PRINT the number
// through AddReportEntry. They are skipped unless GOTTHLIVE_MEASURE is set,
// because a measurement taken on a contended host and read as a gate is worse
// than no measurement.
//
// Run everything:
//
//	GOTTHLIVE_SOAK=1 GOTTHLIVE_MEASURE=1 go test ./test/internal/chaos/ -count=1
package chaos_test

import (
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChaos(t *testing.T) {
	RegisterFailHandler(Fail)
	// Eventually's default is one second, which is shorter than several of the
	// bounds this suite asserts and would turn a slow container into a red
	// spec. Every spec that needs a specific bound states it at the call.
	SetDefaultEventuallyTimeout(10 * time.Second)
	SetDefaultEventuallyPollingInterval(20 * time.Millisecond)
	RunSpecs(t, "QA-2 Checkpoint-3 Chaos Suite")
}

// soakOnly skips a soak-class spec unless the environment opts in, matching
// test/internal/conformance's convention so one filter selects both.
func soakOnly() {
	GinkgoHelper()
	if os.Getenv("GOTTHLIVE_SOAK") == "" {
		Skip("soak-class: set GOTTHLIVE_SOAK=1 to run")
	}
}

// measureOnly skips an Appendix-B measurement unless the environment opts in.
//
// It is a skip rather than a cheap version of the same run, and the reason is
// the report: these specs publish numbers into docs/qa/checkpoint-3-chaos.md
// and into the equivalence spec behind it, and a number produced by a shorter
// window than the one the report names is a different number wearing the same
// label. Either the real window runs or nothing runs.
func measureOnly() {
	GinkgoHelper()
	if os.Getenv("GOTTHLIVE_MEASURE") == "" {
		Skip("measurement-class: set GOTTHLIVE_MEASURE=1 to run (Appendix B QA3-1/2/3)")
	}
}
