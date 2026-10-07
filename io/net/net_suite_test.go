// Copyright 2026 Candace Labs

package net_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipc/net Suite")
}
