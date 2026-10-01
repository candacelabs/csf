package widget_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget"
)

var _ = Describe("Reading a wire value into a state field", func() {
	DescribeTable("ParseFlag",
		func(raw string, fallback bool, expected bool) {
			Expect(widget.ParseFlag(raw, fallback)).To(Equal(expected))
		},
		Entry("true", "true", false, true),
		Entry("false", "false", true, false),
		Entry("the form spelling of a checked box", "1", false, true),
		Entry("the form spelling of an unchecked box", "0", true, false),
		Entry("a capitalised spelling", "True", false, true),
		Entry("keeps the field where it was when the value is not a boolean", "yes", true, true),
		Entry("keeps a false field too, rather than defaulting to false", "yes", false, false),
		Entry("keeps the field on an empty value, which is not the same as false", "", true, true),
	)

	DescribeTable("ParseCount",
		func(raw string, fallback int64, expected int64) {
			Expect(widget.ParseCount(raw, fallback)).To(Equal(expected))
		},
		Entry("a number", "42", int64(0), int64(42)),
		Entry("a negative number, because a count is not monotonic", "-3", int64(7), int64(-3)),
		Entry("a lower number, for the same reason", "1", int64(9), int64(1)),
		Entry("keeps the field on a value that is not an integer", "many", int64(7), int64(7)),
		Entry("keeps the field on a decimal, which this is not", "1.5", int64(7), int64(7)),
	)

	DescribeTable("ParseCounter",
		func(raw string, fallback uint64, expected uint64) {
			Expect(widget.ParseCounter(raw, fallback)).To(Equal(expected))
		},
		Entry("a higher number", "42", uint64(7), uint64(42)),
		Entry("the same number", "7", uint64(7), uint64(7)),
		Entry("keeps the field on a lower number, because a counter is monotonic",
			"3", uint64(7), uint64(7)),
		Entry("keeps the field on a negative number, which a counter cannot be",
			"-1", uint64(7), uint64(7)),
		Entry("keeps the field on a value that is not an integer", "many", uint64(7), uint64(7)),
	)

	It("does not walk a counter backwards when a delivery arrives late", func() {
		// The failure this exists to prevent: two deliveries, the older one
		// arriving second. A counter that took it would show the wrong total
		// until the next message and would have looked right the whole time.
		held := widget.ParseCounter("9", 0)

		Expect(widget.ParseCounter("4", held)).To(Equal(uint64(9)))
	})
})
