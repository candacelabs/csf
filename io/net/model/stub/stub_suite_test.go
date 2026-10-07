// Copyright 2026 Candace Labs

package stub_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStub(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/model/stub canned brain")
}
