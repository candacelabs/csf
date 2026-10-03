// Copyright 2026 Candace Labs

package housekeeping_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHousekeeping(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Housekeeping Service Suite")
}
