// Package gates_test holds the specs that keep the house gates' CI wiring
// honest: the two workflows under .github/workflows that run the house checker
// (tools/house_lint, through tools/check-house-lint.sh) and the ontology
// alignment score (tools/ontology-score.sh), read from the checkout rather than
// restated here.
//
// A gate whose path filters do not fire on a change to one of its own inputs is
// green by never looking. The house lint therefore has no path filter at all,
// and the ratchet's filters are checked against one representative file per
// input the score reads; every representative must exist, so the list cannot
// rot into naming files that are gone.
package gates_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "House gates")
}
