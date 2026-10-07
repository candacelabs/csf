// Copyright 2026 Candace Labs

package llamacpp_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLlamacpp(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/model/llamacpp reranker")
}
