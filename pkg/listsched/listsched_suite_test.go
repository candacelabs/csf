// Copyright 2026 Candace Labs

package listsched_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestListSched(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/listsched suite")
}
