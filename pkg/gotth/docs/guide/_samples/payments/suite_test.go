package payments_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPayments(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Payments Suite")
}
