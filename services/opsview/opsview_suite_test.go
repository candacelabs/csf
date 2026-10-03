// Copyright 2026 Candace Labs

package opsview_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOpsView(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/opsview Suite")
}
