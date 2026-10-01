package queuecumber

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQueuecumber(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "examples/widget/candaws/queuecumber")
}
