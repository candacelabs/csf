// Copyright 2026 Candace Labs

package hfjobs_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHFJobs(test *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(test, "ipc/hfjobs Hugging Face Jobs API")
}
