// Copyright 2026 Candace Labs

package prod_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf/prod"
)

var _ = Describe("prod", func() {
	Describe("the registry", func() {
		It("carries the reference older rows and the chief rows", func() {
			ids := make(map[string]bool)
			for _, row := range prod.Rows {
				Expect(ids[row.ID]).To(BeFalse(), "row id %q is duplicated", row.ID)
				ids[row.ID] = true
				Expect(row.Weight).To(BeNumerically(">", 0), "row %q has a positive weight", row.ID)
				Expect(row.Meaning).NotTo(BeEmpty(), "row %q states its meaning", row.ID)
			}

			// The reference acceptance surface names exactly these eight older
			// signals; losing one silently changes the score without a
			// method-version bump.
			older := []string{
				"csfc-check", "generated-drift", "cs-16", "cs-15", "cs-17",
				"ontology-dirs", "retired-vocabulary", "unlinked-terms",
			}
			for _, id := range older {
				row, ok := prod.RowByID(id)
				Expect(ok).To(BeTrue(), "older row %q is present", id)
				Expect(row.Kind).To(Equal(prod.KindOlder), "row %q is an older row", id)
			}
			Expect(prod.OlderRows).To(HaveLen(8))

			chief := []string{
				"every_directory_declared", "fanout_at_most_16", "grammar_decision_fits_one_pick",
				"grammar_generated_from_kinds", "cli_purpose_declared", "csf_only_under_csf",
			}
			for _, id := range chief {
				row, ok := prod.RowByID(id)
				Expect(ok).To(BeTrue(), "chief row %q is present", id)
				Expect(row.Kind).To(Equal(prod.KindChief), "row %q is a chief row", id)
				Expect(row.Blocking).To(BeTrue(), "chief row %q blocks", id)
			}
			Expect(prod.ChiefRows).To(HaveLen(6))
		})

		It("references the same older weights and blocking flags as the OCaml", func() {
			Expect(weight("csfc-check")).To(Equal(10))
			Expect(blocking("csfc-check")).To(BeTrue())
			Expect(weight("generated-drift")).To(Equal(10))
			Expect(blocking("generated-drift")).To(BeTrue())
			Expect(weight("cs-16")).To(Equal(5))
			Expect(blocking("cs-16")).To(BeTrue())
			Expect(weight("cs-15")).To(Equal(3))
			Expect(blocking("cs-15")).To(BeFalse())
			Expect(weight("cs-17")).To(Equal(3))
			Expect(blocking("cs-17")).To(BeFalse())
			Expect(weight("ontology-dirs")).To(Equal(3))
			Expect(weight("retired-vocabulary")).To(Equal(2))
			Expect(weight("unlinked-terms")).To(Equal(1))
			Expect(blocking("unlinked-terms")).To(BeTrue())
		})
	})

	Describe("the reference score", func() {
		// These are tools/house_lint/alignment_test.ml's own vectors, ported to
		// Go: the reference's asserted behavior is the acceptance surface for
		// prod_score_matches_reference.
		var allMeasured = func(count int) prod.Record {
			var observations []prod.ObservationEntry
			for _, row := range prod.OlderRows {
				entry, ok := prod.Entry(row.ID, prod.Measured(count))
				Expect(ok).To(BeTrue())
				observations = append(observations, entry)
			}
			return prod.Record{Observations: observations}
		}

		It("maps a clean tree to zero and a weighted sum to the penalty", func() {
			clean := allMeasured(0)
			Expect(clean.Penalty()).To(Equal(0))
			score, err := prod.ScoreOfPenalty(0)
			Expect(err).NotTo(HaveOccurred())
			Expect(score).To(Equal(0.0))
			Expect(clean.Complete()).To(BeTrue())

			totalWeight := 0
			for _, row := range prod.OlderRows {
				totalWeight += row.Weight
			}
			Expect(allMeasured(2).Penalty()).To(Equal(2 * totalWeight))
		})

		It("scores one half at the scale and stays monotone below one", func() {
			half, err := prod.ScoreOfPenalty(prod.Scale)
			Expect(err).NotTo(HaveOccurred())
			Expect(half).To(Equal(0.5))

			ten, _ := prod.ScoreOfPenalty(10)
			eleven, _ := prod.ScoreOfPenalty(11)
			Expect(ten).To(BeNumerically("<", eleven))

			huge, _ := prod.ScoreOfPenalty(1_000_000_000)
			Expect(huge).To(BeNumerically("<", 1.0))
		})

		It("refuses a negative penalty", func() {
			_, err := prod.ScoreOfPenalty(-1)
			Expect(err).To(HaveOccurred())
		})

		It("counts a measured signal and marks an unmeasured one incomplete", func() {
			cs15, _ := prod.RowByID("cs-15")
			cs17Entry, _ := prod.Entry("cs-17", prod.NotMeasured("TODO: pending checker"))
			only := prod.Record{Observations: []prod.ObservationEntry{
				mustEntry("cs-15", prod.Measured(4)),
				cs17Entry,
			}}
			Expect(only.Penalty()).To(Equal(4 * cs15.Weight))
			Expect(only.Complete()).To(BeFalse())
		})
	})

	Describe("the alignment record JSON", func() {
		It("serializes the reference shape and round-trips", func() {
			cs16Entry, _ := prod.Entry("cs-16", prod.Measured(3))
			cs17Entry, _ := prod.Entry("cs-17", prod.NotMeasured("TODO: pending checker"))
			record := prod.Record{
				Revision:     "0123456789abcdef",
				CommitTime:   1_790_000_000,
				Observations: []prod.ObservationEntry{cs16Entry, cs17Entry},
			}
			raw, err := json.Marshal(record)
			Expect(err).NotTo(HaveOccurred())

			var document map[string]any
			Expect(json.Unmarshal(raw, &document)).To(Succeed())

			Expect(document["schema"]).To(Equal("candace.ontology.alignment/v1"))
			Expect(document["method_version"]).To(Equal(float64(1)))
			Expect(document["lower_is_better"]).To(Equal(true))
			cs16, _ := prod.RowByID("cs-16")
			Expect(document["penalty"]).To(Equal(float64(3 * cs16.Weight)))
			Expect(document["complete"]).To(Equal(false))

			signals := document["signals"].([]any)
			Expect(signals).To(HaveLen(2))

			measured := signals[0].(map[string]any)
			Expect(measured["id"]).To(Equal("cs-16"))
			Expect(measured["status"]).To(Equal("measured"))
			Expect(measured["count"]).To(Equal(float64(3)))
			Expect(measured["contribution"]).To(Equal(float64(3 * cs16.Weight)))

			unmeasured := signals[1].(map[string]any)
			Expect(unmeasured["id"]).To(Equal("cs-17"))
			Expect(unmeasured["status"]).To(Equal("not_measured"))
			Expect(unmeasured["count"]).To(BeNil())
			Expect(unmeasured["reason"]).To(Equal("TODO: pending checker"))
			Expect(unmeasured["contribution"]).To(Equal(float64(0)))

			formula := document["formula"].(string)
			Expect(formula).To(ContainSubstring("penalty + scale"))
		})
	})
})

func mustEntry(id string, observation prod.Observation) prod.ObservationEntry {
	entry, ok := prod.Entry(id, observation)
	if !ok {
		panic("unknown row " + id)
	}
	return entry
}

func weight(id string) int {
	row, ok := prod.RowByID(id)
	if !ok {
		panic("unknown row " + id)
	}
	return row.Weight
}

func blocking(id string) bool {
	row, ok := prod.RowByID(id)
	if !ok {
		panic("unknown row " + id)
	}
	return row.Blocking
}
