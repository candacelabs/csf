package widgettest_test

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/pkg/widget/widgettest"
)

// The double below is a widget written the way a generated one is: a region, a
// state, an event that writes into it, and a render that is a pure function of
// state. It is hand-written rather than one of the generated exemplars because
// pkg/ may not depend on examples/ — and because a double this package's own
// specifications can break on purpose is exactly what a card assertion needs to
// be checked against.
const (
	probeRegion  = "widget.widgettest.probe"
	probeTitleID = "probe-title"
	probeEvent   = "widget.widgettest.probe.report"
	probeField   = "depth"
)

// probeState is the double's own state.
type probeState struct {
	// Depth is the one number the card draws.
	Depth int
}

// probeWidget is the double. Its markup carries one of everything a card
// assertion asks about: a named landmark, a class that is a prefix of another
// class, an ordered stat list, and a motion gate.
type probeWidget struct {
	// nameless renders a root with no accessible name, which is the failure
	// [widgettest.Rendered.Landmark] exists to be able to report.
	nameless bool
}

var _ widget.IWidget[probeState, live.AnonymousIdentity] = probeWidget{}

func (instance probeWidget) Register() widget.Registration {
	return widget.Registration{
		Name:     "Probe",
		Region:   probeRegion,
		Internal: []string{probeEvent},
		Payloads: []widget.EventPayload{{Event: probeEvent, Fields: []string{probeField}}},
	}
}

func (instance probeWidget) Mount(
	ctx context.Context, session live.Session[live.AnonymousIdentity],
) (probeState, []live.Effect[live.AnonymousIdentity], error) {
	return probeState{}, nil, nil
}

func (instance probeWidget) Reduce(
	state probeState, event live.Event,
) (probeState, []live.Effect[live.AnonymousIdentity]) {
	current := state
	if event.Name != probeEvent {
		return current, nil
	}
	if raw, present := event.Fields.Lookup(probeField); present {
		if depth, parseError := strconv.Atoi(raw); parseError == nil {
			current.Depth = depth
		}
	}
	return current, nil
}

func (instance probeWidget) Render(state probeState) templ.Component {
	label := probeTitleID
	if instance.nameless {
		label = "probe-title-absent"
	}
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		_, writeError := io.WriteString(writer, fmt.Sprintf(
			`<aside data-fragment="%s" aria-labelledby="%s" data-motion="%s">`+
				`<h2 id="%s">Probe</h2>`+
				`<ul><li class="widget-stat">depth %d</li>`+
				`<li class="widget-stat">region %s</li></ul>`+
				`<circle class="widget-pulse widget-pulse-forward"></circle>`+
				`<circle class="widget-pulse-forward"></circle>`+
				`</aside>`,
			probeRegion, label, strconv.FormatBool(state.Depth > 0),
			probeTitleID, state.Depth, probeRegion))
		return writeError
	})
}

func (instance probeWidget) Unmount(ctx context.Context, session live.Session[live.AnonymousIdentity], state probeState) {
}

func (instance probeWidget) Snapshot(state probeState) widget.Snapshot {
	return widget.Snapshot{Widget: "Probe", Fields: []widget.SnapshotField{
		{Name: "depth", Value: strconv.Itoa(state.Depth)},
	}}
}

// mount is one card, mounted and ready to drive.
func mount(instance probeWidget) *widgettest.Card {
	GinkgoHelper()
	card, mountError := widgettest.Mount(context.Background(), instance)
	Expect(mountError).ToNot(HaveOccurred())
	return card
}

// render drives a card and returns what a viewer would receive.
func render(card *widgettest.Card, events ...live.Event) widgettest.Rendered {
	GinkgoHelper()
	Expect(card.Apply(events...)).To(BeEmpty(), "this widget schedules no effect")
	markup, renderError := card.Render(context.Background())
	Expect(renderError).ToNot(HaveOccurred())
	return markup
}

