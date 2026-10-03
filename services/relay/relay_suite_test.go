// Copyright 2026 Candace Labs

package relay_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../../ipc/net/network.go -destination=mock_network_test.go -package=relay_test
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=registry.go -destination=mock_registry_test.go -package=relay_test

func TestRelay(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/relay Suite")
}
