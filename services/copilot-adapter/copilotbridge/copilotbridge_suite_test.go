package copilotbridge

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCopilotBridge(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "copilot bridge suite")
}
