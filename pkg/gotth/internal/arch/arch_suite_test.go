package arch_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestArchitecture(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Architecture Suite")
}
