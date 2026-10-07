// Copyright 2026 Candace Labs

package progress_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProgress(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/harness/progress Suite")
}
