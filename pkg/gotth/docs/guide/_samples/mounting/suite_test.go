package mounting_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMounting(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mounting Suite")
}
