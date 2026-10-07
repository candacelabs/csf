// Copyright 2026 Candace Labs

package copilotcli_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCopilotCLI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipc/model/copilotcli Suite")
}
