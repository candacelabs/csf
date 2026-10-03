// Copyright 2026 Candace Labs

package sessiongate_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSessionGate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/harness/sessiongate Suite")
}
