// Copyright 2026 Candace Labs

package costs_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCosts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/harness/costs Suite")
}
