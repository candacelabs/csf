package blobfish

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBlobfish(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "examples/widget/candaws/blobfish")
}
