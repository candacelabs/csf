package opencode

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOpenCode(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS OpenCode Harness Suite")
}

// quiescence is the window a Consistently uses to assert that something the
// runtime must never do has not happened. It is several poll intervals of the
// suite's test configuration, so a reconciliation that would have produced the
// event has had room to run.
const quiescence = 120 * time.Millisecond
