package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChatGotth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bench Chat (gotth-live) Suite")
}

// There is deliberately no testing.TB adapter in this file.
//
// livetest.ReplayN and livetest.AssertDirtyComplete take a testing.TB and
// Ginkgo's GinkgoT() is deliberately not one — testing.TB carries an unexported
// method, so nothing outside package testing can implement it. Ginkgo ships
// GinkgoTB() for exactly this, so the call sites below spell it that way and no
// suite in this repository hand-rolls the embedded-nil-TB workaround.
