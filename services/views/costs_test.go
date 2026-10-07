// Copyright 2026 Candace Labs

package views_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/views"
)

var _ = Describe("CostsThrough", func() {
	It("adds up the cost events at or before the instant by operation, agent and model", func() {
		events := []views.CostEvent{
			{Operation: "turn_warm", Agent: "a", Model: "m", At: at(10, 0), USD: 0.5},
			{Operation: "turn_warm", Agent: "a", Model: "m", At: at(11, 0), USD: 0.25},
			{Operation: "reopen", Agent: "a", Model: "m", At: at(12, 0), USD: 1},
		}
		Expect(views.CostsThrough(events, at(11, 0))).To(Equal(map[views.CostKey]views.CostTally{
			{Operation: "turn_warm", Agent: "a", Model: "m"}: {Events: 2, USD: 0.75},
		}))
	})
})

var _ = Describe("DeriveCosts", func() {
	It("derives the hypervisor's cost events from the runs through #353's cost model", func() {
		events, err := views.DeriveCosts(stateDirectory())
		Expect(err).NotTo(HaveOccurred())
		for _, event := range events {
			Expect(event.Operation).NotTo(HavePrefix("HYPERVISOR_OPERATION_"))
			Expect(event.At.IsZero()).To(BeFalse())
		}
	})
})
