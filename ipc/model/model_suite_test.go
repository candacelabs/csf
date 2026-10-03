// Copyright 2026 Candace Labs

package model_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModel(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/model brain contract")
}
