package yakshave

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

// The card is asserted on rendered output and never on source. That is not a
// stylistic preference: a generated card and a hand-written one share no file
// names, no function names and no formatting, and holding both to what comes out
// of Render is the only assertion that is fair to both. Everything here is
// therefore written the way the BEN probe's own fragment fixture will be.

// card renders the Yakshave card in the state the two streams would have left
// it in, and is where the seam is exercised: the field maps come from seam.go
// rather than from literals, so a renamed document field fails to compile.
func card(run RunView, quota QuotaView) widgettest.Rendered {
	GinkgoHelper()

	mounted, mountError := widgettest.Mount(context.Background(), NewYakshave[live.AnonymousIdentity]())
	Expect(mountError).ToNot(HaveOccurred())
	Expect(mounted.Apply(
		widgettest.Deliver(YakshaveEventRunAdvance, RunFields(run)),
		widgettest.Deliver(YakshaveEventQuotaUpdate, QuotaFields(quota)),
	)).To(BeEmpty(), "this card schedules no effect")

	markup, renderError := mounted.Render(context.Background())
	Expect(renderError).ToNot(HaveOccurred())
	return markup
}

// green is a run that cleared every stage.
func green(sequence uint64) RunView {
	return RunView{
		Sequence: sequence, Run: sequence, Stage: idleStage,
		Cleared: [stageCount]bool{true, true, true, true},
	}
}

var _ = Describe("The Yakshave card", func() {
	It("declares the two streams and the two events they deliver", func() {
		registration := NewYakshave[live.AnonymousIdentity]().Register()

		Expect(registration.Region).To(Equal("widget.candaws.yakshave"))
		Expect(registration.Events).To(BeEmpty(),
			"a read-only card sends nothing, so a browser may send it nothing")
		Expect(registration.Internal).To(ConsistOf(
			YakshaveEventRunAdvance, YakshaveEventQuotaUpdate))
		Expect(registration.Streams).To(HaveLen(2),
			"two streams, and only one of them can be the tick")

		// The seam fills exactly the fields the registration declares. This is
		// the assertion that catches a document field the host stopped writing:
		// the map and the payload are two spellings of one contract.
		for event, filled := range map[string]map[string]string{
			YakshaveEventRunAdvance:  RunFields(RunView{}),
			YakshaveEventQuotaUpdate: QuotaFields(QuotaView{}),
		} {
			declared, known := registration.Payload(event)
			Expect(known).To(BeTrue(), "no payload declared for %s", event)
			Expect(filled).To(HaveLen(len(declared)))
			for _, field := range declared {
				Expect(filled).To(HaveKey(field), "the seam does not fill %s of %s", field, event)
			}
		}
	})

	It("is a landmark with an accessible name, and names its region once", func() {
		markup := card(green(4), QuotaView{QuotaMinutes: 12})

		landmark, found := markup.Landmark()
		Expect(found).To(BeTrue())
		Expect(landmark.Element).To(Equal("aside"))
		Expect(landmark.LabelledBy).To(Equal(YakshaveTitleID))
		Expect(landmark.Named).To(BeTrue(),
			"a nameless aside inside sectioning content is not a landmark at all")
		Expect(markup.Count(`"`+YakshaveRegion+`"`)).To(Equal(1),
			"a card that named its own region twice patches correctly and cannot be addressed")
	})

	It("renders no source line, because the document declares no source slot", func() {
		markup := card(green(1), QuotaView{QuotaMinutes: 5})
		Expect(markup.Has("widget-source")).To(BeFalse(),
			"the slot is 0..1 and this document leaves it empty; the absence of a slot "+
				"is independent of the absence of a source, and this card has two")
	})

	It("renders four stats in declaration order", func() {
		markup := card(
			RunView{Sequence: 9, Stage: "build", Retries: 2,
				Cleared: [stageCount]bool{true, false, false, false}},
			QuotaView{QueueMinutes: 6, QuotaMinutes: 31})

		Expect(markup.Elements("widget-stat")).To(Equal(4))
		Expect(markup.InOrder(
			"stage build",
			"6 minutes queued",
			"2 automatic retries",
			"31 minutes of quota left",
		)).To(BeTrue(), "stats render in the order the document declares them")
	})

	It("interpolates a text state field into a literal label", func() {
		// currentStage is the only `text` field in the fleet and the first
		// anywhere: nothing in the three shipped exemplars carries a string
		// through state, so this is the assertion that it arrives at all.
		markup := card(RunView{Sequence: 2, Stage: "checkout"}, QuotaView{QuotaMinutes: 3})
		Expect(markup.Has("stage checkout")).To(BeTrue())
	})

	It("renders both legend entries in declaration order", func() {
		markup := card(green(1), QuotaView{QuotaMinutes: 8})
		Expect(markup.Elements("widget-legend-entry")).To(Equal(2))
		Expect(markup.InOrder("artifact", "rollback")).To(BeTrue())
	})

	It("turns the indicator on a green pipeline and off a red one", func() {
		shipping := card(green(3), QuotaView{QuotaMinutes: 20})
		Expect(shipping.Has(`data-tone="positive"`)).To(BeTrue())
		Expect(shipping.Has("pipeline green")).To(BeTrue())

		red := card(
			RunView{Sequence: 4, Stage: idleStage,
				Cleared: [stageCount]bool{true, false, false, false}},
			QuotaView{QuotaMinutes: 20})
		Expect(red.Has(`data-tone="warning"`)).To(BeTrue())
		Expect(red.Has("build failed")).To(BeTrue())
	})

	It("reports an exhausted quota ahead of anything else that is wrong", func() {
		// The guard order is the meaning. A pipeline with no minutes left is
		// stopped for a reason no stage caption explains, so it is the first
		// clause of the status binding and the only one that can be reached
		// from this state.
		markup := card(
			RunView{Sequence: 5, Stage: idleStage,
				Cleared: [stageCount]bool{true, false, false, false}},
			QuotaView{QuotaMinutes: 0})
		Expect(markup.Has("quota exhausted")).To(BeTrue())
		Expect(markup.Has("build failed")).To(BeFalse())
		Expect(markup.Has("the run is stopped because the minute quota is exhausted")).To(BeTrue(),
			"the scene's own text alternative says what the picture is showing")
	})

	It("opens the motion gate only on a run that is shipping", func() {
		shipping := card(green(6), QuotaView{QuotaMinutes: 14})
		Expect(shipping.MotionOpen()).To(BeTrue())
		Expect(shipping.Pulses()).To(Equal(3),
			"three staggered artifact pulses; the rollback edge carries no pulse, and "+
				"the document says why")

		held := card(RunView{Sequence: 7, Stage: "test"}, QuotaView{QuotaMinutes: 14})
		Expect(held.MotionOpen()).To(BeFalse())
		Expect(held.Pulses()).To(Equal(3),
			"a shut gate is an attribute on the root rather than markup that vanishes: "+
				"the scene's declared motion does not depend on state")
	})

	It("declares dirty for every transition that moves its markup", func() {
		instance := NewYakshave[live.AnonymousIdentity]()
		before := YakshaveState{}
		after, _ := instance.Reduce(before,
			widgettest.Deliver(YakshaveEventRunAdvance, RunFields(green(1))))

		var declarer widget.IDirtyDeclarer[YakshaveState] = instance
		Expect(declarer.Dirty(before, after)).To(BeTrue())
		Expect(declarer.Dirty(after, after)).To(BeFalse(),
			"equal state renders byte-identical markup, and a patch for it is a patch nobody needs")
	})
})
