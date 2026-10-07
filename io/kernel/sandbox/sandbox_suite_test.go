// Copyright 2026 Candace Labs

package sandbox

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSandbox(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipc/sandbox")
}
