// Copyright 2026 Candace Labs

package codes_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCodes(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "Failure codes")
}
