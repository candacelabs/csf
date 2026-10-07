// Copyright 2026 Candace Labs

package prod_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf/prod"
)

var _ = Describe("the golden metrics and the merge quality gate", func() {
	It("counts violations over the chief rows only, per item and unweighted", func() {
		r := prod.Reading{Chief: map[string]map[string]int{
			"a": {"fanout_at_most_16": 3, "cli_purpose_declared": 1},
			"b": {},
		}}
		Expect(r.Violations()).To(Equal(3 + 1))

		// The older observations are not chief violations: the ruling counts
		// "seed and violations over the chief rows only".
		record := prod.Record{Observations: []prod.ObservationEntry{mustEntry("cs-15", prod.Measured(2))}}
		Expect(prod.Reading{Record: record, Chief: map[string]map[string]int{"a": {"cli_purpose_declared": 1}}}.
			Violations()).To(Equal(1))
	})

	It("counts the seed files: the directories whose chief rows all hold", func() {
		r := reading(map[string]map[string]int{
			"a": {},
			"b": {},
			"c": {"fanout_at_most_16": 1},
			"d": {"cli_purpose_declared": 1},
		})
		Expect(r.SeedFiles()).To(Equal(2))
		Expect(reading(map[string]map[string]int{"a": {}, "b": {}}).SeedFiles()).To(Equal(2))
		Expect(reading(nil).SeedFiles()).To(BeZero())
	})

	It("measures a merge that grows the seed and breaks nothing", func() {
		main := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		after := reading(map[string]map[string]int{"a": {}, "b": {}})
		metrics := prod.Metrics(main, after)
		Expect(metrics.SeedFilesBefore).To(Equal(1))
		Expect(metrics.SeedFilesAfter).To(Equal(2))
		Expect(metrics.SeedBreaks).To(BeZero())
		Expect(metrics.ViolationsBefore).To(Equal(1))
		Expect(metrics.ViolationsAfter).To(BeZero())
		Expect(metrics.Refused()).To(BeFalse())
	})

	It("refuses a merge that breaks main's seed", func() {
		main := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		after := reading(map[string]map[string]int{"a": {"cli_purpose_declared": 2}, "b": {"fanout_at_most_16": 1}})
		metrics := prod.Metrics(main, after)
		Expect(metrics.SeedBreaks).To(Equal(1))
		Expect(metrics.Refused()).To(BeTrue())
		Expect(metrics.Reasons()).To(ContainElement(prod.MergeGateSeedBreaks))
	})

	It("refuses a merge that raises violations outside main's seed", func() {
		main := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		// b was already dirty, so no seed broke, but the violation count rose.
		after := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 3}})
		metrics := prod.Metrics(main, after)
		Expect(metrics.SeedBreaks).To(BeZero())
		Expect(metrics.ViolationsAfter).To(Equal(3))
		Expect(metrics.Reasons()).To(Equal([]prod.MergeGate{prod.MergeGateChiefViolationsRise}))
	})

	It("refuses a merge that lowers the seed files", func() {
		main := reading(map[string]map[string]int{"a": {}, "b": {}, "c": {"fanout_at_most_16": 1}})
		// The merge dirties a directory main's seed held clean, so the seed falls.
		after := reading(map[string]map[string]int{"a": {"cli_purpose_declared": 1}, "b": {}, "c": {"fanout_at_most_16": 1}})
		metrics := prod.Metrics(main, after)
		Expect(metrics.SeedFilesAfter).To(BeNumerically("<", metrics.SeedFilesBefore))
		Expect(metrics.Reasons()).To(ContainElement(prod.MergeGateSeedFilesFall))
	})

	It("refuses a merge that lowers the derived share", func() {
		// main holds one source and one authored file, so its derived share is
		// 1/2. The merge drops the source file, so the share falls to 0.
		main := prod.Reading{Files: []string{"csf/a.csf", "services/hand.go"}}
		after := prod.Reading{Files: []string{"services/hand.go"}}
		metrics := prod.Metrics(main, after)
		Expect(metrics.DerivedShareBefore).To(BeNumerically("~", 0.5))
		Expect(metrics.DerivedShareAfter).To(BeZero())
		Expect(metrics.Reasons()).To(Equal([]prod.MergeGate{prod.MergeGateDerivedShareFalls}))
	})

	It("allows a merge that holds or raises the derived share", func() {
		// The merge reproduces a file main authored by hand, so the share rises;
		// nothing else moves, so no gate refuses.
		main := prod.Reading{Files: []string{"csf/a.csf", "services/hand.go"}}
		after := prod.Reading{Files: []string{"csf/a.csf", "pkg/BUILD.bazel"}}
		metrics := prod.Metrics(main, after)
		Expect(metrics.DerivedShareAfter).To(BeNumerically(">", metrics.DerivedShareBefore))
		Expect(metrics.Refused()).To(BeFalse())
		Expect(metrics.Reasons()).To(BeEmpty())
	})

	It("reports the merge result's authored families, descending", func() {
		// The merge result authors three files under services/hand and one under
		// pkg, and reproduces a BUILD file. The report lists the authored
		// families most files first and never a machine-written family: pkg holds
		// one authored file, not two, because BUILD.bazel is derived.
		merge := prod.Reading{Files: []string{
			"services/hand/b.go", "services/hand/a.go", "services/hand/c.go",
			"pkg/hand.go", "pkg/BUILD.bazel",
		}}
		snapshot := prod.SnapshotOf("m", "p", prod.Reading{}, merge)
		Expect(snapshot.AuthoredFamilies).To(Equal([]prod.Family{
			{Directory: "services/hand", Files: 3},
			{Directory: "pkg", Files: 1},
		}))

		encoded, err := json.Marshal(snapshot)
		Expect(err).NotTo(HaveOccurred())
		var decoded prod.Snapshot
		Expect(json.Unmarshal(encoded, &decoded)).To(Succeed())
		Expect(decoded.AuthoredFamilies).To(Equal(snapshot.AuthoredFamilies))
	})

	It("names every gate a merge violates in a fixed order", func() {
		main := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}, "c": {"fanout_at_most_16": 1}})
		after := reading(map[string]map[string]int{"a": {"cli_purpose_declared": 1}, "b": {"fanout_at_most_16": 2}, "c": {"fanout_at_most_16": 1}})
		metrics := prod.Metrics(main, after)
		Expect(metrics.Reasons()).To(Equal([]prod.MergeGate{
			prod.MergeGateSeedFilesFall,
			prod.MergeGateSeedBreaks,
			prod.MergeGateChiefViolationsRise,
		}))
	})

	It("records the merge quality gate's verdict as a JSON snapshot", func() {
		main := reading(map[string]map[string]int{"a": {}, "b": {"fanout_at_most_16": 1}})
		merge := reading(map[string]map[string]int{"a": {"cli_purpose_declared": 2}, "b": {"fanout_at_most_16": 1}})
		snapshot := prod.SnapshotOf("m", "p", main, merge)

		Expect(snapshot.Gate).To(Equal(prod.GateName))
		Expect(snapshot.MainRevision).To(Equal("m"))
		Expect(snapshot.MergeRevision).To(Equal("p"))
		Expect(snapshot.SeedFilesMain).To(Equal(1))
		Expect(snapshot.SeedFilesMerge).To(Equal(0))
		Expect(snapshot.SeedBreaks).To(Equal(1))
		Expect(snapshot.ViolationsMain).To(Equal(1))
		Expect(snapshot.ViolationsMerge).To(Equal(3))
		Expect(snapshot.Refused).To(BeTrue())
		Expect(snapshot.Reasons).To(ConsistOf(
			prod.MergeGateSeedFilesFall, prod.MergeGateSeedBreaks, prod.MergeGateChiefViolationsRise))

		encoded, err := json.Marshal(snapshot)
		Expect(err).NotTo(HaveOccurred())
		var decoded prod.Snapshot
		Expect(json.Unmarshal(encoded, &decoded)).To(Succeed())
		Expect(decoded).To(Equal(snapshot))
	})
})
