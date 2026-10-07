// Copyright 2026 Candace Labs

package knee_test

import (
	"slices"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/knee"
)

func TestKnee(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/knee Suite")
}

var _ = Describe("Of", func() {
	It("reads the threshold from the sample, always one of its points", func() {
		sample := []float64{0.9, 0.2, 0.5, 0.3, 0.95, 0.85, 0.4}

		result, ok := knee.Of(sample)

		Expect(ok).To(BeTrue())
		Expect(sample).To(ContainElement(result.Threshold))
		Expect(result.Samples).To(Equal(len(sample)))
		Expect(result.Quantile).To(BeNumerically(">", 0))
		Expect(result.Quantile).To(BeNumerically("<=", 1))
	})

	It("does not disturb the sample it reads", func() {
		sample := []float64{3, 1, 2}
		before := slices.Clone(sample)

		_, _ = knee.Of(sample)

		Expect(sample).To(Equal(before))
	})

	It("reports no curve for a sample of fewer than two points", func() {
		_, ok := knee.Of(nil)
		Expect(ok).To(BeFalse())
		_, ok = knee.Of([]float64{1})
		Expect(ok).To(BeFalse())
	})

	It("puts the knee where a sparse tail turns into a dense body", func() {
		// A flat run at one then a long dense run at two: the curve bends
		// where the second run starts, so the knee is that first two.
		sample := []float64{1, 2, 2, 2, 2, 2, 2, 2}

		result, ok := knee.Of(sample)

		Expect(ok).To(BeTrue())
		Expect(result.Threshold).To(Equal(2.0))
	})
})

var _ = Describe("Index", func() {
	It("returns zero for fewer than two points", func() {
		Expect(knee.Index(nil)).To(Equal(0))
		Expect(knee.Index([]float64{5})).To(Equal(0))
	})

	It("places the knee inside the curve for a two-regime sample", func() {
		// Ten points near one, then one far outlier: the cumulative curve is
		// dense over the first regime, so the knee is inside it, not the
		// outlier.
		x := make([]float64, 0, 11)
		for range 10 {
			x = append(x, 1)
		}
		x = append(x, 100)

		index := knee.Index(x)

		Expect(index).To(BeNumerically(">=", 0))
		Expect(index).To(BeNumerically("<", len(x)-1))
	})
})
