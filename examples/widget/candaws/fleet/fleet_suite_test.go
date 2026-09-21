package fleet

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "examples/widget/candaws/fleet")
}
