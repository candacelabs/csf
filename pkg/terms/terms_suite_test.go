// Copyright 2026 Candace Labs

package terms_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTerms(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/terms")
}
