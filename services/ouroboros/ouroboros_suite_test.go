// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOuroboros(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/ouroboros Suite")
}
