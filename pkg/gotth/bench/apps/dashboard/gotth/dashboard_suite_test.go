package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDashboardGotth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bench Dashboard (gotth-live) Suite")
}

// There is deliberately no testing.TB adapter in this file.
//
// livetest.ReplayN and livetest.AssertDirtyComplete take a testing.TB, Ginkgo's
// GinkgoT() is deliberately not one — testing.TB carries an unexported method —
// and every suite in this repository used to carry the same forty-line
// embedded-nil-TB workaround. Ginkgo ships GinkgoTB(), which is a real
// testing.TB, so the call sites below spell it that way and no copy of the
// workaround survives anywhere.
