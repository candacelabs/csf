package dashbored

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDashbored(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "examples/widget/candaws/dashbored")
}
