// Copyright 2026 Candace Labs

package evaluate_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEvaluate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/evaluate suite")
}
