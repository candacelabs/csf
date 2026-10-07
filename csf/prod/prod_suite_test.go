// Copyright 2026 Candace Labs

package prod_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "csf/prod Suite")
}
