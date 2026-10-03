// Copyright 2026 Candace Labs

package inproc_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInproc(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "io/inproc suite")
}
