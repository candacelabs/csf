package mailbox

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMailbox(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/mailbox suite")
}

// The one property that cannot be observed from outside the package, and the
// reason both harness runtimes wrote this channel out by hand: submission is
// the serialization point only because there is nowhere to queue.
var _ = Describe("the command channel", func() {
	It("is unbuffered", func() {
		Expect(cap(New[int]().commands)).To(BeZero())
	})
})
