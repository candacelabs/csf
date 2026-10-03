// Copyright 2026 Candace Labs

package proc_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProc(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipc/proc Suite")
}
