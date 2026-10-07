// Copyright 2026 Candace Labs

package affect_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/affect"
)

var _ = Describe("Read", func() {
	// The back-test the ticket names: the operator's own messages of the day
	// it was filed, each with what it must read as.
	DescribeTable("reads the ticket's known events",
		func(message string, strain affect.Strain, kind affect.Kind, correction bool) {
			reading := affect.Read(message)
			Expect(reading.Strain).To(Equal(strain))
			Expect(reading.Kind).To(Equal(kind))
			Expect(reading.Correction).To(Equal(correction))
		},
		Entry("a shouted insult", "THE ORCHESTRATOR UI STILL SUCKS BALLS", affect.StrainHigh, affect.KindDirective, true),
		Entry("a shouted urgent ask", "MAKE IT LOOK GOOD ASAP", affect.StrainHigh, affect.KindDirective, false),
		Entry("an insult in lowercase", "it says that session is dead moron", affect.StrainHigh, affect.KindDirective, true),
		Entry("vision, though it opens with no", "no action needed, just laying out the vision", affect.StrainLow, affect.KindVision, false),
		Entry("a reproach after fillers", "uhhh how did you accept this question without follow up questions", affect.StrainLow, affect.KindQuestion, true),
		Entry("a mining flag", "mineable offense lol prose instead of structural enforcement", affect.StrainLow, affect.KindDirective, true),
		Entry("a plain directive", "make a pr when done", affect.StrainLow, affect.KindDirective, false),
		Entry("a plain question", "is there a way to see the logs", affect.StrainLow, affect.KindQuestion, false),
	)

	It("calls the ticket's shouted and insulting events dissatisfied, and the plain ask not", func() {
		Expect(affect.Read("MAKE IT LOOK GOOD ASAP").Dissatisfied()).To(BeTrue())
		Expect(affect.Read("it says that session is dead moron").Dissatisfied()).To(BeTrue())
		Expect(affect.Read("make a pr when done").Dissatisfied()).To(BeFalse())
	})

	It("measures the features it reads from", func() {
		Expect(affect.Read("MAKE IT LOOK GOOD ASAP").Features).To(Equal(affect.Features{
			CapsRatio: 1, Letters: 18, Shouted: true, Urgency: 1,
		}))
	})

	It("reads a self-report as high strain whatever else the message says", func() {
		reading := affect.Read("look man i'm going through it emotionally, record that structurally")
		Expect(reading.SelfReport).To(BeTrue())
		Expect(reading.Strain).To(Equal(affect.StrainHigh))
		Expect(reading.Correction).To(BeFalse())
	})

	It("sets code and links aside: they are data, not the operator's words", func() {
		reading := affect.Read("see `NO_PROXY=BAD` and https://example.com/WHY-DID-IT-FAIL then ```\nSTOP ALL\n``` ok")
		Expect(reading.Features.Shouted).To(BeFalse())
		Expect(reading.Correction).To(BeFalse())
		Expect(reading.Strain).To(Equal(affect.StrainLow))
	})

	It("does not read a short capitalised word as shouting", func() {
		Expect(affect.Read("OK go").Features.Shouted).To(BeFalse())
		Expect(affect.Read("").Strain).To(Equal(affect.StrainLow))
	})
})
