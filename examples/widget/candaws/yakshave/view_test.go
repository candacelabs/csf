package yakshave

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The view is folded by a pure function, so every rule about what the card may
// show is driven from a literal here. Not one specification in this file starts
// a goroutine, and that is the property being demonstrated as much as the rules
// are: a pipeline standing up to reach a state is a pipeline whose timing has to
// be waited out before anything can be asserted.

// accepted is the report a stage sends when it takes an artifact.
func accepted(run uint64, at stageID, attempt int) stageReport {
	return stageReport{Run: run, Stage: at, Attempt: attempt, Busy: true}
}

// finished is the report a stage sends when it is done with one.
func finished(run uint64, at stageID, attempt int, cleared bool) stageReport {
	return stageReport{Run: run, Stage: at, Attempt: attempt, Cleared: cleared}
}

// through folds a whole run's worth of reports, stopping at the first stage that
// does not clear — which is what the chain itself does.
func through(view RunView, run uint64, attempt int, failAt stageID, fails bool) RunView {
	for index := range stageCount {
		at := stageID(index)
		view = advanceView(view, accepted(run, at, attempt))
		cleared := !(fails && at == failAt)
		view = advanceView(view, finished(run, at, attempt, cleared))
		if !cleared {
			return view
		}
	}
	return view
}

var _ = Describe("The stage registry", func() {
	// A map can be enumerated and a method set cannot, which is the whole
	// reason the chain is a registry: this assertion is unwritable against a
	// switch, and without it a fifth stage is a stage nothing ever runs.
	It("has exactly one rule per stage, and no rule for anything else", func() {
		Expect(stageWorks).To(HaveLen(stageCount))
		Expect(stageNames).To(HaveLen(stageCount))
		for index := range stageCount {
			Expect(stageWorks).To(HaveKey(stageID(index)), "no rule runs stage %d", index)
			Expect(stageNames).To(HaveKey(stageID(index)), "stage %d has no name", index)
		}
	})

	It("names every stage it dispatches on", func() {
		for index := range stageCount {
			Expect(stageID(index).String()).ToNot(ContainSubstring("stage "),
				"stage %d prints as a number, so a failure naming it says nothing", index)
		}
	})

	It("clears every stage at a failure rate of zero and none at one", func() {
		// The draws bracket the interval a random stream can produce: 0 is the
		// lowest a draw can be and the ceiling is what a rate of 1 has to beat.
		for index := range stageCount {
			work := stageWorks[stageID(index)]
			Expect(work(artifact{}, 0, 0)).To(BeTrue(),
				"stage %d fails on the luckiest possible draw at a rate of zero", index)
			Expect(work(artifact{}, 0.999999, 1)).To(BeFalse(),
				"stage %d ships on the unluckiest draw at a rate of one, so "+
					"'never ships' is not a state this engine can reach", index)
		}
	})

	It("is kinder to a retried test than to a first attempt", func() {
		work := stageWorks[stageTest]
		Expect(work(artifact{Attempt: 0}, 0.5, 0.4)).To(BeFalse())
		Expect(work(artifact{Attempt: 1}, 0.5, 0.4)).To(BeTrue(),
			"a retry that could not pass anything a first attempt could not is not a retry policy")
	})
})

var _ = Describe("The published view", func() {
	It("marks a stage cleared only after it has finished", func() {
		view := advanceView(RunView{Stage: idleStage}, accepted(1, stageCheckout, 0))
		Expect(view.Stage).To(Equal("checkout"))
		Expect(view.Cleared[stageCheckout]).To(BeFalse(),
			"a stage that has an artifact in hand has not cleared it")

		view = advanceView(view, finished(1, stageCheckout, 0, true))
		Expect(view.Cleared[stageCheckout]).To(BeTrue())
	})

	It("never shows a stage cleared over a predecessor that is not", func() {
		// The whole run, then the build failing on the next one. This is the
		// invariant the Yakshave document records as an engine fact rather than
		// a document one: W415 cannot see a chain.
		green := through(RunView{Stage: idleStage}, 1, 0, stageCheckout, false)
		Expect(green.Green()).To(BeTrue())
		Expect(green.Ordered()).To(BeTrue())

		red := through(green, 2, 0, stageBuild, true)
		Expect(red.Ordered()).To(BeTrue())
		Expect(red.Cleared[stageCheckout]).To(BeTrue())
		Expect(red.Cleared[stageBuild]).To(BeFalse())
		Expect(red.Cleared[stageTest]).To(BeFalse(),
			"the test stage did not run on this attempt, so its previous answer is not an answer")
		Expect(red.Cleared[stageDeploy]).To(BeFalse())
	})

	It("starts a retry from nothing cleared", func() {
		green := through(RunView{Stage: idleStage}, 1, 0, stageCheckout, false)
		retrying := advanceView(green, accepted(1, stageCheckout, 1))

		Expect(retrying.Retries).To(Equal(1))
		Expect(retrying.Cleared).To(Equal([stageCount]bool{}),
			"a retry that kept the last attempt's flags would show stages it is about to rerun")
	})

	It("goes idle when the gate finishes and when any stage fails", func() {
		green := through(RunView{Stage: idleStage}, 1, 0, stageCheckout, false)
		Expect(green.Stage).To(Equal(idleStage))

		red := through(RunView{Stage: idleStage}, 2, 0, stageTest, true)
		Expect(red.Stage).To(Equal(idleStage),
			"a chain nobody is holding an artifact in is idle, however it got that way")
	})

	It("carries the run identity the chain is working", func() {
		view := through(RunView{Stage: idleStage}, 7, 0, stageCheckout, false)
		Expect(view.Run).To(Equal(uint64(7)))
	})
})
