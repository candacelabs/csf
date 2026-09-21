package raftdemo

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRaftDemo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "examples/widget/raftdemo")
}
