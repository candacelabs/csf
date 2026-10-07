// Copyright 2026 Candace Labs

package ollama_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOllama(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/model/ollama local model provider")
}