var _ = Describe("A mounted card", func() {
	It("renders the region the widget registered", func() {
		card := mount(probeWidget{})
		Expect(card.Region()).To(Equal(probeRegion))
		Expect(render(card).Count(probeRegion)).To(Equal(2),
			"the double names its region on the root and in a stat, which is what "+
				"makes Count a different question from Has")
	})

	It("drives an event through the registry's own router", func() {
		card := mount(probeWidget{})
		markup := render(card, widgettest.Deliver(probeEvent, map[string]string{probeField: "12"}))
		Expect(markup.Has("depth 12")).To(BeTrue())
	})

	It("stamps the card's region on an event that named none", func() {
		card := mount(probeWidget{})
		// A live event carries a region and the frame boundary requires it, so
		// an event constructed without one is a spec bug the card fills in
		// rather than a delivery the router silently drops.
		Expect(card.Apply(widgettest.Deliver(probeEvent, map[string]string{probeField: "3"}))).To(BeEmpty())
		markup, renderError := card.Render(context.Background())
		Expect(renderError).ToNot(HaveOccurred())
		Expect(markup.Has("depth 3")).To(BeTrue())
	})

	It("ignores an event no registration names", func() {
		card := mount(probeWidget{})
		markup := render(card, widgettest.Deliver("widget.widgettest.probe.forged",
			map[string]string{probeField: "99"}))
		Expect(markup.Has("depth 0")).To(BeTrue(),
			"an unrouted event reaches no reducer, which is the default-deny the registry owns")
	})
})

var _ = Describe("Rendered markup", func() {
	It("counts elements by whole class token rather than by substring", func() {
		markup := render(mount(probeWidget{}))
		Expect(markup.Elements("widget-pulse")).To(Equal(1),
			"widget-pulse is a prefix of widget-pulse-forward, and a substring count says two")
		Expect(markup.Elements("widget-pulse-forward")).To(Equal(2))
		Expect(markup.Pulses()).To(Equal(1))
	})

	It("reads declaration order rather than mere presence", func() {
		markup := render(mount(probeWidget{}), widgettest.Deliver(probeEvent,
			map[string]string{probeField: "5"}))
		Expect(markup.InOrder("depth 5", "region "+probeRegion)).To(BeTrue())
		Expect(markup.InOrder("region "+probeRegion, "depth 5")).To(BeFalse(),
			"a set of independent presence checks cannot see order, which is the whole point")
		Expect(markup.InOrder("depth 5", "absent")).To(BeFalse())
	})

	It("reports a landmark that is named, and one that is not", func() {
		named, found := render(mount(probeWidget{})).Landmark()
		Expect(found).To(BeTrue())
		Expect(named.Element).To(Equal("aside"))
		Expect(named.LabelledBy).To(Equal(probeTitleID))
		Expect(named.Named).To(BeTrue())

		unnamed, alsoFound := render(mount(probeWidget{nameless: true})).Landmark()
		Expect(alsoFound).To(BeTrue())
		Expect(unnamed.Named).To(BeFalse(),
			"an aside whose aria-labelledby points at nothing is not a landmark")
	})

	It("reports no landmark for markup with no element", func() {
		_, found := widgettest.Rendered("   ").Landmark()
		Expect(found).To(BeFalse())
	})

	It("reads the motion gate", func() {
		Expect(render(mount(probeWidget{})).MotionOpen()).To(BeFalse())
		Expect(render(mount(probeWidget{}), widgettest.Deliver(probeEvent,
			map[string]string{probeField: "1"})).MotionOpen()).To(BeTrue())
	})
})

var _ = Describe("Mounting a widget whose region the registry cannot serve", func() {
	It("reports the disagreement rather than rendering something else", func() {
		_, mountError := widgettest.Mount(context.Background(), regionlessWidget{})
		Expect(mountError).To(HaveOccurred())
	})
})

// regionlessWidget registers a region the registry refuses, so Mount has a
// failure to report that is not a render.
type regionlessWidget struct{ probeWidget }

func (instance regionlessWidget) Register() widget.Registration {
	registration := instance.probeWidget.Register()
	registration.Region = "not a region"
	return registration
}
