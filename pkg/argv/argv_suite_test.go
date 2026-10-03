// Copyright 2026 Candace Labs

package argv_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestArgv(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/argv Suite")
}
