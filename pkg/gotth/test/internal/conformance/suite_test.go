// Package conformance holds QA-1's independent checkpoint-1 verification.
//
// It is deliberately not a re-run of the developers' own suites. Every spec
// here was written against the specification documents — docs/PRD.md §6
// Phase 1, docs/protocol.md §6 (the H invariants) and §7 (the P provenance
// properties) — and then pointed at the implementation, rather than derived
// from the implementation and pointed at itself. Where a spec duplicates one
// that already exists in internal/, it does so on purpose: an independent
// second implementation of the same assertion is the point, because a property
// checked only by the code that implements it is checked by nobody.
//
// # Why this lives under test/internal/
//
// PRD delivery reserves gotth-live/test/ for QA. The C-20 assertion in
// internal/arch requires the module's *non-internal* package set to be exactly
// {live, live/livetest}, and `go list ./...` reports a directory holding only
// _test.go files as a package, so a suite at gotth-live/test/ fails that
// assertion on arrival. Nesting under test/internal/ satisfies both: the
// arch walk skips any path containing "/internal/", the suite still runs
// under a plain `go test ./...`, and a QA conformance suite is in any case not
// part of the module's exported surface. The collision is reported as a
// finding in docs/qa/checkpoint-1.md rather than fixed here, because
// internal/arch is DEV-1's file.
//
// # Soak-class specs
//
// Specs carrying Label("soak") are skipped unless GOTTHLIVE_SOAK is set, so
// `go test ./...` stays fast by default. Run them with:
//
//	GOTTHLIVE_SOAK=1 go test ./test/... -args -ginkgo.label-filter=soak
package conformance_test

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConformance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "QA-1 Checkpoint-1 Conformance Suite")
}

// soakOnly skips a soak-class spec unless the environment opts in. The label
// is carried as well, so the suite can also be selected by label filter.
func soakOnly() {
	GinkgoHelper()
	if os.Getenv("GOTTHLIVE_SOAK") == "" {
		Skip("soak-class: set GOTTHLIVE_SOAK=1 to run")
	}
}
