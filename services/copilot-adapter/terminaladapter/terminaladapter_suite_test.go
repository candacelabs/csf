package terminaladapter_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTerminalAdapter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "terminal adapter suite")
}
