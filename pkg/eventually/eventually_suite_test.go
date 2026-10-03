package eventually_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEventually(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/eventually suite")
}

// This suite is a black-box test — package eventually_test, not package eventually —
// so it can dot-import both ginkgo and gomega the way every other suite in the
// module does (CS-11). An in-package test could not: eventually declares
// Consistently itself, and dot-importing gomega, which also exports
// Consistently, would be a redeclaration. Reaching interval, the one unexported
// thing a spec needs, goes through export_test.go's BudgetInterval.

// recordingReporter is a eventually.IReporter that records instead of aborting.
//
// This is the reason IReporter exists rather than the signatures taking
// testing.TB: testing.TB cannot be implemented outside the standard library,
// and a real *testing.T aborts the spec on the first Fatalf, so the failing
// half of this package — every message a reader will ever actually see —
// would have been untestable.
type recordingReporter struct {
	helped   int
	failures []string
}

func (reporter *recordingReporter) Helper() { reporter.helped++ }

func (reporter *recordingReporter) Fatalf(format string, arguments ...any) {
	reporter.failures = append(reporter.failures, fmt.Sprintf(format, arguments...))
}

// failed is the whole recorded failure text, which is what the specifications
// assert against: a message is only useful if the thing a reader needs is in
// it, wherever the engine chose to put it.
func (reporter *recordingReporter) failed() string {
	return fmt.Sprint(reporter.failures)
}
