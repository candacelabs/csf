package discovery

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The discovery package has no contract suite of its own, so this file provides
// the single Ginkgo bootstrap that runs every spec registered across the
// package's test files.
func TestDiscovery(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "discovery suite")
}
