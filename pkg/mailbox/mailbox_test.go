package mailbox_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/mailbox"
)

// state is a stand-in for whatever a caller serializes. Nothing here locks it:
// that every assertion below passes under -race is the point of the package.
type state struct {
	applied []string
}

// run starts a mailbox's goroutine and returns it with the state it owns.
// Reading that state from a spec is only safe once the mailbox has stopped,
// which every spec that reads it waits for.
func run() (*mailbox.Mailbox[state], *state) {
	box := mailbox.New[state]()
	owned := &state{}
	go box.Run(owned)
	return box, owned
}

var _ = Describe("Mailbox", func() {
	It("runs commands one at a time, in submission order", func() {
		box, owned := run()

		for _, name := range []string{"first", "second", "third"} {
			Expect(box.Submit(func(current *state) bool {
				current.applied = append(current.applied, name)
				return false
			})).To(BeTrue())
		}
		Expect(box.Submit(func(current *state) bool { return true })).To(BeTrue())
		Eventually(box.Stopped()).Should(BeClosed())

		Expect(owned.applied).To(Equal([]string{"first", "second", "third"}))
	})

	It("refuses submissions once a command has retired the goroutine", func() {
		box, _ := run()

		Expect(box.Submit(func(current *state) bool { return true })).To(BeTrue())
		Eventually(box.Stopped()).Should(BeClosed())

		Expect(box.Submit(func(current *state) bool { return false })).To(BeFalse(),
			"a stopped mailbox reports the refusal rather than blocking forever")
	})

	It("blocks a submission while the goroutine is inside a command", func(ctx SpecContext) {
		box, _ := run()

		occupied := make(chan struct{})
		release := make(chan struct{})
		Expect(box.Submit(func(current *state) bool {
			close(occupied)
			<-release
			return false
		})).To(BeTrue())
		<-occupied
		defer close(release)

		impatient, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		Expect(box.SubmitContext(impatient, nil, func(current *state) bool { return false })).To(BeFalse())
	})

	It("publishes what a command wrote before it stopped the mailbox", func() {
		box, owned := run()

		// The shape a caller with a close result depends on: the command
		// writes, then stops, and a reader that was waiting on Stopped sees
		// the write.
		Expect(box.Submit(func(current *state) bool {
			current.applied = append(current.applied, "final")
			box.Stop()
			return true
		})).To(BeTrue())

		<-box.Stopped()
		Expect(owned.applied).To(Equal([]string{"final"}))
	})

	It("tolerates Stop from both a command and Run", func() {
		box, _ := run()

		Expect(box.Submit(func(current *state) bool {
			box.Stop()
			return true
		})).To(BeTrue())
		Eventually(box.Stopped()).Should(BeClosed())

		Expect(box.Stop).NotTo(Panic(), "Stop is idempotent, so no caller has to track who stopped first")
	})

	Describe("SubmitContext", func() {
		It("gives up when the caller's context is done", func() {
			box, _ := run()
			DeferCleanup(func() { Expect(box.Submit(func(current *state) bool { return true })).To(BeTrue()) })

			occupied := make(chan struct{})
			release := make(chan struct{})
			Expect(box.Submit(func(current *state) bool {
				close(occupied)
				<-release
				return false
			})).To(BeTrue())
			<-occupied
			defer close(release)

			done, cancel := context.WithCancel(context.Background())
			cancel()
			Expect(box.SubmitContext(done, nil, func(current *state) bool { return false })).To(BeFalse())
		})

		It("gives up when the supplied cancellation channel closes", func() {
			box, _ := run()
			DeferCleanup(func() { Expect(box.Submit(func(current *state) bool { return true })).To(BeTrue()) })

			occupied := make(chan struct{})
			release := make(chan struct{})
			Expect(box.Submit(func(current *state) bool {
				close(occupied)
				<-release
				return false
			})).To(BeTrue())
			<-occupied
			defer close(release)

			canceled := make(chan struct{})
			close(canceled)
			Expect(box.SubmitContext(context.Background(), canceled,
				func(current *state) bool { return false })).To(BeFalse())
		})

		It("gives up when the mailbox has stopped", func() {
			box, _ := run()

			Expect(box.Submit(func(current *state) bool { return true })).To(BeTrue())
			Eventually(box.Stopped()).Should(BeClosed())

			Expect(box.SubmitContext(context.Background(), nil,
				func(current *state) bool { return false })).To(BeFalse())
		})

		It("treats a nil cancellation channel as no second cancellation", func() {
			box, owned := run()

			Expect(box.SubmitContext(context.Background(), nil, func(current *state) bool {
				current.applied = append(current.applied, "accepted")
				return true
			})).To(BeTrue())
			<-box.Stopped()

			Expect(owned.applied).To(Equal([]string{"accepted"}))
		})
	})
})
