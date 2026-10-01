package ifacereturn_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestIfaceReturn is the RunSpecs bootstrap, and the only standard-library
// TestXxx function this package is allowed: candace/pkg/scripts/check-test-style.sh
// counts `func TestXxx(t *testing.T)` declarations against `RunSpecs(t,` calls
// per file and fails when they differ, and as of 2026-09-02 that walk covers
// candace/tools as well as candace/pkg.
func TestIfaceReturn(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "tools/ifacereturn suite")
}
