package yakshave

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestYakshave(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "examples/widget/candaws/yakshave")
}
