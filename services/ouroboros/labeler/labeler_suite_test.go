// Copyright 2026 Candace Labs

package labeler_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLabeler(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "services/ouroboros/labeler")
}
