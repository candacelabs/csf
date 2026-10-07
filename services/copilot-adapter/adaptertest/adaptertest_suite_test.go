// Copyright 2026 Candace Labs

package adaptertest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAdaptertest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "copilot adapter test harness suite")
}
