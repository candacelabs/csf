package live_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// C-32 / F-4: live.IsRetryable.
//
// The predicate was cut at the checkpoint-2 batch for having no call site, with
// a re-add trigger pre-registered — "something needing to inspect an error it
// did not itself produce". The trigger fired in examples/chat, and L9-1
// measured what the workaround was worth: with live.Retryable replaced by a
// plain fmt.Errorf("%w"), so that the mark is gone entirely, chat's suite stays
// green, including the spec whose failure message reads "the pump must have
// wrapped it with live.Retryable".
//
// That is why the fifth spec below is the one that matters. errors.Unwrap(err)
// != nil answers "does this error wrap anything", and every entry in this table
// except the first two wraps something. A predicate that cannot separate a
// %w-wrapped unmarked error from a marked one is not testing classification.
// ---------------------------------------------------------------------------

var _ = Describe("IsRetryable", func() {
	DescribeTable("reads back the mark Retryable sets, and nothing else",
		func(err error, want bool) {
			Expect(live.IsRetryable(err)).To(Equal(want))
		},

		Entry("a marked error", live.Retryable(errors.New("the broker is reconnecting")), true),

		Entry("an unmarked error", errors.New("the row does not exist"), false),

		// Retryable(nil) is nil, so this is the nil case twice over and both
		// spellings must answer the same way. An application wrapping a result
		// unconditionally must not turn "nothing failed" into "retry it".
		Entry("nil", nil, false),
		Entry("Retryable(nil), which is nil", live.Retryable(nil), false),

		// The mark is found through errors.As, so it survives being carried up
		// through an application's own helpers. A predicate that only looked at
		// the outermost error would be a different and worse function: the
		// executor that classified the failure is rarely the frame that returns
		// it.
		Entry("a marked error wrapped twice",
			fmt.Errorf("subscribing: %w",
				fmt.Errorf("pump: %w",
					live.Retryable(errors.New("the mailbox is full")))),
			true),

		// The input the errors.Unwrap workaround cannot distinguish. It wraps,
		// so the workaround calls it retryable; it carries no mark, so it is
		// terminal. This entry is the whole reason the symbol is back.
		Entry("a plain %w wrap of an unmarked error",
			fmt.Errorf("subscribing: %w", errors.New("the session fell behind")),
			false),
	)

	// Stated separately because it is the property the fifth entry is about,
	// and a spec that asserts the two answers differ says so more directly than
	// two rows of a table that happen to disagree.
	It("separates a wrapped unmarked error from a marked one, where errors.Unwrap does not", func() {
		terminal := fmt.Errorf("subscribing: %w", errors.New("the session fell behind"))
		transient := live.Retryable(fmt.Errorf("pump: %w", errors.New("the mailbox is full")))

		Expect(errors.Unwrap(terminal)).NotTo(BeNil())
		Expect(errors.Unwrap(transient)).NotTo(BeNil())

		Expect(live.IsRetryable(terminal)).To(BeFalse())
		Expect(live.IsRetryable(transient)).To(BeTrue(),
			"both errors wrap, so wrapping cannot be the test; only one of them was classified")
	})

	// The predicate and the library agree by construction — live.IsRetryable is
	// internal/session.IsRetryable, which is the function the actor calls to
	// fill EffectFailedRetryableField — and this is the spec that fails if
	// somebody ever gives the exported one its own body.
	It("answers what the failure event will say", func() {
		transient := func(_ context.Context, _ live.Session[user], _ live.Emitter) error {
			return live.Retryable(fmt.Errorf("pump: %w", errors.New("the broker is reconnecting")))
		}
		app := mount(func(c *live.Config[counter, user]) {
			c.Reduce = func(state counter, ev live.Event) (counter, []live.Effect[user]) {
				switch ev.Name {
				case "counter.increment":
					return state, []live.Effect[user]{logEffect(transient)}
				case live.EffectFailedEvent:
					state.Label = ev.Fields.Get(live.EffectFailedRetryableField)
				}
				return state, nil
			}
		})
		defer app.stop()

		app.send("counter.increment", nil)

		Expect(app.nextPatch().GetUpdates()[0].GetHtml()).To(Equal("<b>true 0</b>"),
			"the event's classification field and live.IsRetryable disagreed about one error")
	})
})
