// Copyright 2026 Candace Labs

package opsview

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Records in the shape a miner's mutate verb prints (Mutation in
// services/ouroboros/contract/records.proto), as the series file holds them.
const (
	firstMutationRecord  = `{"miner":"draft-pr-late","at":1791100800,"killed":10,"survived":0,"excluded":6,"score":1.0,"floor":1.0,"accepted":true,"surviving":[],"seconds":0.031}` + "\n"
	secondMutationRecord = `{"miner":"draft-pr-late","at":1791104400,"killed":9,"survived":1,"excluded":6,"score":0.9,"floor":1.0,"accepted":false,"surviving":[{"kind":"write knee","description":"rule 1: write 468 for knee(K)","source":"invisible(R) :- gated(R), score(R, S), gt(S, 468).","changed":["6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31"]}],"seconds":0.028}` + "\n"
	otherMutationRecord  = `{"miner":"a-second-miner","at":1791100900,"killed":4,"survived":0,"excluded":1,"score":1.0,"floor":1.0,"accepted":true,"surviving":[],"seconds":0.01}` + "\n"
	minerlessRecord      = `{"at":1791100900,"killed":4}` + "\n"
)

var _ = Describe("reading the mutation score series", func() {
	It("groups records by miner in name order and keeps file order within a miner", func() {
		series := ReadSeries([]byte(firstMutationRecord + "not json\n" + minerlessRecord + otherMutationRecord + secondMutationRecord))
		Expect(series).To(HaveLen(2))
		Expect(series[0].Miner).To(Equal("a-second-miner"))
		Expect(series[1].Miner).To(Equal("draft-pr-late"))
		Expect(series[1].Points).To(HaveLen(2))
		Expect(series[1].Points[0].At).To(Equal(time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)))
		Expect(series[1].Points[0].Accepted).To(BeTrue())
		Expect(series[1].Points[1].Accepted).To(BeFalse())
		Expect(series[1].Points[1].Surviving).To(Equal([]string{"rule 1: write 468 for knee(K)"}))
	})

	It("labels a point by its counts and score", func() {
		point := ReadSeries([]byte(secondMutationRecord))[0].Points[0]
		Expect(pointOf(point)).To(Equal(pointView{Time: "10-04 09:00", Label: "9/10 = 0.90", Accepted: false}))
	})

	It("reads a missing or empty file as no series, equal to the series never read", func() {
		Expect(ReadSeries(nil)).To(BeEmpty())
		Expect(seriesEqual(nil, ReadSeries(nil))).To(BeTrue())
		Expect(seriesEqual(nil, ReadSeries([]byte(firstMutationRecord)))).To(BeFalse())
	})

	It("marks the panel dirty only when the series changed", func() {
		before := viewState{miners: ReadSeries([]byte(firstMutationRecord))}
		Expect(minersChanged(before, before)).To(BeFalse())
		Expect(minersChanged(before, viewState{miners: ReadSeries([]byte(firstMutationRecord + secondMutationRecord))})).To(BeTrue())
	})
})
