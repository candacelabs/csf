package main

import (
	"context"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/candaws/dashbored"
	"github.com/candacelabs/csf/examples/widget/candaws/queuecumber"
	"github.com/candacelabs/csf/examples/widget/candaws/yakshave"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/widget"
)

// This file is the end-to-end one: a real WebSocket, a real handshake, real
// protobuf frames, and five real engines at the other end of them.
//
// Every other specification in this fleet asserts on a reducer or on a render.
// Both are necessary and neither is sufficient: a fragment identifier that does
// not match a region, a mount that never schedules its effect, an effect the
// host cannot execute and an event name the registration does not carry are all
// mistakes those specifications hold constant and the wire does not.
const (
	// probeOrigin is what the handshake sends and what the configuration below
	// admits. It is a real allowlist rather than live.AnyOrigin because that is
	// the posture the host ships and the refusal is worth being able to reach.
	probeOrigin = "http://127.0.0.1:8081"

	// probePath is where the handler is mounted in this specification's router,
	// which is at the root: livetest dials the handler, not the host's mux.
	probePath = "/"

	// The frame origin kinds from proto/gotthlive/v1/frame.proto, spelled here
	// rather than imported: the .proto is the public artifact, and a value
	// arriving that is not named here fails with a number somebody can look up.
	originEffect = 2
	originMount  = 5

	// probePatience bounds a wait for one frame. It is generous against the
	// pace below, because a timeout here should mean "nothing arrived", never
	// "the machine was busy".
	probePatience = 30 * time.Second
)

// probeFleet is the fleet at a pace fast enough that a whole run, a whole lease
// cycle and a whole flush window happen between two assertions.
func probeFleet() *running {
	GinkgoHelper()

	fleet, buildError := build(20260902, 0.02, 1)
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := fleet.start(ctx)
	DeferCleanup(func() {
		cancel()
		// Five engines, five returns. Waiting for all of them is what makes a
		// goroutine still running when the next specification starts a failure
		// here rather than a race report attributed to somebody else.
		for range 5 {
			Eventually(stopped, probePatience).Should(Receive())
		}
	})
	return fleet
}

// probeApp is this host's own widgets and this host's own effects, wired the way
// run wires them — the same registration set, the same six sources, the same
// command wrapper.
func probeApp(fleet *running) *live.App[widget.HostState, live.AnonymousIdentity] {
	GinkgoHelper()

	config, configError := hostWidgets().LiveConfig(widget.MountOptions[live.AnonymousIdentity]{
		Origins:      []string{probeOrigin},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
		Init:         fleet.sources,
	})
	Expect(configError).ToNot(HaveOccurred())
	config.Reduce = fleet.commands(config.Reduce)

	app, appError := live.New(config)
	Expect(appError).ToNot(HaveOccurred())
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		Expect(app.Close(ctx)).To(Succeed())
	})
	return app
}

// dialProbe opens one session against the handler, exactly as a browser does.
func dialProbe(app *live.App[widget.HostState, live.AnonymousIdentity]) *livetest.Client {
	GinkgoHelper()
	return livetest.NewClient(GinkgoTB(), app.Handler(), livetest.ClientOptions{
		Path:    probePath,
		Origin:  probeOrigin,
		Timeout: probePatience,
	})
}

// awaitRegion takes frames until one carries the named region's markup
// satisfying the predicate, acknowledging each patch on the way.
//
// The acknowledgement is what makes this a browser rather than a stalled one.
// livetest never acknowledges on its own, and a probe that inherited the silence
// would fill the outbound window in a second at this pace and then fail with "no
// frame arrived", which is a true statement about the wrong thing.
func awaitRegion(
	client *livetest.Client, region string, what string, match func(html string) bool,
) string {
	GinkgoHelper()
	frame := client.Await(what, probePatience, func(frame *livetest.Frame) bool {
		if frame.Patch == nil {
			return false
		}
		client.Ack(frame.Patch.ServerSeq)
		html, carried := frame.Patch.Fragment(region)
		return carried && match(html)
	})
	Expect(frame.Patch.Origin.Kind).To(BeNumerically("==", originEffect),
		"the patch was caused by something other than a stream")
	html, _ := frame.Patch.Fragment(region)
	return html
}

