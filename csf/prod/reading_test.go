// Copyright 2026 Candace Labs

package prod_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf/prod"
)

// reading builds a Reading from a directory-to-chief-counts map.
func reading(chief map[string]map[string]int) prod.Reading {
	return prod.Reading{Chief: chief}
}

var _ = Describe("the seed, its growth, and the two gates", func() {
	It("names the directories where every chief row holds", func() {
		r := reading(map[string]map[string]int{
			"a": {},
			"b": {"fanout_at_most_16": 2},
			"c": {"grammar_decision_fits_one_pick": 1, "cli_purpose_declared": 3},
		})
		Expect(r.Seed()).To(Equal([]string{"a"}))
		Expect(r.AllHold()).To(BeFalse())

		Expect(reading(map[string]map[string]int{"a": {}, "b": {}}).Seed()).To(Equal([]string{"a", "b"}))
		Expect(reading(map[string]map[string]int{"a": {}, "b": {}}).AllHold()).To(BeTrue())
		Expect(reading(nil).AllHold()).To(BeTrue())
	})

	It("grows only when the new seed is a strict superset", func() {
		before := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		after := reading(map[string]map[string]int{"a": {}, "b": {}})
		Expect(prod.Grew(before, after)).To(BeTrue())
		Expect(prod.Grew(after, before)).To(BeFalse())

		// Cleaning b but dirtying a is not growth: a was clean.
		broken := reading(map[string]map[string]int{"a": {"cli_purpose_declared": 1}, "b": {}})
		Expect(prod.Grew(before, broken)).To(BeFalse())

		same := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		Expect(prod.Grew(before, same)).To(BeFalse())
	})

	It("refuses a new violation in a directory that was clean", func() {
		before := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		after := reading(map[string]map[string]int{"a": {"cli_purpose_declared": 2}, "b": {"fanout_at_most_16": 1}})
		refusals := prod.Refuses(before, after)
		Expect(refusals).To(ContainElement(prod.Refusal{
			Gate:      prod.GateNewViolationInSeed,
			Signal:    "cli_purpose_declared",
			Directory: "a",
			Before:    0,
			After:     2,
		}))
	})

	It("refuses a change that raises the row count", func() {
		before := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		after := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 3}})
		refusals := prod.Refuses(before, after)
		Expect(refusals).To(HaveLen(1))
		Expect(refusals[0].Gate).To(Equal(prod.GateRowCountRises))
		Expect(refusals[0].Before).To(Equal(before.Penalty()))
		Expect(refusals[0].After).To(Equal(after.Penalty()))
	})

	It("accepts a change that grows the seed and breaks nothing", func() {
		before := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		after := reading(map[string]map[string]int{"a": {}, "b": {}})
		Expect(prod.Refuses(before, after)).To(BeEmpty())
		Expect(prod.Grew(before, after)).To(BeTrue())
	})

	It("compares readings for the fixed-point check", func() {
		a := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		b := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		Expect(prod.Equal(a, b)).To(BeTrue())
		c := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 2}})
		Expect(prod.Equal(a, c)).To(BeFalse())
	})

	It("scores the chief rows together with the older penalty", func() {
		record := prod.Record{Observations: []prod.ObservationEntry{
			mustEntry("cs-15", prod.Measured(2)),
		}}
		r := prod.Reading{Record: record, Chief: map[string]map[string]int{"a": {"cli_purpose_declared": 1}}}
		// cs-15 weight 3 * 2 plus cli_purpose_declared weight 10 * 1.
		Expect(r.Penalty()).To(Equal(3*2 + 10*1))
	})
})
