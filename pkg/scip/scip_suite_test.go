// Copyright 2026 Candace Labs

package scip_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestScip(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "scip")
}
