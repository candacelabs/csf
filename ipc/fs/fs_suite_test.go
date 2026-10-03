// Copyright 2026 Candace Labs

package fs_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipc/fs Suite")
}
