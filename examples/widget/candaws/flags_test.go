package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The two flags that reach every engine at once, and what they do at their
// edges.
//
// Both of these were written after an audit attacked the flags rather than the
// fleet, and found that `-pace 0` exited with Yakshave's name on it and that
// `-trouble -5` started a fleet the banner then described wrongly. A flag is a
// contract with a person at a terminal, and neither of those answers is one.

var _ = Describe("The -pace flag", func() {
	It("refuses a value no fleet exists at, in its own name and with its range", func() {
		for _, outside := range []float64{0, -1, -0.5, minimumPace / 2, maximumPace * 2} {
			_, buildError := build(20260903, outside, 1)
			Expect(buildError).To(MatchError(ContainSubstring("-pace is out of range")),
				"for %v", outside)
			Expect(buildError).To(MatchError(ContainSubstring("[0.001, 1000]")),
				"a refusal that does not say what would have been accepted is half a refusal")
		}
	})

	It("builds the whole fleet at both ends of the range it accepts", func() {
		// The bounds are a claim that every engine still validates its own
		// config there, and an engine's refusal at a pace this guard accepted
		// would be the same failure the guard exists to prevent, one step
		// further in.
		for _, inside := range []float64{minimumPace, 0.02, 1, maximumPace} {
			fleet, buildError := build(20260903, inside, 1)
			Expect(buildError).ToNot(HaveOccurred(), "for %v", inside)
			Expect(fleet).ToNot(BeNil())
		}
	})
})

var _ = Describe("The -trouble flag", func() {
	It("reads a negative value as zero rather than as a fainter fleet", func() {
		Expect(effectiveTrouble(-5)).To(Equal(float64(0)))
		Expect(effectiveTrouble(0)).To(Equal(float64(0)))
		Expect(effectiveTrouble(2.5)).To(Equal(2.5))
	})

	It("builds the same fleet from a negative value as from zero", func() {
		// The two branches that used to test the raw value are what this is
		// about: Blobfish's slow zone is switched off by trouble being zero,
		// and Dashbored's breach threshold is rescaled only when it is above
		// zero. A negative value took neither branch, so -trouble -5 was a
		// fleet with an eventually-consistent zone and an unscaled threshold —
		// a third fleet nobody asked for.
		negative, negativeError := build(20260903, 1, -5)
		Expect(negativeError).ToNot(HaveOccurred())
		zero, zeroError := build(20260903, 1, 0)
		Expect(zeroError).ToNot(HaveOccurred())

		Expect(negative.store.Config().SlowZone).To(Equal(zero.store.Config().SlowZone),
			"a negative trouble leaves the store's slow zone off, the same as zero does")
		Expect(negative.metrics.Config().BreachThreshold).
			To(Equal(zero.metrics.Config().BreachThreshold),
				"a negative trouble leaves the breach threshold unscaled, the same as zero does")
		Expect(negative.broker.Config().DropRate).To(Equal(zero.broker.Config().DropRate))
		Expect(negative.pipeline.Config().FailureRate).To(Equal(zero.pipeline.Config().FailureRate))
	})
})
