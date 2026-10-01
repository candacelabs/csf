package keychords_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestKeychords(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Keychords Suite")
}
