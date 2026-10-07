// Copyright 2026 Candace Labs

package views_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestViews(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/views Suite")
}
