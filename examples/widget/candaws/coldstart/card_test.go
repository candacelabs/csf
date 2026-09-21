package coldstart

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

// The probe's twin: a request/response round trip, asserted on rendered output
// only. A node with no caption, one edge carrying three channels, a control that
// carries no pressed state, and two indicators reading two different predicates
// rather than two views of one.

// card renders the Coldstart card in the state the dispatcher stream would have
// left it in.
func card(view PoolView, sent ...live.Event) widgettest.Rendered {
	GinkgoHelper()

	mounted, mountError := widgettest.Mount(context.Background(), NewColdstart[live.AnonymousIdentity]())
	Expect(mountError).ToNot(HaveOccurred())
	Expect(mounted.Apply(append(
		[]live.Event{widgettest.Deliver(ColdstartEventPoolReport, ReportFields(view))},
		sent...)...)).To(BeEmpty(), "this card schedules no effect")

	markup, renderError := mounted.Render(context.Background())
	Expect(renderError).ToNot(HaveOccurred())
	return markup
}

// serving is a pool with a warm instance, nothing queued and a live dispatcher.
func serving(sequence uint64) PoolView {
	return PoolView{
		Sequence: sequence, RuntimeName: "candace/go1.26", WarmInstances: 2,
		LiveInstances: 2, Queued: 0, ColdStartMillis: 800, DispatcherUp: true,
	}
}

var _ = Describe("The Coldstart card", func() {
	It("registers one command and one stream-owned event", func() {
		registration := NewColdstart[live.AnonymousIdentity]().Register()

		Expect(registration.Events).To(ConsistOf(ColdstartEventPrewarm))
		Expect(registration.Internal).To(ConsistOf(ColdstartEventPoolReport))

		declared, known := registration.Payload(ColdstartEventPoolReport)
		Expect(known).To(BeTrue())
		filled := ReportFields(PoolView{})
		Expect(filled).To(HaveLen(len(declared)))
		for _, field := range declared {
			Expect(filled).To(HaveKey(field), "the seam does not fill %s", field)
		}

		_, carries := registration.Payload(ColdstartEventPrewarm)
		Expect(carries).To(BeFalse(),
			"a command carries no fields; what warming an instance means is the host's")
	})

	It("draws one node with no caption at all", func() {
		markup := card(serving(3))
		Expect(markup.Elements("widget-node")).To(Equal(4))
		Expect(markup.Elements("widget-node-caption")).To(Equal(3),
			"caption is the one optional clause in a node block, and the caller omits it")
		Expect(markup.Has("caller")).To(BeTrue())
	})

	It("carries three channels on one edge", func() {
		markup := card(serving(4))
		Expect(markup.Elements("widget-legend-entry")).To(Equal(3))
		Expect(markup.InOrder("invoke", "result", "start-up")).To(BeTrue())
		Expect(markup.Pulses()).To(Equal(6))
		Expect(markup.Elements("widget-orbit")).To(Equal(1))
	})

	It("gives its control no pressed state", func() {
		markup := card(serving(5))
		Expect(markup.Elements("widget-control")).To(Equal(1))
		Expect(markup.Has("Keep one instance warm")).To(BeTrue())
		Expect(markup.Has("aria-pressed")).To(BeFalse(),
			"the control declares no pressedWhen, so the control and the event are "+
				"fully decoupled from the widget's own flags")

		// And pressing it changes nothing here, which is the same statement
		// from the other side.
		commanded := card(serving(5), widgettest.Deliver(ColdstartEventPrewarm, nil))
		Expect(commanded.String()).To(Equal(markup.String()))
	})

	It("reads two different predicates in its two indicators", func() {
		warmAndIdle := card(serving(6))
		Expect(warmAndIdle.Elements("widget-indicator")).To(Equal(2))
		Expect(warmAndIdle.InOrder("2 instances warm", "no invocations queued")).To(BeTrue())
		Expect(warmAndIdle.Count(`data-tone="positive"`)).To(Equal(2))

		// Cold with a backlog: both indicators turn, and for two unrelated
		// reasons — which is what "two views of one predicate" would not do.
		cold := card(PoolView{
			Sequence: 7, RuntimeName: "candace/go1.26", WarmInstances: 0,
			Queued: 3, ColdStartMillis: 800, DispatcherUp: true})
		Expect(cold.Has("scaled to zero")).To(BeTrue())
		Expect(cold.Has("3 invocations waiting on a start-up")).To(BeTrue())
		Expect(cold.Count(`data-tone="warning"`)).To(Equal(2))

		// Warm with a backlog: one turns and one does not.
		mixed := card(PoolView{
			Sequence: 8, RuntimeName: "candace/go1.26", WarmInstances: 1,
			Queued: 2, ColdStartMillis: 800, DispatcherUp: true})
		Expect(mixed.Count(`data-tone="positive"`)).To(Equal(1))
		Expect(mixed.Count(`data-tone="warning"`)).To(Equal(1))
	})

	It("renders three stats, the first a literal carrying the runtime name", func() {
		markup := card(PoolView{
			Sequence: 9, RuntimeName: "candace/go1.26", WarmInstances: 1,
			Queued: 4, ColdStartMillis: 800, DispatcherUp: true})

		Expect(markup.Elements("widget-stat")).To(Equal(3))
		Expect(markup.InOrder(
			"runtime candace/go1.26",
			"800 ms start-up, not being paid",
			"4 queued",
		)).To(BeTrue())

		// The one stat whose text depends on whether anybody is paying for it.
		scaled := card(PoolView{
			Sequence: 10, RuntimeName: "candace/go1.26", ColdStartMillis: 800, DispatcherUp: true})
		Expect(scaled.Has("800 ms start-up, being paid")).To(BeTrue())
	})

	It("captions the dispatcher from the three states it can be in", func() {
		Expect(card(serving(11)).Has("routing")).To(BeTrue())

		draining := serving(12)
		draining.Draining = true
		Expect(card(draining).Has("draining")).To(BeTrue())

		down := serving(13)
		down.DispatcherUp = false
		Expect(card(down).Has("closed")).To(BeTrue())
		Expect(card(down).Has("the dispatcher is not accepting invocations")).To(BeTrue())
	})

	It("opens the motion gate only on a dispatcher with something warm", func() {
		Expect(card(serving(14)).MotionOpen()).To(BeTrue())

		cold := serving(15)
		cold.WarmInstances = 0
		Expect(card(cold).MotionOpen()).To(BeFalse())
		Expect(card(cold).Has("every instance is cold and the next invocation pays the start-up cost")).
			To(BeTrue())

		down := serving(16)
		down.DispatcherUp = false
		Expect(card(down).MotionOpen()).To(BeFalse())
	})
})
