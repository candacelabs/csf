// Copyright 2026 Candace Labs

package githubgen_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGitHubGen(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GitHub generator")
}
