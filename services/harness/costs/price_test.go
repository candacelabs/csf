// Copyright 2026 Candace Labs

package costs_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/costs"
)

var _ = Describe("fitting prices to the money the record reports", func() {
	const fitTolerance = 1e-12
	known := costs.Price{Write: 2e-6, Read: 1e-7, Output: 5e-6}

	turn := func(tokens costs.Tokens) costs.Turn {
		return costs.Turn{ByModel: map[string]costs.Priced{model: {Tokens: tokens, USD: known.Of(tokens)}}}
	}

	It("recovers each kind's price, and leaves a kind too rare to identify undetermined", func() {
		runs := []costs.Run{{Turns: []costs.Turn{
			turn(costs.Tokens{Uncached: 1, Write: 20_000, Read: 30_000, Output: 500}),
			turn(costs.Tokens{Uncached: 1, Write: 1_000, Read: 400_000, Output: 2_000}),
			turn(costs.Tokens{Uncached: 2, Write: 90_000, Read: 10_000, Output: 100}),
			turn(costs.Tokens{Uncached: 1, Write: 3_000, Read: 1_000_000, Output: 9_000}),
		}}}
		fits := costs.FitPrices(runs)
		Expect(fits).To(HaveLen(1))
		fit := fits[0]
		Expect(fit.N).To(Equal(4))
		Expect(fit.Determined).To(Equal([4]bool{false, true, true, true}))
		Expect(fit.Price.Write).To(BeNumerically("~", known.Write, fitTolerance))
		Expect(fit.Price.Read).To(BeNumerically("~", known.Read, fitTolerance))
		Expect(fit.Price.Output).To(BeNumerically("~", known.Output, fitTolerance))
		Expect(fit.Residual).To(BeNumerically("<", fitTolerance))
	})

	It("fits nothing when a model has fewer turns than kinds", func() {
		fits := costs.FitPrices([]costs.Run{{Turns: []costs.Turn{turn(costs.Tokens{Write: 1, Read: 1, Output: 1})}}})
		Expect(fits).To(HaveLen(1))
		Expect(fits[0].Determined).To(Equal([4]bool{}))
		Expect(fits[0].Price).To(Equal(costs.Price{}))
	})
})
