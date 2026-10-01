package dashbored

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

// The fleet's own console, asserted on rendered output only: five stats, two of
// them literal templates side by side, a five-node fan-in, and no reverse
// channel anywhere.

// card renders the Dashbored card in the state the aggregator stream would have
// left it in.
func card(view MetricsView, sent ...live.Event) widgettest.Rendered {
	GinkgoHelper()

	mounted, mountError := widgettest.Mount(context.Background(), NewDashbored[live.AnonymousIdentity]())
	Expect(mountError).ToNot(HaveOccurred())
	Expect(mounted.Apply(append(
		[]live.Event{widgettest.Deliver(DashboredEventScrapeReport, ReportFields(view))},
		sent...)...)).To(BeEmpty(), "this card schedules no effect")

	markup, renderError := mounted.Render(context.Background())
	Expect(renderError).ToNot(HaveOccurred())
	return markup
}

// observing is a pipeline with every collector reporting and nothing firing.
func observing(sequence uint64) MetricsView {
	return MetricsView{
		Sequence: sequence, CollectorsUp: 3, SamplesPerSecond: 42,
		RetentionDays: 395, QueryWindowHours: 2, AggregatorUp: true,
	}
}

var _ = Describe("The Dashbored card", func() {
	It("declares one toggle and one stream-owned event", func() {
		registration := NewDashbored[live.AnonymousIdentity]().Register()

		Expect(registration.Events).To(ConsistOf(DashboredEventToggleSilence))
		Expect(registration.Internal).To(ConsistOf(DashboredEventScrapeReport))

		declared, known := registration.Payload(DashboredEventScrapeReport)
		Expect(known).To(BeTrue())
		filled := ReportFields(MetricsView{})
		Expect(filled).To(HaveLen(len(declared)))
		for _, field := range declared {
			Expect(filled).To(HaveKey(field), "the seam does not fill %s", field)
		}
	})

	It("renders five stats, two of them literal templates side by side", func() {
		markup := card(observing(3))

		Expect(markup.Elements("widget-stat")).To(Equal(5),
			"five stats, the most in the fleet")
		Expect(markup.InOrder(
			"42 samples per second ingested",
			"3 collectors reporting",
			"395 days retained",
			"2 hours queryable",
			"no alert firing",
		)).To(BeTrue(),
			"the retention joke is rendered as data rather than written as a sentence")
	})

	It("draws a five-node fan-in with no reverse channel anywhere", func() {
		markup := card(observing(4))

		Expect(markup.Elements("widget-node")).To(Equal(5))
		Expect(markup.Elements("widget-role-probe")).To(Equal(3))
		Expect(markup.InOrder("collector-a", "collector-b", "collector-c",
			"aggregator", "alerter")).To(BeTrue())

		Expect(markup.Elements("widget-pulse-reverse")).To(Equal(0),
			"every other document in the fleet carries a reverse channel somewhere; "+
				"a metrics pipeline answers nobody")
		Expect(markup.Elements("widget-pulse-forward")).To(Equal(4))
		Expect(markup.Elements("widget-legend-entry")).To(Equal(2))
	})

	It("says nothing is wrong from a predicate with no requires clause", func() {
		// `quiet` is `forbids breaching` and nothing else, which is the natural
		// spelling for "nothing is wrong" and which nothing had used.
		quiet := card(observing(5))
		Expect(quiet.Has("no alert firing")).To(BeTrue())
		Expect(quiet.Has(`data-tone="positive"`)).To(BeTrue())
		Expect(quiet.Has("quiet")).To(BeTrue())

		breaching := observing(6)
		breaching.Breaching = true
		breaching.FiringAlert = "p99_latency"
		loud := card(breaching)
		Expect(loud.Has("firing: p99_latency")).To(BeTrue())
		Expect(loud.Has(`data-tone="warning"`)).To(BeTrue())
	})

	It("silences the alerter without stopping it", func() {
		breaching := observing(7)
		breaching.Breaching = true
		breaching.FiringAlert = "p99_latency"

		silenced := card(breaching, widgettest.Deliver(DashboredEventToggleSilence, nil))
		Expect(silenced.Has(`aria-pressed="true"`)).To(BeTrue())
		Expect(silenced.Has("Unsilence the alerter")).To(BeTrue())
		Expect(silenced.Has("alerts silenced by operator")).To(BeTrue())
		Expect(silenced.Has("firing: p99_latency")).To(BeFalse(),
			"a silenced alert still transitions in the engine and is only not shown here")

		loud := card(breaching)
		Expect(loud.Has(`aria-pressed="false"`)).To(BeTrue())
		Expect(loud.Has("Silence the alerter")).To(BeTrue())
	})

	It("gates its motion on every collector reporting", func() {
		Expect(card(observing(8)).MotionOpen()).To(BeTrue())

		short := observing(9)
		short.CollectorsUp = 2
		Expect(card(short).MotionOpen()).To(BeFalse())
		Expect(card(short).Has("2 collectors reporting · below the floor")).To(BeTrue())
		Expect(card(short).Has("at least one collector has stopped reporting")).To(BeTrue())
		Expect(card(short).Has("stalled")).To(BeTrue())

		down := observing(10)
		down.AggregatorUp = false
		Expect(card(down).MotionOpen()).To(BeFalse())
		Expect(card(down).Has("the aggregator is not accepting samples")).To(BeTrue())

		// Silencing closes the gate too. The document records that as a gap
		// rather than a design: silencing the alerter should stop the breach
		// pulse and not the sample pulses, and one gate governs both.
		Expect(card(observing(11), widgettest.Deliver(DashboredEventToggleSilence, nil)).
			MotionOpen()).To(BeFalse())
	})

	It("renders a source line before its title", func() {
		markup := card(observing(12))
		Expect(markup.InOrder("Read-only aggregator stream", "Dashbored")).To(BeTrue())
	})
})
