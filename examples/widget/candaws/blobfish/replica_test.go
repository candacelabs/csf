package blobfish

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A zone is a table of pure functions with a goroutine around it, so every rule
// below is driven from a literal state. Not one specification in this file
// starts a goroutine.

var _ = Describe("The replica rule table", func() {
	// A map can be enumerated and a method set cannot, which is the whole
	// reason this is a table: without this assertion a fifth operation kind is
	// one every zone silently ignores.
	It("has exactly one rule per operation kind, and no rule for anything else", func() {
		kinds := []opKind{opWrite, opRead, opRepair, opProbe}
		Expect(replicaRules).To(HaveLen(len(kinds)))
		Expect(opNames).To(HaveLen(len(kinds)))
		for _, kind := range kinds {
			Expect(replicaRules).To(HaveKey(kind), "no rule handles %s", kind)
			Expect(kind.String()).ToNot(ContainSubstring("op "),
				"kind %d prints as a number, so a failure naming it says nothing", uint8(kind))
		}
	})
})

var _ = Describe("A zone taking a write", func() {
	It("moves its generation forward and acknowledges", func() {
		outcome := replicaRules[opWrite](replicaState{Zone: 1}, replicaOp{Generation: 7})

		Expect(outcome.Accepted).To(BeTrue())
		Expect(outcome.State.Generation).To(Equal(uint64(7)))
		Expect(outcome.State.Objects).To(Equal(1))
		Expect(outcome.Ack).To(BeTrue())
	})

	It("acknowledges a generation it already has without counting it twice", func() {
		atSeven := replicaState{Zone: 1, Generation: 7, Objects: 1}
		outcome := replicaRules[opWrite](atSeven, replicaOp{Generation: 7})

		Expect(outcome.Accepted).To(BeFalse())
		Expect(outcome.State).To(Equal(atSeven))
		Expect(outcome.Ack).To(BeTrue(),
			"a zone that already has the write has still answered for it")
	})
})

var _ = Describe("A zone taking a repair", func() {
	It("accepts only a generation greater than its own", func() {
		behind := replicaState{Zone: 2, Generation: 3}

		forward := replicaRules[opRepair](behind, replicaOp{Generation: 9})
		Expect(forward.Accepted).To(BeTrue())
		Expect(forward.State.Generation).To(Equal(uint64(9)))
		Expect(forward.Ack).To(BeFalse(),
			"a repair is not a write and is not counted towards anybody's quorum")

		backwards := replicaRules[opRepair](replicaState{Zone: 2, Generation: 9},
			replicaOp{Generation: 3})
		Expect(backwards.Accepted).To(BeFalse())
		Expect(backwards.State.Generation).To(Equal(uint64(9)),
			"a repair that could move a zone backwards is a repair that loses a write")
	})

	It("does not invent an object", func() {
		repaired := replicaRules[opRepair](replicaState{Zone: 2, Objects: 4}, replicaOp{Generation: 1})
		Expect(repaired.State.Objects).To(Equal(4),
			"the object already existed; the zone was behind on it, not missing it")
	})
})

var _ = Describe("A zone answering a probe", func() {
	It("reports rather than acknowledging, and moves nothing", func() {
		state := replicaState{Zone: 0, Generation: 5, Objects: 5}
		outcome := replicaRules[opProbe](state, replicaOp{})

		Expect(outcome.Report).To(BeTrue())
		Expect(outcome.Ack).To(BeFalse())
		Expect(outcome.State).To(Equal(state))
	})
})

var _ = Describe("The published view", func() {
	It("merges two halves neither goroutine can see together", func() {
		view := StoreView{StorageClass: "Glacial"}

		view = foldReport(view, storeReport{
			Kind: reportWrite, Generation: 4, Acks: 2, Objects: 4,
			Writable: true, Readable: true})
		Expect(view.Durable()).To(BeTrue())
		Expect(view.Serving()).To(BeTrue())
		Expect(view.LaggingZones).To(Equal(0))

		view = foldReport(view, storeReport{Kind: reportRepair, Lagging: 1})
		Expect(view.LaggingZones).To(Equal(1))
		Expect(view.Generation).To(Equal(uint64(4)),
			"the repairer's half says nothing about the coordinator's, and overwrites none of it")
	})

	It("never reports more acknowledgements than there are zones", func() {
		view := foldReport(StoreView{}, storeReport{Kind: reportWrite, Acks: 99})
		Expect(view.WriteAcks).To(Equal(zoneCount),
			"a card reporting durability nobody has is worse than one reporting less")

		negative := foldReport(StoreView{}, storeReport{Kind: reportWrite, Acks: -3})
		Expect(negative.WriteAcks).To(Equal(0))
	})
})
