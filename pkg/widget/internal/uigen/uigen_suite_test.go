package uigen_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUigen(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Uigen Suite")
}
