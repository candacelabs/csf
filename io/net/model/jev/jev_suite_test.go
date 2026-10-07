// Copyright 2026 Candace Labs

package jev_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJev(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/model/jev typed decisions")
}
