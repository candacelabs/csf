// Copyright 2026 Candace Labs

package database_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock_listener_test.go -package=database_test github.com/candacelabs/csf/io/net IListener

func TestDatabase(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Database Service Suite")
}
