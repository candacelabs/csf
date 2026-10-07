// Copyright 2026 Candace Labs

package copilot_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=copilot.go -destination=mock_workbench_client_test.go -package=copilot_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCopilot(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/model/copilot provider")
}
