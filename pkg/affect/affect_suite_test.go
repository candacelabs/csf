// Copyright 2026 Candace Labs

package affect_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAffect(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/affect")
}
