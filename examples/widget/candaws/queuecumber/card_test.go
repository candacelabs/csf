package queuecumber

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

// The widest card in the fleet, asserted on rendered output only. Four channels
// and therefore a four-entry legend, two indicators rather than one, two
// controls of two different kinds, seven pulses, and one role classifying three
// nodes.

// card renders the Queuecumber card in the state the broker stream would have
// left it in, after any events a specification wants to send from the browser.
func card(view BrokerView, sent ...live.Event) widgettest.Rendered {
	GinkgoHelper()

	mounted, mountError := widgettest.Mount(context.Background(), NewQueuecumber[live.AnonymousIdentity]())
	Expect(mountError).ToNot(HaveOccurred())
	Expect(mounted.Apply(append(
		[]live.Event{widgettest.Deliver(QueuecumberEventBrokerReport, ReportFields(view))},
		sent...)...)).To(BeEmpty(), "this card schedules no effect")

	markup, renderError := mounted.Render(context.Background())
	Expect(renderError).ToNot(HaveOccurred())
	return markup
}

// flowing is a broker with room, three workers leasing and nothing exhausted.
func flowing(sequence uint64) BrokerView {
	return BrokerView{
		Sequence: sequence, Accepting: true, Depth: 4, InFlight: 2,
		DeadLettered: 0, WorkersUp: 3,
	}
}

var _ = Describe("The Queuecumber card", func() {
	It("registers two browser-sendable events and one the stream owns", func() {
		registration := NewQueuecumber[live.AnonymousIdentity]().Register()

		Expect(registration.Events).To(ConsistOf(
			QueuecumberEventToggleIntake, QueuecumberEventRedriveDeadLetters),
			"the two controls are what a browser may send, and nothing else is")
		Expect(registration.Internal).To(ConsistOf(QueuecumberEventBrokerReport),
			"a browser posting the broker's own truth is a forgery the registry refuses")

		declared, known := registration.Payload(QueuecumberEventBrokerReport)
		Expect(known).To(BeTrue())
		filled := ReportFields(BrokerView{})
		Expect(filled).To(HaveLen(len(declared)))
		for _, field := range declared {
			Expect(filled).To(HaveKey(field), "the seam does not fill %s", field)
		}
	})

	It("renders the four-entry legend in declaration order", func() {
		markup := card(flowing(3))
		Expect(markup.Elements("widget-legend-entry")).To(Equal(4),
			"nothing until this fleet had rendered a legend long enough to wrap")
		Expect(markup.InOrder("enqueue", "lease", "ack", "redrive")).To(BeTrue())
	})

	It("renders both indicators, side by side rather than one replacing the other", func() {
		markup := card(flowing(4))
		Expect(markup.Elements("widget-indicator")).To(Equal(2))
		Expect(markup.InOrder("intake open", "no dead letters")).To(BeTrue())
		Expect(markup.Count(`data-tone="positive"`)).To(Equal(2))

		strained := card(BrokerView{Sequence: 5, Accepting: false, Depth: 16, DeadLettered: 2})
		Expect(strained.InOrder("intake closed", "2 messages exhausted their attempts")).To(BeTrue())
		Expect(strained.Count(`data-tone="warning"`)).To(Equal(2))
	})

	It("classifies three nodes with one role and captions them from one binding", func() {
		markup := card(flowing(6))
		Expect(markup.Elements("widget-role-handler")).To(Equal(3),
			"changing how a handler looks is one edit rather than three")
		Expect(markup.InOrder("worker-1", "worker-2", "worker-3")).To(BeTrue())
		Expect(markup.Count("leasing")).To(BeNumerically(">=", 3),
			"one bound label fills all three worker captions, so they cannot disagree")
	})

	It("renders three stats, one of which is a literal carrying a template", func() {
		markup := card(BrokerView{
			Sequence: 7, Accepting: true, Depth: 5, InFlight: 2,
			DeadLettered: 9, WorkersUp: 3})

		Expect(markup.Elements("widget-stat")).To(Equal(3))
		Expect(markup.InOrder("5 queued · 2 in flight", "3 workers leasing", "9 redriven")).To(BeTrue())
	})

	It("renders a source line, because this document fills the slot", func() {
		markup := card(flowing(8))
		Expect(markup.Has("Read-only broker stream")).To(BeTrue())
		Expect(markup.InOrder("Read-only broker stream", "Queuecumber")).To(BeTrue(),
			"the source is emitted before the title because that is the order it is read in")
	})

	It("gives the two controls two different relationships to state", func() {
		open := card(flowing(9))
		Expect(open.Elements("widget-control")).To(Equal(2))
		Expect(open.Has("Pause intake")).To(BeTrue())
		Expect(open.Has(`aria-pressed="false"`)).To(BeTrue())
		Expect(open.Count("aria-pressed")).To(Equal(1),
			"only the toggle has a pressed state; the redrive button changes nothing here")
		Expect(open.Has("Redrive the dead letters")).To(BeTrue())

		// The toggle changes the widget's own flag and the caption with it.
		paused := card(flowing(10), widgettest.Deliver(QueuecumberEventToggleIntake, nil))
		Expect(paused.Has("Resume intake")).To(BeTrue())
		Expect(paused.Has(`aria-pressed="true"`)).To(BeTrue())
		Expect(paused.Has("intake paused by operator")).To(BeTrue())

		// The command changes nothing at all. The widget declares that it can
		// be sent; what a redrive means is the host's, and this card cannot
		// tell whether one happened.
		commanded := card(flowing(10), widgettest.Deliver(QueuecumberEventRedriveDeadLetters, nil))
		Expect(commanded.String()).To(Equal(card(flowing(10)).String()),
			"an event that writes no state renders no differently, which is what "+
				"makes it a command rather than a control")
	})

	It("carries seven pulses, and gates them on a flowing queue", func() {
		markup := card(flowing(11))
		Expect(markup.Pulses()).To(Equal(7),
			"seven pulses is the most motion in the fleet; the redrive edge carries a "+
				"channel that animates never, and the document says why")
		Expect(markup.MotionOpen()).To(BeTrue())

		// The operator's pause closes the gate without stopping the broker,
		// which is what `forbids intakePaused` on the motion block means.
		paused := card(flowing(12), widgettest.Deliver(QueuecumberEventToggleIntake, nil))
		Expect(paused.MotionOpen()).To(BeFalse())
		Expect(paused.Pulses()).To(Equal(7))

		unstaffed := card(BrokerView{Sequence: 13, Accepting: true, Depth: 2})
		Expect(unstaffed.MotionOpen()).To(BeFalse())
		Expect(unstaffed.Has("no worker is leasing")).To(BeTrue())
	})

	It("reads a numeric bound in both directions", func() {
		// `atMost` shipped beside `atLeast` and had no caller anywhere until
		// this fleet. Both are in this document, on three different fields.
		empty := card(BrokerView{Sequence: 14, Accepting: true, WorkersUp: 3})
		Expect(empty.Has("queue empty")).To(BeTrue())
		Expect(empty.Has("empty")).To(BeTrue())

		full := card(flowing(15))
		Expect(full.Has("4 queued")).To(BeTrue())
		Expect(full.Has("queue empty")).To(BeFalse())
	})
})
