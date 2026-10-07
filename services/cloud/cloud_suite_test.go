// Copyright 2026 Candace Labs

package cloud_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCloud(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "services/cloud paid cloud jobs")
}
