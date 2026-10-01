package csf

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestCSF(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "CSF semantics")
}
