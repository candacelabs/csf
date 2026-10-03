// Copyright 2026 Candace Labs

package runtime_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=host.go -destination=mock_host_test.go -package=runtime_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=lazy.go -destination=mock_lazy_test.go -package=runtime_test

func TestRuntime(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Runtime Suite")
}
