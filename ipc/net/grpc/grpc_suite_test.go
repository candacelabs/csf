// Copyright 2026 Candace Labs

package grpc_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../network.go -destination=mock_network_test.go -package=grpc_test

func TestGRPC(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ipc/net/grpc Suite")
}
