package boundedbuffer_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBoundedBuffer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bounded Buffer Suite")
}
