package main

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
)

func TestChat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chat Example Suite")
}

// specCountClaim matches the README's "N specs" sentence.
var specCountClaim = regexp.MustCompile(`(\d+) specs\b`)

// The README's spec count, held to the number this suite actually has.
//
// It said 151 while the suite ran 155 (QA-1's Phase 4 grading pass, F-5). The
// number was right when it was written and nothing read it afterwards, which
// is how every stale number in this repository got that way: the next author
// adds a spec, and prose does not notice.
//
// A ReportAfterSuite is the only node handed the suite's own total, and it is
// not itself a spec — so this check cannot move the number it is checking.
// TotalSpecs rather than SpecsThatWillRun, so that a focused run does not
// report the README as wrong.
var _ = ReportAfterSuite("the README's spec count", func(report types.Report) {
	source, err := os.ReadFile("README.md")
	if err != nil {
		Fail("cannot read README.md to check the spec count it publishes: " + err.Error())
		return
	}
	claims := specCountClaim.FindAllStringSubmatch(string(source), -1)
	if len(claims) == 0 {
		Fail(`README.md no longer makes an "N specs" claim, so this check is checking nothing: ` +
			"restore the sentence or delete this node rather than leaving a guard that cannot fail")
		return
	}
	for _, claim := range claims {
		n, err := strconv.Atoi(claim[1])
		if err != nil {
			Fail("README.md quotes an unreadable spec count " + claim[1] + ": " + err.Error())
			return
		}
		if n != report.PreRunStats.TotalSpecs {
			Fail("README.md says " + claim[1] + " specs and this suite has " +
				strconv.Itoa(report.PreRunStats.TotalSpecs) +
				": correct the README, which is the number a reader is asked to trust")
			return
		}
	}
})
