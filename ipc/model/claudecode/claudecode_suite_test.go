// Copyright 2026 Candace Labs

package claudecode_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClaudeCode(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipc/model/claudecode Suite")
}
