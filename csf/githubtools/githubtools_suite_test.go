// Copyright 2026 Candace Labs

package githubtools_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGitHubTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GitHub tools")
}
