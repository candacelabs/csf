package worktreeadapter_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWorktreeAdapter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "worktree adapter suite")
}
