package dashbored

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The reservoir, the histogram and the merge are pure functions, so every rule
// below is driven from a literal. Not one specification in this file starts a
// goroutine.

// draws returns a deterministic stand-in for a collector's random stream, so a
// specification about the reservoir is about the reservoir rather than about
// luck.
func draws(values ...int) func(bound int) int {
	taken := 0
	return func(bound int) int {
		if taken >= len(values) {
			return bound - 1
		}
		chosen := values[taken]
		taken++
		if chosen >= bound {
			return bound - 1
		}
		return chosen
	}
}

var _ = Describe("A collector's reservoir", func() {
	It("keeps every observation until it is full", func() {
		reservoir := make([]float64, 0, 3)
		for seen, value := range []float64{0.1, 0.2, 0.3} {
			reservoir = reservoirAdd(reservoir, value, seen+1, draws())
		}
		Expect(reservoir).To(Equal([]float64{0.1, 0.2, 0.3}))
	})

	It("never grows past its size, and replaces in place after that", func() {
		reservoir := make([]float64, 0, 2)
		reservoir = reservoirAdd(reservoir, 0.1, 1, draws())
		reservoir = reservoirAdd(reservoir, 0.2, 2, draws())

		// The third observation replaces slot 0; the fourth draws a slot
		// outside the reservoir and is discarded, which is what gives every
		// observation the same chance rather than the recent ones a better one.
		reservoir = reservoirAdd(reservoir, 0.3, 3, draws(0))
		Expect(reservoir).To(Equal([]float64{0.3, 0.2}))

		reservoir = reservoirAdd(reservoir, 0.4, 4, draws(3))
		Expect(reservoir).To(HaveLen(2))
		Expect(reservoir).To(Equal([]float64{0.3, 0.2}))
	})

	It("holds nothing when it was given no room", func() {
		Expect(reservoirAdd(make([]float64, 0, 0), 0.5, 1, draws())).To(BeEmpty())
	})
})

var _ = Describe("The histogram", func() {
	It("puts every observation in exactly one bucket", func() {
		buckets := make([]int, len(histogramBounds)+1)
		for _, value := range []float64{0.05, 0.2, 0.4, 0.8, 0.95, 0.995, 1.0} {
			buckets = foldSample(buckets, value)
		}

		total := 0
		for _, count := range buckets {
			total += count
		}
		Expect(total).To(Equal(7))
		Expect(buckets).To(HaveLen(len(histogramBounds)+1),
			"a histogram of N bounds has N+1 buckets: the last one is everything above")
		Expect(buckets[len(histogramBounds)]).To(Equal(2),
			"0.995 and 1.0 are both above the last bound, and both land in the "+
				"overflow bucket rather than off the end of the slice")
	})

	It("reports no median for a histogram nothing is in", func() {
		Expect(median(make([]int, len(histogramBounds)+1))).To(Equal(0.0),
			"an empty histogram has no median and says so rather than pretending")
	})

	It("reports the midpoint of the bucket the middle observation is in", func() {
		buckets := make([]int, len(histogramBounds)+1)
		for range 10 {
			buckets = foldSample(buckets, 0.3)
		}
		Expect(median(buckets)).To(BeNumerically("~", (0.25+0.5)/2, 1e-9))
	})
})

var _ = Describe("The published view", func() {
	It("merges two halves neither goroutine can see together", func() {
		view := MetricsView{RetentionDays: 395, QueryWindowHours: 2}

		view = foldTelemetry(view, telemetryReport{
			Kind: reportFlush, CollectorsUp: 3, SamplesPerSecond: 12, AggregatorUp: true})
		Expect(view.Quiet()).To(BeTrue(),
			"nothing is wrong is the only thing a metrics service ever wants to say")

		view = foldTelemetry(view, telemetryReport{
			Kind: reportAlert, Breaching: true, FiringAlert: "p99_latency"})
		Expect(view.Quiet()).To(BeFalse())
		Expect(view.FiringAlert).To(Equal("p99_latency"))
		Expect(view.SamplesPerSecond).To(Equal(12),
			"the alerter's half says nothing about the aggregator's, and overwrites none of it")

		view = foldTelemetry(view, telemetryReport{
			Kind: reportFlush, CollectorsUp: 2, SamplesPerSecond: 8, AggregatorUp: true})
		Expect(view.FiringAlert).To(Equal("p99_latency"),
			"and the aggregator's half overwrites none of the alerter's either")
		Expect(view.RetentionDays).To(Equal(395))
	})
})
