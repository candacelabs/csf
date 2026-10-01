package mailbox

import (
	"context"
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

	It("refuses pre-stopped submissions when a send is ready", func() {
		expectReadySendRefused(func(box *Mailbox[int]) bool {
			box.Stop()
			return box.Submit(func(current *int) bool { return false })
		})
	})

	It("refuses pre-stopped contextual submissions when a send is ready", func() {
		expectReadySendRefused(func(box *Mailbox[int]) bool {
			box.Stop()
			return box.SubmitContext(context.Background(), nil, func(current *int) bool { return false })
		})
	})

	It("refuses pre-canceled contexts when a send is ready", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		expectReadySendRefused(func(box *Mailbox[int]) bool {
			return box.SubmitContext(ctx, nil, func(current *int) bool { return false })
		})
	})

	It("refuses pre-closed cancellation channels when a send is ready", func() {
		canceled := make(chan struct{})
		close(canceled)
		expectReadySendRefused(func(box *Mailbox[int]) bool {
			return box.SubmitContext(context.Background(), canceled, func(current *int) bool { return false })
		})
	})
})

const readySendAttempts = 128

func expectReadySendRefused(submit func(box *Mailbox[int]) bool) {
	for range readySendAttempts {
		box := New[int]()
		// Production mailboxes remain unbuffered. This test-only channel makes a
		// command send ready alongside the abandonment select case without a Run
		// goroutine that would need cleanup.
		box.commands = make(chan Command[int], 1)

		Expect(submit(box)).To(BeFalse())
		Expect(len(box.commands)).To(BeZero())
	}
}
