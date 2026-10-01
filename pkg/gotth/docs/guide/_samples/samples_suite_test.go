package samples_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSamples(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Documentation Samples Suite")
}