var _ = Describe("The CandaWS host, over a real WebSocket", func() {
	It("mounts every region with the page's own markup, before any engine has said anything", func() {
		snapshot := dialProbe(probeApp(probeFleet())).Snapshot()

		Expect(snapshot.Kind).To(Equal(livetest.FrameSnapshot))
		Expect(snapshot.Patch.Origin.Kind).To(BeNumerically("==", originMount))
		Expect(snapshot.Patch.FragmentIDs()).To(ConsistOf(
			yakshave.YakshaveRegion,
			queuecumber.QueuecumberRegion,
			"widget.candaws.blobfish",
			"widget.candaws.coldstart",
			dashbored.DashboredRegion,
		), "a mount renders every region, which is what makes the first paint the "+
			"bytes the first snapshot would produce")
	})

	It("delivers a pipeline stage the chain is actually in", func() {
		client := dialProbe(probeApp(probeFleet()))

		html := awaitRegion(client, yakshave.YakshaveRegion,
			"the pipeline card reporting a stage",
			func(html string) bool { return strings.Contains(html, "stage build") })
		Expect(html).To(MatchRegexp(`stage (checkout|build|test|deploy|idle)`),
			"the stat line carries the stage a goroutine is holding an artifact in")
		Expect(html).ToNot(ContainSubstring("stage </li>"),
			"a text state field that arrived empty is a seam that is not wired")
	})

	It("delivers an ingest rate the collectors actually produced", func() {
		client := dialProbe(probeApp(probeFleet()))

		html := awaitRegion(client, dashbored.DashboredRegion,
			"the console card reporting every collector",
			func(html string) bool { return strings.Contains(html, "3 collectors reporting") })
		Expect(html).To(MatchRegexp(`[1-9][0-9]* samples per second ingested`),
			"a fan-in that reported a rate of zero is a fan-in nothing reached")
		Expect(html).To(ContainSubstring("395 days retained"))
	})

	It("answers a browser's own event on the same connection", func() {
		client := dialProbe(probeApp(probeFleet()))
		awaitRegion(client, queuecumber.QueuecumberRegion, "a broker card with workers",
			func(html string) bool { return strings.Contains(html, "workers leasing") })

		// The region is not optional decoration: the frame schema requires a
		// non-empty fragment_id, and it is also what the registry routes on
		// before it looks at the wire name.
		client.Send(queuecumber.QueuecumberEventToggleIntake,
			queuecumber.QueuecumberRegion, nil)

		paused := client.Await("the broker card reporting itself paused", probePatience,
			func(frame *livetest.Frame) bool {
				if frame.Patch == nil {
					return false
				}
				client.Ack(frame.Patch.ServerSeq)
				html, carried := frame.Patch.Fragment(queuecumber.QueuecumberRegion)
				return carried && strings.Contains(html, "Resume intake")
			})
		html, _ := paused.Patch.Fragment(queuecumber.QueuecumberRegion)
		Expect(html).To(ContainSubstring(`aria-pressed="true"`))
		Expect(html).To(ContainSubstring(`data-motion="false"`),
			"the button closes the gate; it does not stop the broker")
		Expect(html).To(ContainSubstring("intake paused by operator"))
	})

	It("acts on a command that changes no widget state at all", func() {
		fleet := probeFleet()
		client := dialProbe(probeApp(fleet))
		awaitRegion(client, "widget.candaws.coldstart", "a pool card",
			func(html string) bool { return strings.Contains(html, "runtime ") })

		// The prewarm button emits an event the widget's reducer does nothing
		// with. What it means is the host's, and the evidence that the host
		// meant it is a warm floor the pool did not have before.
		client.Send(coldstartPrewarm, "widget.candaws.coldstart", nil)

		warmed := awaitRegion(client, "widget.candaws.coldstart",
			"a pool that has been asked to keep something warm",
			func(html string) bool { return strings.Contains(html, "instances warm") })
		Expect(warmed).To(MatchRegexp(`[1-9][0-9]* instances warm`))
	})

	It("runs the whole fleet in one process, and the scheduler can be asked how", func() {
		// The monolithic-microservices claim, made checkable. Five services,
		// one binary, one session — and a number that is the sum of the
		// goroutines each engine documents, plus the library's own per-session
		// and per-effect ones, plus whatever this test binary is doing.
		//
		// It is asserted as a range rather than a constant because the last of
		// those three is not this fleet's business, and because Coldstart's
		// instance goroutines come and go by design.
		before := runtime.NumGoroutine()
		client := dialProbe(probeApp(probeFleet()))
		awaitRegion(client, dashbored.DashboredRegion, "a live console",
			func(html string) bool { return strings.Contains(html, "collectors reporting") })

		during := runtime.NumGoroutine()
		AddReportEntry("goroutines", map[string]int{"before": before, "with the fleet and one session": during})
		Expect(during).To(BeNumerically(">", before+30),
			"five engines and six effect goroutines is not a handful")
		Expect(during).To(BeNumerically("<", before+120),
			"and it is not a thread per request either")
	})
})

// coldstartPrewarm is spelled here rather than imported, because importing the
// coldstart package into this file for one constant would make the import list
// claim a dependency this specification does not otherwise have.
const coldstartPrewarm = "widget.candaws.coldstart.prewarm"
