package blobfish

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

// The storage-shaped card, asserted on rendered output only: two orbits, four
// stats, a predicate graph two levels deep, and a chrome with no source slot on
// a widget that does have a source.

// card renders the Blobfish card in the state the replica stream would have
// left it in.
func card(view StoreView) widgettest.Rendered {
	GinkgoHelper()

	mounted, mountError := widgettest.Mount(context.Background(), NewBlobfish[live.AnonymousIdentity]())
	Expect(mountError).ToNot(HaveOccurred())
	Expect(mounted.Apply(
		widgettest.Deliver(BlobfishEventReplicaReport, ReportFields(view)),
	)).To(BeEmpty(), "this card schedules no effect")

	markup, renderError := mounted.Render(context.Background())
	Expect(renderError).ToNot(HaveOccurred())
	return markup
}

// durable is a store serving both quorums with nothing behind.
func durable(sequence uint64) StoreView {
	return StoreView{
		Sequence: sequence, Generation: sequence, StorageClass: "Glacial",
		Objects: 41, WriteAcks: 3, LaggingZones: 0, Writable: true, Readable: true,
	}
}

var _ = Describe("The Blobfish card", func() {
	It("declares one stream and no browser-sendable event", func() {
		registration := NewBlobfish[live.AnonymousIdentity]().Register()

		Expect(registration.Events).To(BeEmpty())
		Expect(registration.Internal).To(ConsistOf(BlobfishEventReplicaReport))
		Expect(registration.Streams).To(HaveLen(1))

		declared, known := registration.Payload(BlobfishEventReplicaReport)
		Expect(known).To(BeTrue())
		filled := ReportFields(StoreView{})
		Expect(filled).To(HaveLen(len(declared)))
		for _, field := range declared {
			Expect(filled).To(HaveKey(field), "the seam does not fill %s", field)
		}
	})

	It("renders no source line even though it has a source", func() {
		markup := card(durable(3))
		Expect(markup.Has("widget-source")).To(BeFalse(),
			"the absence of the slot is not a proxy for the absence of a source; "+
				"this card has one and does not render a line for it")
		landmark, found := markup.Landmark()
		Expect(found).To(BeTrue())
		Expect(landmark.Named).To(BeTrue())
	})

	It("draws two orbits", func() {
		markup := card(durable(4))
		Expect(markup.Elements("widget-orbit")).To(Equal(2),
			"two orbits used as tiers rather than as decoration; which ring is which "+
				"is decided by declaration order, and the document records that as a gap")
	})

	It("renders four stats, the first a literal carrying the storage class", func() {
		markup := card(StoreView{
			Sequence: 5, Generation: 5, StorageClass: "Deep Glacial",
			Objects: 12, WriteAcks: 2, LaggingZones: 1, Writable: true, Readable: true})

		Expect(markup.Elements("widget-stat")).To(Equal(4),
			"four stats, the most of any card with no controls")
		Expect(markup.InOrder(
			"storage class Deep Glacial",
			"12 objects stored",
			"2 of 3 zones acknowledged",
			"1 zones behind",
		)).To(BeTrue())
	})

	It("holds a predicate graph two levels deep", func() {
		// `durable` requires `serving`, `quorumWrites` and `consistent`, and
		// each of those is itself composed. Example 01's `live` composes flag
		// fields only, so this is the first card anywhere whose gate depends on
		// a predicate that depends on a predicate.
		whole := card(durable(6))
		Expect(whole.MotionOpen()).To(BeTrue())
		Expect(whole.Has("quorum acknowledged")).To(BeTrue())
		Expect(whole.Has(`data-tone="positive"`)).To(BeTrue())

		// Each leaf of the graph, alone, closes the gate.
		for _, broken := range []struct {
			what string
			view StoreView
			says string
		}{
			{"a read that did not reach its quorum",
				StoreView{Sequence: 7, StorageClass: "Glacial", WriteAcks: 3,
					Writable: true, Readable: false}, "quorum acknowledged"},
			{"a write that did not reach its quorum",
				StoreView{Sequence: 8, StorageClass: "Glacial", WriteAcks: 1,
					Writable: false, Readable: true}, "writes refused"},
			{"a zone that is behind",
				StoreView{Sequence: 9, StorageClass: "Glacial", WriteAcks: 3,
					LaggingZones: 1, Writable: true, Readable: true}, "1 zones repairing"},
		} {
			markup := card(broken.view)
			Expect(markup.MotionOpen()).To(BeFalse(), "the gate stayed open on %s", broken.what)
			Expect(markup.Has(broken.says)).To(BeTrue(), "for %s", broken.what)
		}
	})

	It("reads atLeast and atMost on two different fields", func() {
		waiting := card(StoreView{
			Sequence: 10, StorageClass: "Glacial", WriteAcks: 1, Writable: true, Readable: true})
		Expect(waiting.Has("waiting on the quorum")).To(BeTrue())

		met := card(durable(11))
		Expect(met.Has("3 of 3 zones acknowledged")).To(BeTrue())
		Expect(met.Has("no repairs pending")).To(BeTrue())
	})

	It("captions all three zones from one binding", func() {
		markup := card(durable(12))
		Expect(markup.Elements("widget-role-replica")).To(Equal(3))
		Expect(markup.InOrder("zone-a", "zone-b", "zone-c")).To(BeTrue())
		Expect(markup.Count("in sync")).To(Equal(3),
			"one bound caption for three interchangeable zones; three copies would be "+
				"three chances to disagree about one fact")
	})

	It("carries six pulses and three legend entries", func() {
		markup := card(durable(13))
		Expect(markup.Pulses()).To(Equal(6))
		Expect(markup.Elements("widget-legend-entry")).To(Equal(3))
		Expect(markup.InOrder("write", "ack", "repair")).To(BeTrue())
	})
})
