package core_test

// Contract tests for the shared pkg/core helpers that warden (and any future
// caller) now depend on: BoolToFloat64 encodes the Prometheus 0/1 convention,
// and FormatTimeOrNever renders "never" for the zero time and UTC RFC 3339
// otherwise. Both are load-bearing in warden's metrics, email bodies, and
// incident messages.
//
// FormatAgo is here for the same reason from the other direction: it had no
// test at all while it was a private ladder copied into two dashboards, and
// the reason to centralize it is that both of them render the same labels.
// The boundaries below are what "the same labels" means.

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	core "github.com/candacelabs/csf/pkg/core"
)

func TestCoreContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/core contract suite")
}

var _ = Describe("BoolToFloat64", func() {
	It("maps true to 1 and false to 0 (the Prometheus gauge convention)", func() {
		Expect(core.BoolToFloat64(true)).To(Equal(float64(1)))
		Expect(core.BoolToFloat64(false)).To(Equal(float64(0)))
	})
})

var _ = Describe("FormatTimeOrNever", func() {
	It(`renders the zero time as "never"`, func() {
		Expect(core.FormatTimeOrNever(time.Time{})).To(Equal("never"))
	})

	It("renders a non-zero time as UTC RFC 3339", func() {
		t := time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC)
		Expect(core.FormatTimeOrNever(t)).To(Equal("2026-07-21T15:04:05Z"))
	})

	It("normalizes a non-UTC instant to UTC before formatting", func() {
		// 10:04:05 at UTC-5 is 15:04:05Z.
		loc := time.FixedZone("EST", -5*3600)
		t := time.Date(2026, 7, 21, 10, 4, 5, 0, loc)
		Expect(core.FormatTimeOrNever(t)).To(Equal("2026-07-21T15:04:05Z"))
	})
})

var _ = Describe("FormatAgo", func() {
	DescribeTable("renders the tier the elapsed duration falls in",
		func(elapsed time.Duration, want string) {
			Expect(core.FormatAgo(elapsed)).To(Equal(want))
		},
		Entry("zero", time.Duration(0), "just now"),
		Entry("just under a minute", time.Minute-time.Nanosecond, "just now"),
		Entry("exactly a minute", time.Minute, "1m ago"),
		Entry("truncates rather than rounds within the minute tier",
			59*time.Minute+59*time.Second, "59m ago"),
		Entry("exactly an hour", time.Hour, "1h ago"),
		Entry("truncates rather than rounds within the hour tier",
			23*time.Hour+59*time.Minute, "23h ago"),
		Entry("exactly a day", 24*time.Hour, "1d ago"),
		Entry("truncates rather than rounds within the day tier",
			47*time.Hour+59*time.Minute, "1d ago"),
		Entry("many days", 10*24*time.Hour, "10d ago"),
	)

	It("clamps a negative duration rather than rendering a future age", func() {
		// Clock skew between the machine that stamped an event and the one
		// rendering it. A page that answers "in 3m" is reporting on the
		// clocks, not on the event.
		Expect(core.FormatAgo(-3 * time.Minute)).To(Equal("just now"))
	})
})
