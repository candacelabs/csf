// Copyright 2026 Candace Labs

package ros_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=spine.go -destination=mock_spine_test.go -package=ros_test

func TestROS(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/ros spine boundary")
}
