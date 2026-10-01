package main

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/patience"
)

// FR-62's five properties, measured on the frames.
//
// Three of them — which fragments a patch carries, which events a coalesced
// patch names, how many patches a client that stops acknowledging can be sent —
// are claims about what is on the wire, and an application cannot check any of
// them by looking at its own state. So these specs dial a real WebSocket against
// a real httptest server and decode what arrives with the reader in wire.go.
//
// The other two are the plain-HTMX region, which is a claim about what is NOT on
// the wire and about what the markup declares, and the high-frequency push,
// which is a claim about frames arriving with nothing sent to ask for them.
//
// README.md's table says which spec covers which property, and what was mutated
// to prove each of them can fail. A spec nobody has watched go red is a spec
// that has never been shown to test anything.

const (
	testOrigin = "http://dashboard.test"

	// settleIdle is how long silence has to last before a spec calls the
	// session quiet. It is measured on the frame channel rather than by
	// cancelling a read, which would close the session being measured.
	settleIdle = 250 * time.Millisecond
)

var actorAppliedBudget = patience.Budget{Within: 5 * time.Second}

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

// provenanceLog captures the library's transition rows.
//
// The rows are what make the coalescing spec an assertion about events rather
// than about counts: a client knows the client_ref it sent and the server
// assigns the event_id that ends up in a patch's contributing list, and the
// provenance row is the only place the two appear together. That is FR-41's
// "resolve the originating event from a captured frame" done by a spec, and it
// is the same lookup `go run . -provenance` gives an operator.
type provenanceLog struct {
	slog.Handler

	mu   sync.Mutex
	rows []provenanceRow
}

type provenanceRow struct {
	EventID   uint64
	ClientRef uint64
	PatchID   uint64
	Source    string
	Fragments []string
}

func newProvenanceLog() *provenanceLog {
	// The embedded handler discards: the rows are read structurally below, and
	// a suite that also printed them would bury the spec output.
	return &provenanceLog{Handler: slog.NewJSONHandler(io.Discard, nil)}
}

func (p *provenanceLog) Enabled(context.Context, slog.Level) bool { return true }

func (p *provenanceLog) WithAttrs([]slog.Attr) slog.Handler { return p }

func (p *provenanceLog) WithGroup(string) slog.Handler { return p }

func (p *provenanceLog) Handle(_ context.Context, rec slog.Record) error {
	if rec.Message != "transition" {
		return nil
	}
	var row provenanceRow
	rec.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "event_id":
			row.EventID = a.Value.Uint64()
		case "client_ref":
			row.ClientRef = a.Value.Uint64()
		case "patch_id":
			row.PatchID = a.Value.Uint64()
		case "origin_source":
			row.Source = a.Value.String()
		case "fragment_ids":
			if ids, ok := a.Value.Any().([]string); ok {
				row.Fragments = slices.Clone(ids)
			}
		}
		return true
	})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.rows = append(p.rows, row)
	return nil
}

// rowFor returns the transition row for one patch identifier: the lookup
// `go run . -provenance` gives an operator holding a captured frame.
//
// A patch off the wire is NOT a receipt for its own row. The library writes the
// socket first and the causal row second — deliberately, so that a frame which
// never reached the transport is not logged as delivered — so a caller that
// reads this the instant a patch arrives is racing the actor, and will get nil
// for a row that is about to exist. Callers must first observe something the
// library orders after that write; the mount spec below uses the next patch.
func (p *provenanceLog) rowFor(patchID uint64) *provenanceRow {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, row := range p.rows {
		if row.PatchID == patchID {
			return &row
		}
	}
	return nil
}

// eventIDFor returns the server-assigned event identifier for one client
// reference, which is what a patch's contributing list is expressed in.
func (p *provenanceLog) eventIDFor(clientRef uint64) (uint64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, row := range p.rows {
		if row.ClientRef == clientRef && row.EventID != 0 {
			return row.EventID, true
		}
	}
	return 0, false
}

type mounted struct {
	app    *live.App[State, live.AnonymousIdentity]
	feed   *Feed
	meters *Meters
	prov   *provenanceLog
	htmx   []byte
	server *httptest.Server
}

// mount builds the dashboard over a feed that does NOT tick, and serves it.
//
// The ticker is off by default and specs that want it call run(). A spec that
// drives feed.Sample itself controls exactly how many server-initiated
// transitions happen, which is what lets an assertion be "this patch carried
// these fragments" rather than "eventually something like this arrived".
func mount(mutate func(*live.Config[State, live.AnonymousIdentity])) *mounted {
	GinkgoHelper()

	feed := NewFeed(1, time.Hour)
	feed.now = func() time.Time { return baseTime }

	m := &mounted{feed: feed, meters: NewMeters(), prov: newProvenanceLog()}

	cfg := Config(feed, []string{testOrigin})
	cfg.Metrics = m.meters
	cfg.Logger = slog.New(m.prov)
	if mutate != nil {
		mutate(&cfg)
	}

	app, err := live.New(cfg)
	Expect(err).NotTo(HaveOccurred())
	m.app = app

	htmx, err := LoadHTMX(DefaultHTMXPath)
	Expect(err).NotTo(HaveOccurred())
	m.htmx = htmx
	m.server = httptest.NewServer(NewMux(app, feed, htmx, m.meters))

	DeferCleanup(func() {
		m.server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		Expect(app.Close(ctx)).To(Succeed())
	})
	return m
}

// run starts the feed's own ticker at the given interval, which is the only way
// to demonstrate that patches arrive with nothing asking for them.
func (m *mounted) run(interval time.Duration) {
	m.feed.interval = interval
	m.feed.Start()
	DeferCleanup(m.feed.Stop)
}

// browser is one connected tab: a livetest.Client, plus the two things this
// example needs that the protocol does not have.
//
// The frame reader and the WebSocket driver this type used to carry are gone —
// livetest.Client is the library's supported answer and it is now built, so an
// example that used to prove FR-62's wire properties with its own decoder now
// proves them with the one thing a reader of the example can also pick up. What
// is left is `name`, for a failure message, and a count of the event frames
// this tab sent, which is what "nothing in this browser polls anything" is
// asserted with.
//
// livetest.Client never acknowledges on its own — the property two of the
// specs below are built on, and the reason this type used to carry the same
// comment.
type browser struct {
	*livetest.Client
	name   string
	events atomic.Int64
}

func (m *mounted) open(name string) *browser {
	GinkgoHelper()

	return &browser{
		Client: livetest.NewClient(GinkgoTB(), NewMux(m.app, m.feed, m.htmx, m.meters), livetest.ClientOptions{
			Path:    MountPath,
			Origin:  testOrigin,
			Timeout: 60 * time.Second,
		}),
		name: name,
	}
}

// ackAndAwait applies one acknowledgement before the spec drives a frame whose
// meaning depends on the server's acknowledged high-water mark. Ack and Event
// frames use different actor channels, so the socket write alone establishes
// their wire order, not the order in which the actor selects them.
func (m *mounted) ackAndAwait(b *browser, serverSeq uint64) {
	GinkgoHelper()

	before := m.meters.Histogram(MetricWindowDepth).Count
	b.Ack(serverSeq)
	patience.Await(GinkgoTB(), "the actor to apply the acknowledgement", actorAppliedBudget,
		func() Distribution { return m.meters.Histogram(MetricWindowDepth) },
		func(observed Distribution) bool {
			return observed.Count > before && observed.Last == 0
		})
}

// send writes one event frame and returns the client reference it used, which
// is the handle the provenance log resolves to a server-side event identifier.
func (b *browser) send(name string, fields map[string]string) uint64 {
	GinkgoHelper()
	b.events.Add(1)
	return b.Send(name, FragmentControls, fields)
}

func (b *browser) probe() uint64 { return b.send(EventProbe, nil) }
func (b *browser) pause() uint64 { return b.send(EventPause, nil) }
func (b *browser) clear() uint64 { return b.send(EventClear, nil) }

// take reads n frames, acknowledging each patch as a browser does.
func (b *browser) take(n int, timeout time.Duration) []*livetest.Frame {
	GinkgoHelper()

	out := make([]*livetest.Frame, 0, n)
	for len(out) < n {
		f := b.Next(timeout)
		out = append(out, f)
		if f.Patch != nil {
			b.Ack(f.Patch.ServerSeq)
		}
	}
	return out
}

// eventFrames is how many event frames this browser sent. It is what "nothing in
// this browser polls anything" is asserted with.
func (b *browser) eventFrames() int64 { return b.events.Load() }

func isPatch(f *livetest.Frame) bool { return f.Kind == livetest.FramePatch }

func carries(fragmentID string) func(*livetest.Frame) bool {
	return func(f *livetest.Frame) bool {
		if !isPatch(f) {
			return false
		}
		_, ok := f.Patch.Fragment(fragmentID)
		return ok
	}
}

// anyPatchCarrying reports whether any patch already received carried the given
// text in the given region.
func anyPatchCarrying(frames []*livetest.Frame, fragmentID, want string) bool {
	for _, f := range frames {
		if !isPatch(f) {
			continue
		}
		if html, ok := f.Patch.Fragment(fragmentID); ok && strings.Contains(html, want) {
			return true
		}
	}
	return false
}

func patchesIn(frames []*livetest.Frame) []*livetest.Frame {
	var out []*livetest.Frame
	for _, f := range frames {
		if isPatch(f) {
			out = append(out, f)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// FR-62 property 1 — high-frequency server-initiated updates
// ---------------------------------------------------------------------------

var _ = Describe("High-frequency server-initiated updates", func() {
	It("patches a browser that has never sent an event, at the feed's rate", func() {
		m := mount(nil)
		m.run(2 * time.Millisecond)
		b := m.open("watcher")

		const want = 25
		frames := b.take(want, 10*time.Second)

		Expect(b.eventFrames()).To(BeZero(),
			"nothing in this browser asked for any of that: acknowledgements are not events")

		var seqs []uint64
		for _, f := range frames {
			Expect(f.Kind).To(Equal(livetest.FramePatch), f.String())
			Expect(f.Patch.Origin.Kind).To(BeNumerically("==", originEffect),
				"a server-initiated patch is attributed to the effect that produced it (FR-42)")
			Expect(f.Patch.Origin.Source).To(Equal("effect:"+SourceSubscribe),
				"and `unknown` is not a permitted origin value")
			seqs = append(seqs, f.Patch.ServerSeq)
		}
		Expect(seqs).To(HaveLen(want))
		Expect(slices.IsSortedFunc(seqs, func(a, b uint64) int { return int(a) - int(b) })).To(BeTrue(),
			"server sequence numbers are monotonic: %v", seqs)
	})

	It("shows a rising sample number in the markup, so a stalled feed cannot look like a live one", func() {
		m := mount(nil)
		b := m.open("watcher")

		var numbers []string
		for i := 0; i < 5; i++ {
			m.feed.Sample(live.ID{}, 0)
			f := b.Await("a meters patch", 5*time.Second, carries(FragmentMeters))
			html, _ := f.Patch.Fragment(FragmentMeters)
			numbers = append(numbers, sampleNumber(html))
			b.Ack(f.Patch.ServerSeq)
		}

		Expect(numbers).To(Equal([]string{"1", "2", "3", "4", "5"}),
			"two readings with identical values would otherwise render identical bytes and the patch "+
				"would be suppressed, which is correct behaviour that would make a stalled feed "+
				"indistinguishable from a live one")
	})

	It("stops pushing to a session that has gone, so a closed tab is not a leaked subscription", func() {
		m := mount(nil)
		b := m.open("leaver")
		m.feed.Sample(live.ID{}, 0)
		b.Await("a meters patch", 5*time.Second, carries(FragmentMeters))

		Expect(m.feed.Subscribers()).To(Equal(1))
		Expect(b.Close()).To(Succeed())

		Eventually(m.feed.Subscribers, 10*time.Second, 20*time.Millisecond).Should(BeZero(),
			"Config.Teardown runs after the session goroutine exits and must unsubscribe")
	})
})

// sampleNumber pulls the rendered sample count out of the meters markup. The
// markup carries a data attribute for exactly this, so the spec reads a
// contract rather than a layout.
func sampleNumber(html string) string {
	const marker = `data-dash-id="seq">sample `
	i := strings.Index(html, marker)
	if i < 0 {
		return "<no sample number in " + html + ">"
	}
	rest := html[i+len(marker):]
	j := strings.IndexByte(rest, '<')
	if j < 0 {
		return "<unterminated>"
	}
	return rest[:j]
}

// ---------------------------------------------------------------------------
// FR-62 property 2 — multiple independent live regions
// ---------------------------------------------------------------------------

var _ = Describe("Multiple independent live regions", func() {
	It("puts only the meters on the wire when the feed samples, and renders nothing else at all", func() {
		m := mount(nil)
		b := m.open("watcher")

		for i := 0; i < 5; i++ {
			m.feed.Sample(live.ID{}, 0)
			f := b.Await("a patch", 5*time.Second, isPatch)

			Expect(f.Patch.FragmentIDs()).To(Equal([]string{FragmentMeters}),
				"a sample moves the meters and nothing else, so the alert log and the control "+
					"panel are not re-painted twenty times a second")
			b.Ack(f.Patch.ServerSeq)
		}

		// The second half, and the half that carries the property.
		//
		// The wire assertion above is necessary and it is NOT sufficient, which
		// was found by mutating the code rather than by thinking about it:
		// widening the controls region's Dirty to include the meters leaves the
		// assertion above completely green, because the controls markup does not
		// change when a reading does and identical-render suppression drops it
		// before it reaches a frame. Over-declaring costs a RENDER, not a patch,
		// and the render is what FR-62's independent-regions property is about
		// at twenty samples a second.
		//
		// gotthlive_patches_suppressed_total is where that cost is visible: a
		// fragment is counted there only if it declared itself dirty, was
		// rendered, and produced bytes the client already had. Zero is the claim
		// — no region of this dashboard is rendered on a transition that did not
		// touch it.
		Expect(m.meters.Counter(MetricPatchesSuppressed)).To(BeZero(),
			"a region was re-rendered and then thrown away: some fragment declares itself "+
				"dirty for a transition that does not touch it")
	})

	It("puts only the alert log on the wire when a series crosses the threshold", func() {
		m := mount(nil)
		b := m.open("watcher")

		// Driven rather than waited for: the crossing is the subject, and a
		// spec that waited for the random walk to produce one would be
		// asserting about the seed.
		m.feed.step = func(int) int { return AlertAbove + 5 }
		m.feed.Sample(live.ID{}, 0)

		alert := b.Await("an alerts patch", 5*time.Second, carries(FragmentAlerts))
		Expect(alert.Patch.FragmentIDs()).To(Equal([]string{FragmentAlerts}),
			"the crossing is its own transition and carries its own region")

		html, _ := alert.Patch.Fragment(FragmentAlerts)
		Expect(html).To(ContainSubstring(`data-dash-id="alert-list"`))
		Expect(html).To(ContainSubstring("cpu"))

		// The same second half as the spec above, and for the same reason:
		// folding the alert log into the meters fragment's Dirty leaves every
		// wire assertion here green, because the reading has not changed and the
		// re-rendered meters are suppressed before they reach a frame. The
		// suppression counter is where an over-declared region shows up.
		Expect(m.meters.Counter(MetricPatchesSuppressed)).To(BeZero(),
			"a threshold crossing re-rendered a region it does not touch")
	})

	It("puts only this tab's controls on the wire when it pauses, and nothing on the other tab's", func() {
		m := mount(nil)
		first := m.open("first")
		second := m.open("second")

		first.pause()
		f := first.Await("a patch", 5*time.Second, isPatch)

		Expect(f.Patch.FragmentIDs()).To(Equal([]string{FragmentControls}))
		html, _ := f.Patch.Fragment(FragmentControls)
		Expect(html).To(ContainSubstring("paused"))
		Expect(f.Patch.Origin.Kind).To(BeNumerically("==", originClientEvent))

		Expect(second.Settle(settleIdle)).To(BeEmpty(),
			"pausing one tab is not an event in anybody else's session")
	})

	It("keeps a paused tab off the wire entirely while every other tab keeps updating", func() {
		m := mount(nil)
		paused := m.open("paused")
		keeping := m.open("keeping-up")

		paused.pause()
		paused.Await("the controls patch", 5*time.Second, carries(FragmentControls))

		for i := 0; i < 5; i++ {
			m.feed.Sample(live.ID{}, 0)
		}

		Expect(paused.Settle(settleIdle)).To(BeEmpty(),
			"a paused session drops the sample without changing state, so no fragment is asked "+
				"whether it is dirty and no patch is built")
		Expect(patchesIn(keeping.Settle(settleIdle))).NotTo(BeEmpty())
	})

	It("reaches every tab when one of them clears the shared alert log", func() {
		m := mount(nil)
		first := m.open("first")
		second := m.open("second")

		m.feed.step = func(int) int { return AlertAbove + 5 }
		m.feed.Sample(live.ID{}, 0)
		first.Await("an alerts patch", 5*time.Second, carries(FragmentAlerts))
		second.Await("an alerts patch", 5*time.Second, carries(FragmentAlerts))

		first.clear()

		cleared := second.Await("the cleared alert log", 5*time.Second, func(f *livetest.Frame) bool {
			html, ok := f.Patch.Fragment(FragmentAlerts)
			return isPatch(f) && ok && strings.Contains(html, `data-dash-id="alerts-empty"`)
		})
		Expect(cleared.Patch.Origin.Kind).To(BeNumerically("==", originEffect),
			"the other tab learns through the feed, not through a client event of its own")
	})
})

// ---------------------------------------------------------------------------
// FR-62 property 3 — batching and debounce, without losing the causal chain
// ---------------------------------------------------------------------------

var _ = Describe("Batching and debounce", func() {
	// The ladder engages at half the outbound window, so a small window is what
	// makes it reachable inside a spec rather than after 8 unacknowledged
	// patches. Everything else is the library's default.
	const ackWindow = 4

	slowWindow := func(cfg *live.Config[State, live.AnonymousIdentity]) { cfg.Limits.AckWindow = ackWindow }

	It("coalesces once the window fills, and the coalesced patch names every contributing event", func() {
		m := mount(slowWindow)
		b := m.open("stalled")

		// Twelve probes and no acknowledgements. Each probe is a client event
		// whose effect samples the feed, and the emission that carries the
		// reading names the probe that asked for it — so twelve probes are
		// twelve causal edges that must all survive being collapsed into fewer
		// frames than there were probes.
		const probes = 12
		refs := make([]uint64, 0, probes)
		for i := 0; i < probes; i++ {
			refs = append(refs, b.probe())
		}
		b.Settle(settleIdle)

		// Acknowledge everything, repeatedly: the ladder holds deferred work
		// until the window re-opens, and one acknowledgement re-opens it once.
		// A spec that acknowledged once would be asserting over whatever had
		// been flushed by then.
		for i := 0; i < 8; i++ {
			b.Ack(b.Seq())
			b.Settle(settleIdle)
		}

		frames := patchesIn(b.Received())
		Expect(frames).NotTo(BeEmpty())

		// What the browser was told, as a set of server-side event identifiers.
		union := map[uint64]bool{}
		widest := 0
		for _, f := range frames {
			for _, id := range f.Patch.Origin.Contributing {
				union[id] = true
			}
			widest = max(widest, len(f.Patch.Origin.Contributing))
		}

		// What it should have been told: every probe, resolved through the
		// provenance log the way an operator resolves a captured frame.
		var want []uint64
		for _, ref := range refs {
			id, ok := m.prov.eventIDFor(ref)
			Expect(ok).To(BeTrue(), "no provenance row resolved client_ref %d to an event", ref)
			want = append(want, id)
		}

		Expect(slices.Sorted(maps.Keys(union))).To(Equal(slices.Sorted(slices.Values(want))),
			"a coalesced patch must name every contributing event: %d probes, %d patches, "+
				"union %v, expected %v", probes, len(frames), union, want)

		Expect(widest).To(BeNumerically(">", 1),
			"nothing was coalesced at all, so this spec proved nothing about coalescing: "+
				"frames were %v", describeAll(frames))
		Expect(len(frames)).To(BeNumerically("<", probes),
			"coalescing is supposed to send fewer frames than there were transitions")

		Expect(m.meters.Counter(MetricPatchesCoalesced)).To(BeNumerically(">", 0))
		Expect(m.meters.CoalesceRatio()).To(BeNumerically(">", 0))
	})

	It("names no event it did not have, at a load where the ladder reaches its second stage", func() {
		m := mount(slowWindow)
		b := m.open("stalled")

		// The other direction of the same set equality, at twice the load. The
		// spec above says nothing was lost; this one says nothing was invented,
		// which is the failure mode a bug in the union would produce in the
		// other direction and which a count-based assertion cannot see.
		//
		// Note what is deliberately NOT asserted: that an event identifier
		// appears in exactly one patch. It does not, and the first version of
		// this spec claimed it did and went red. One probe is TWO transitions —
		// the client event itself, whose own identifier is deferred as a
		// contributing edge when the ladder collapses it, and the emission the
		// effect it scheduled produced, which names the same event as its cause.
		// Coalescing can put those two in different frames, so an identifier
		// legitimately appears twice. "Named at least once" is the property
		// FR-43 is about; "named exactly once" was a spec asserting an accident.
		const probes = 20
		for i := 0; i < probes; i++ {
			b.probe()
		}
		b.Settle(settleIdle)
		for i := 0; i < 12; i++ {
			b.Ack(b.Seq())
			b.Settle(settleIdle)
		}

		real := map[uint64]bool{}
		m.prov.mu.Lock()
		for _, row := range m.prov.rows {
			if row.EventID != 0 {
				real[row.EventID] = true
			}
		}
		m.prov.mu.Unlock()

		named := 0
		for _, f := range patchesIn(b.Received()) {
			for _, id := range f.Patch.Origin.Contributing {
				Expect(id).NotTo(BeZero(), "zero is not an event identifier")
				Expect(real[id]).To(BeTrue(),
					"patch %d names event %d as a contributor, and this session never had an "+
						"event %d: the union is fabricating edges", f.Patch.PatchID, id, id)
				named++
			}
		}
		Expect(named).To(BeNumerically(">=", probes),
			"%d probes produced only %d contributing edges across every patch", probes, named)

		Expect(m.meters.Counter(MetricSlowClientEvents)).To(Equal(1.0),
			"twenty probes against a four-patch window reaches the second stage of the ladder, "+
				"so this really is the coalescing-under-pressure case and not the easy one")
	})
})

func describeAll(frames []*livetest.Frame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.String())
	}
	return out
}

// ---------------------------------------------------------------------------
// FR-62 property 4 — backpressure under a slow client (FR-51)
// ---------------------------------------------------------------------------

var _ = Describe("Backpressure under a slow client", func() {
	const ackWindow = 4

	slowWindow := func(cfg *live.Config[State, live.AnonymousIdentity]) {
		cfg.Limits.AckWindow = ackWindow
		// Long enough that the session is not evicted mid-spec: eviction is the
		// ladder's third stage and QA-2's chaos suite is where it belongs. What
		// is measured here is that the first two stages bound the queue and tell
		// the application, which is the half FR-62 asks an example to show.
		cfg.Limits.SlowClientGrace = time.Minute
	}

	It("bounds the outbound queue and tells the application, then recovers when the client catches up", func() {
		m := mount(slowWindow)
		b := m.open("slow")

		// Far more samples than the window can hold, none acknowledged.
		for i := 0; i < 200; i++ {
			m.feed.Sample(live.ID{}, 0)
		}
		b.Settle(settleIdle)

		patches := patchesIn(b.Received())
		Expect(len(patches)).To(BeNumerically("<=", ackWindow),
			"the outbound window is the bound: 200 samples, %d patches", len(patches))

		Expect(m.meters.Counter(MetricSlowClientEvents)).To(Equal(1.0),
			"the library synthesizes the backpressure signal once per episode, not once per sample")
		Expect(m.meters.Histogram(MetricWindowDepth).Max).To(BeNumerically("<=", ackWindow))
		Expect(m.feed.QueueDepth()).To(BeNumerically("<=", backlogDepth))

		// The notice reaches the browser only once the window re-opens, which is
		// the honest behaviour: the library had already stopped emitting when
		// the reducer set it.
		b.Ack(b.Seq())
		degraded := b.Await("the degraded notice", 10*time.Second, func(f *livetest.Frame) bool {
			html, ok := f.Patch.Fragment(FragmentControls)
			return isPatch(f) && ok && strings.Contains(html, `data-dash-degraded="true"`)
		})
		html, _ := degraded.Patch.Fragment(FragmentControls)
		Expect(html).To(ContainSubstring("falling behind"))

		// Catch up, and read the recovery out of what was received rather than
		// out of what arrives next: the library synthesizes the recovery signal
		// on the acknowledgement that re-opens the window, so it is already in
		// flight by the time a spec could start waiting for it. The first
		// version of this waited on the channel, drained the recovery frame in
		// its own catch-up loop, and then failed to find it — which is a spec
		// racing its own subject rather than a defect in the subject.
		Eventually(func() bool {
			b.Ack(b.Seq())
			return anyPatchCarrying(b.Received(), FragmentControls, `data-dash-degraded="false"`)
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue(),
			"the session never told the application it had caught up")

		Expect(anyPatchCarrying(b.Received(), FragmentControls, "keeping up")).To(BeTrue())
	})

	It("degrades the slow session and leaves every other session alone", func() {
		m := mount(slowWindow)
		slow := m.open("slow")
		quick := m.open("quick")

		for i := 0; i < 60; i++ {
			m.feed.Sample(live.ID{}, 0)
			// The quick browser acknowledges everything it is given, which is
			// what makes this two sessions rather than one measured twice.
			for _, f := range quick.Settle(20 * time.Millisecond) {
				if f.Patch != nil {
					quick.Ack(f.Patch.ServerSeq)
				}
			}
		}
		quick.Settle(settleIdle)
		slow.Settle(settleIdle)

		Expect(len(patchesIn(slow.Received()))).To(BeNumerically("<=", ackWindow))
		Expect(len(patchesIn(quick.Received()))).To(BeNumerically(">", ackWindow),
			"the session that kept up must not be paced by the one that did not")
	})

	It("never drops a patch: it coalesces, then defers, and the connection stays honest", func() {
		m := mount(slowWindow)
		b := m.open("slow")

		for i := 0; i < 40; i++ {
			m.feed.Sample(live.ID{}, 0)
		}
		b.Settle(settleIdle)

		// Sequence numbers are contiguous from the snapshot: a patch that was
		// dropped rather than deferred would leave a hole, and a hole is how a
		// browser's DOM comes to disagree with the server with nothing saying
		// so. Deferral is what makes the numbers dense.
		var seqs []uint64
		for _, f := range b.Received() {
			if f.Patch != nil {
				seqs = append(seqs, f.Patch.ServerSeq)
			}
		}
		Expect(seqs).NotTo(BeEmpty())
		for i := 1; i < len(seqs); i++ {
			Expect(seqs[i]).To(Equal(seqs[i-1]+1),
				"a gap at %v means a patch was dropped rather than deferred", seqs)
		}

		// And the final state is the current one, not a replay of what was
		// missed: the browser catches up to now.
		b.Ack(b.Seq())
		f := b.Await("a meters patch after catching up", 10*time.Second, carries(FragmentMeters))
		html, _ := f.Patch.Fragment(FragmentMeters)
		Expect(sampleNumber(html)).To(Equal("40"),
			"a client that fell behind is shown the current reading, not the ones it missed")
	})
})

// ---------------------------------------------------------------------------
// FR-62 property 5 — a plain-HTMX region on the same page, and D-16
// ---------------------------------------------------------------------------

var _ = Describe("The plain-HTMX regions", func() {
	It("serves its fragments from ordinary handlers that never touch a live session", func() {
		m := mount(nil)

		for path, want := range map[string]string{
			"/htmx/notes":   `data-dash-id="notes-list"`,
			"/htmx/deploys": `data-dash-id="deploy-list"`,
		} {
			body, status := get(m.server.URL + path)
			Expect(status).To(Equal(http.StatusOK), path)
			Expect(body).To(ContainSubstring(want))
			Expect(body).NotTo(ContainSubstring("data-gotth-region"),
				"an HTMX endpoint returns a fragment, not a live region")
		}
		Expect(m.feed.Subscribers()).To(BeZero(),
			"asking for an HTMX fragment does not open a live session")
	})

	It("declares the deploys card outside every live region, so no patch can name it", func() {
		m := mount(nil)
		body, status := get(m.server.URL + "/")
		Expect(status).To(Equal(http.StatusOK))

		Expect(body).To(ContainSubstring(`id="deploys"`))

		b := m.open("watcher")
		for i := 0; i < 5; i++ {
			m.feed.Sample(live.ID{}, 0)
		}
		b.pause()
		b.Settle(settleIdle)

		for _, f := range b.Received() {
			if f.Patch == nil {
				continue
			}
			for _, id := range f.Patch.FragmentIDs() {
				Expect(id).To(BeElementOf(FragmentMeters, FragmentAlerts, FragmentControls))
			}
			for _, u := range f.Patch.Updates {
				Expect(u.HTML).NotTo(ContainSubstring(`id="deploys"`),
					"FR-31: markup outside a declared live region is never touched by morph")
			}
		}
	})

	It("keeps the HTMX island inside the controls region behind live.Preserve", func() {
		m := mount(nil)
		b := m.open("watcher")

		b.pause()
		f := b.Await("the controls patch", 5*time.Second, carries(FragmentControls))
		html, _ := f.Patch.Fragment(FragmentControls)

		Expect(html).To(ContainSubstring("data-gotth-preserve"))
		Expect(html).To(ContainSubstring(`id="dash-notes"`),
			"the island is inside the region, which is the case FR-27's innermost-declaration-wins rule is for")
	})

	// D-16. This is the executable half of the documentation the ruling asked
	// for: markup carrying hx-* that a MORPH inserts is inert until
	// htmx.process runs, and the way this example stays out of that trap is
	// structural — every hx-* attribute it renders is either outside every live
	// region or inside a live.Preserve subtree, both of which HTMX processes at
	// page load and morph never replaces.
	//
	// The spec is the guard on that structure rather than a restatement of it.
	// Adding an hx-get to the meters region would be a perfectly reasonable
	// thing for somebody to do, it would look right in the browser on first
	// load, and it would silently stop working the first time the server
	// re-rendered that region. This turns that into a red spec with the reason
	// in the failure message.
	It("renders no hx-* attribute inside a server-owned live region (D-16)", func() {
		for _, region := range []struct {
			id   string
			html string
		}{
			{FragmentMeters, render(MetersRegion(sampleState()))},
			{FragmentAlerts, render(AlertsRegion(sampleState()))},
			{FragmentControls, render(ControlsRegion(sampleState()))},
		} {
			for _, chunk := range serverOwned(region.html) {
				Expect(chunk).NotTo(ContainSubstring("hx-"),
					"fragment %q renders an hx-* attribute in markup the server owns. A morph "+
						"INSERTS that markup, HTMX never sees it, and it is inert until something "+
						"calls htmx.process on the region — see D-16 and README.md. Put it outside "+
						"every live region, or inside a live.Preserve subtree.", region.id)
			}
		}
	})
})

// serverOwned splits rendered markup into the parts morph will replace,
// dropping everything from a live.Preserve marker to the end of its element.
//
// It is deliberately crude — it cuts at the preserve attribute and resumes at
// the next region-level element — because the property it guards is coarse: an
// hx-* attribute is either in a preserved subtree or it is not, and a spec that
// needed a full HTML parser to decide would be a spec nobody trusts. The
// example's markup is small enough that the crude split is exact, and the
// browser-side truth is in the conformance suite.
func serverOwned(html string) []string {
	var out []string
	rest := html
	for {
		i := strings.Index(rest, "data-gotth-preserve")
		if i < 0 {
			out = append(out, rest)
			return out
		}
		out = append(out, rest[:i])

		// Skip to the end of the preserved element. The island is a <div> and
		// the markup has no nested one, which the assertion below holds.
		end := strings.Index(rest[i:], "</div>")
		if end < 0 {
			return out
		}
		rest = rest[i+end+len("</div>"):]
	}
}

func sampleState() State {
	return State{
		Meters: &Reading{Seq: 3, AtUnixMilli: baseTime.UnixMilli(), Values: []int{40, 95, 12}},
		Window: &History{Values: []int{10, 40, 95}},
		Alerts: &AlertLog{Version: 2, Entries: []Alert{
			{Seq: 3, Series: "memory", Value: 95, AtUnixMilli: baseTime.UnixMilli()},
		}},
		Notice: "the server could not complete an operation: " + SourceSubscribe,
	}
}

func render(c templ.Component) string {
	GinkgoHelper()
	var b strings.Builder
	Expect(c.Render(context.Background(), &b)).To(Succeed())
	return b.String()
}

// ---------------------------------------------------------------------------
// FR-34 — the backpressure metrics this example exports
// ---------------------------------------------------------------------------

var _ = Describe("The FR-34 backpressure metrics", func() {
	It("registers the queue, coalesce and drop instruments the Phase 3 box names", func() {
		m := mount(nil)
		m.open("watcher")

		// Registration is what makes an instrument emittable at all, and an
		// instrument that is never created is a permanently-zero number with no
		// error anywhere. Asserting the names is what tells "this metric is
		// zero" from "this metric does not exist".
		Expect(m.meters.Registered()).To(ContainElements(
			MetricWindowDepth,
			MetricMailboxDepth,
			MetricPatchesCoalesced,
			MetricSlowClientEvents,
			MetricFramesSent,
			MetricPatchesSuppressed,
			MetricEventsRejected,
			MetricFramesRejected,
			MetricResyncBytes,
			MetricWireBytes,
		))
	})

	It("moves queue depth and wire bytes as the feed pushes", func() {
		m := mount(nil)
		b := m.open("watcher")

		for i := 0; i < 5; i++ {
			m.feed.Sample(live.ID{}, 0)
			f := b.Await("a meters patch", 5*time.Second, carries(FragmentMeters))
			b.Ack(f.Patch.ServerSeq)
		}

		Expect(m.meters.Histogram(MetricWindowDepth).Count).To(BeNumerically(">", 0))
		Expect(m.meters.CounterWith(MetricWireBytes, "direction", "out")).To(BeNumerically(">", 0))
		patience.Await(GinkgoTB(), "all five patch writes to reach the sent-frame counter", actorAppliedBudget,
			func() float64 { return m.meters.CounterWith(MetricFramesSent, "kind", "patch") },
			func(observed float64) bool { return observed >= 5 })
	})

	It("serves the report over HTTP with the drop paragraph the numbers need", func() {
		m := mount(nil)
		body, status := get(m.server.URL + MetricsRoute)

		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(ContainSubstring(MetricWindowDepth))
		Expect(body).To(ContainSubstring("coalesce_ratio"))
		Expect(body).To(ContainSubstring("a patch itself is never dropped"),
			"FR-34 names patch drops and this library has no such counter by design; "+
				"the report has to say so where the numbers are read")
	})

	It("computes the coalesce ratio against patch frames rather than fragment updates", func() {
		meters := NewMeters()
		Expect(meters.CoalesceRatio()).To(BeZero(), "no patches sent is a ratio of zero, not a NaN")

		meter := meters.Meter("test")
		coalesced, err := meter.Int64Counter(MetricPatchesCoalesced)
		Expect(err).NotTo(HaveOccurred())
		frames, err := meter.Int64Counter(MetricFramesSent)
		Expect(err).NotTo(HaveOccurred())

		ctx := context.Background()
		coalesced.Add(ctx, 3)
		frames.Add(ctx, 12, metricAttr("kind", "patch"))
		frames.Add(ctx, 40, metricAttr("kind", "heartbeat"))

		Expect(meters.CoalesceRatio()).To(BeNumerically("~", 0.25, 1e-9),
			"the denominator is patch FRAMES; counting fragment updates would make a patch "+
				"carrying three regions look like three patches")
	})
})

// ---------------------------------------------------------------------------
// The resync-cost measurement, which is code and therefore has to be tested
// ---------------------------------------------------------------------------

var _ = Describe("The resync measurement", func() {
	It("answers every request with a snapshot and agrees with the library's own byte count", func() {
		feed := NewFeed(1, 2*time.Millisecond)
		feed.Start()
		DeferCleanup(feed.Stop)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		DeferCleanup(cancel)

		report, err := MeasureResync(ctx, feed, 3)
		Expect(err).NotTo(HaveOccurred())

		Expect(report.FrameBytes).To(HaveLen(3))
		Expect(report.Latency).To(HaveLen(3))
		Expect(report.PerFragment).To(HaveKey(FragmentMeters))
		Expect(report.PerFragment).To(HaveKey(FragmentAlerts))
		Expect(report.PerFragment).To(HaveKey(FragmentControls))

		for i, frame := range report.FrameBytes {
			Expect(frame).To(BeNumerically(">", report.HTMLBytes[i]),
				"a frame is its markup plus the protocol's own overhead")
			Expect(report.Latency[i]).To(BeNumerically(">", 0))
		}

		// The cross-check, and the reason the report prints both. The library's
		// gotthlive_resync_bytes is recorded server-side by code that has never
		// seen wire.go; the frame lengths are measured client-side by a decoder
		// that has never seen the library's framer. Agreement is evidence;
		// disagreement would be a defect report.
		Expect(report.LibraryResyncBytes.Count).To(Equal(int64(3)))
		Expect(report.LibraryResyncBytes.Max).To(BeNumerically("==", slices.Max(report.FrameBytes)))

		var sum int
		for _, n := range report.FrameBytes {
			sum += n
		}
		Expect(report.LibraryResyncBytes.Sum).To(BeNumerically("==", float64(sum)))
	})

	It("supersedes exactly the gap the client really has, so an operator can say which patches the snapshot replaced", func() {
		m := mount(nil)
		b := m.open("watcher")

		var applied uint64
		for i := 0; i < 3; i++ {
			m.feed.Sample(live.ID{}, 0)
			f := b.Await("a meters patch", 5*time.Second, carries(FragmentMeters))
			m.ackAndAwait(b, f.Patch.ServerSeq)
			applied = f.Patch.ServerSeq
		}

		// The gap, opened deliberately: one more patch, read and left
		// UNACKNOWLEDGED. This is the only state a resync is answered from, and
		// it is the state the shipped runtime asks from — runtime.js latches a
		// gap when a patch is not seq+1, and ask() sends its own cursor rather
		// than a lower one.
		m.feed.Sample(live.ID{}, 0)
		held := b.Await("a meters patch", 5*time.Second, carries(FragmentMeters))
		Expect(held.Patch.ServerSeq).To(BeNumerically(">", applied))

		b.Resync(applied, resyncReasonClientRequest)

		snap := b.Await("the resync snapshot", 5*time.Second,
			func(f *livetest.Frame) bool { return f.Kind == livetest.FrameSnapshot })

		Expect(snap.Patch.Origin.Kind).To(BeNumerically("==", originResync))
		Expect(snap.Patch.FragmentIDs()).To(ConsistOf(FragmentMeters, FragmentAlerts, FragmentControls),
			"a snapshot renders every region, which is what makes it the expensive operation")
		Expect(snap.Patch.SupersededFrom).To(Equal(applied+1),
			"the range begins exactly where this client stands, which is what runtime.js's "+
				"applied() requires of it: from > seq+1 is a hole and from <= seq is an "+
				"overlap, and the client closes 4002 on either")
		Expect(snap.Patch.SupersededThrough).To(BeNumerically(">=", held.Patch.ServerSeq),
			"without the supersession range nobody can say which events produced the markup "+
				"the user is now looking at: the superseded patches were emitted and then dropped")

		// The rate budget is real, and refusing to amplify is a typed error
		// rather than silence. The default burst is three and the answered
		// request above spent one, so these exhaust it — including the ones the
		// server answers with an Ack, because a no-op resync is still charged
		// for the event id, the Authorize hook and the mailbox slot it costs.
		for i := 0; i < 4; i++ {
			b.Resync(applied, resyncReasonClientRequest)
		}
		refused := b.Await("a rate-limit error", 10*time.Second,
			func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })
		Expect(refused.Error.Code).To(BeNumerically("==", codeRateLimited))
		Expect(refused.Error.Fatal).To(BeFalse())
	})

	It("answers a request claiming less than the client acknowledged with an Ack, not a snapshot", func() {
		m := mount(nil)
		b := m.open("watcher")

		var applied uint64
		for i := 0; i < 3; i++ {
			m.feed.Sample(live.ID{}, 0)
			f := b.Await("a meters patch", 5*time.Second, carries(FragmentMeters))
			m.ackAndAwait(b, f.Patch.ServerSeq)
			applied = f.Patch.ServerSeq
		}
		Expect(applied).To(BeNumerically(">", 1))

		// last_applied_seq is untrusted input, and understating it is how a
		// client could once manufacture a supersession range over patches it had
		// already applied: ask with 1 every time and the ranges are [2, S₁],
		// [2, S₂], … — overlapping, which is P7's non-overlap failing on the wire
		// through no server fault. The server clamps the claim to what it already
		// knows — this client's own acknowledged high-water mark, and the
		// sequence of the last snapshot it sent — BEFORE deciding whether the
		// request describes a gap at all, so the claim below clamps up to
		// server_seq and the answer is the no-op Ack rather than a snapshot
		// carrying a falsified range.
		//
		// This example asserts it because this example is where it was noticed:
		// the measurement above ran on the old behaviour, and the library's own
		// suite checks the range a snapshot carries rather than which requests
		// produce a snapshot at all.
		b.Resync(1, resyncReasonClientRequest)

		Expect(b.Await("an ack", 5*time.Second,
			func(f *livetest.Frame) bool { return f.Kind == livetest.FrameAck })).NotTo(BeNil())

		// And nothing follows it. A snapshot superseding [2, applied] would
		// replace markup this client already holds, and runtime.js closes 4002 on
		// precisely that overlap — so answering here would evict a correct client.
		for _, f := range b.Settle(500 * time.Millisecond) {
			Expect(f.Kind).NotTo(Equal(livetest.FrameSnapshot),
				"an understated claim was answered with a snapshot after all")
		}
	})

	It("refuses a sample count that would measure nothing", func() {
		feed := NewFeed(1, time.Hour)
		_, err := MeasureResync(context.Background(), feed, 0)
		Expect(err).To(HaveOccurred())
	})

	It("prints its method beside its numbers", func() {
		report := &ResyncReport{
			Samples: 2, FrameBytes: []int{100, 120}, HTMLBytes: []int{80, 90},
			Latency:      []time.Duration{time.Millisecond, 2 * time.Millisecond},
			PerFragment:  map[string]int{FragmentMeters: 90},
			FeedInterval: 50 * time.Millisecond, ResyncBudget: time.Millisecond, ResyncBurst: 10,
		}
		var out strings.Builder
		report.Print(&out)

		text := out.String()
		Expect(text).To(ContainSubstring("method"))
		Expect(text).To(ContainSubstring("relaxed from the library's"),
			"a measurement taken under non-default limits that does not say so gets quoted for years")
		Expect(text).To(ContainSubstring("compression is disabled"))
	})
})

// ---------------------------------------------------------------------------
// The mounted application
// ---------------------------------------------------------------------------

var _ = Describe("The mounted application", func() {
	It("serves a first paint that already holds the feed's state", func() {
		m := mount(nil)
		m.feed.Sample(live.ID{}, 0)

		body, status := get(m.server.URL + "/")
		Expect(status).To(Equal(http.StatusOK))

		for _, id := range []string{FragmentMeters, FragmentAlerts, FragmentControls} {
			Expect(body).To(ContainSubstring(`data-gotth-region="` + id + `"`))
		}
		Expect(body).To(ContainSubstring(`data-dash-id="seq">sample 1`),
			"the snapshot that arrives a moment later morphs the page to bytes it already has")
	})

	It("points the runtime script at the path the handler is actually mounted under", func() {
		m := mount(nil)
		body, _ := get(m.server.URL + "/")

		src := scriptSrc(body)
		Expect(src).To(HavePrefix(MountPath),
			"a literal here instead of MountPath is a page whose script 404s with no server error")

		_, status := get(m.server.URL + src)
		Expect(status).To(Equal(http.StatusOK), "and the script the page names must actually be there")
	})

	It("serves the stylesheet and the vendored HTMX bundle", func() {
		m := mount(nil)

		css, status := get(m.server.URL + "/dashboard.css")
		Expect(status).To(Equal(http.StatusOK))
		Expect(css).To(ContainSubstring("data-gotth-status"))

		js, status := get(m.server.URL + HTMXRoute)
		Expect(status).To(Equal(http.StatusOK))
		Expect(js).NotTo(BeEmpty())
	})

	It("refuses an upgrade from an Origin that is not on the allowlist", func() {
		m := mount(nil)

		Expect(handshake(m.server.URL, map[string]string{"Origin": "http://evil.test"})).
			To(Equal(http.StatusForbidden))
		Expect(handshake(m.server.URL, nil)).To(Equal(http.StatusForbidden),
			"an absent Origin is not an allowed one")
		Expect(handshake(m.server.URL, map[string]string{"Origin": testOrigin})).
			To(Equal(http.StatusSwitchingProtocols))
	})

	It("attributes the mount snapshot to the mount and indexes it in the provenance log", func() {
		m := mount(nil)
		b := m.open("watcher")

		first := b.Received()[0]
		Expect(first.Kind).To(Equal(livetest.FrameSnapshot))
		Expect(first.Patch.Origin.Kind).To(BeNumerically("==", originMount))
		Expect(first.Patch.SupersededFrom).To(BeZero(), "a session's first snapshot supersedes nothing")

		m.feed.Sample(live.ID{}, 0)
		patch := b.Await("a meters patch", 5*time.Second, carries(FragmentMeters))

		// Wait past the patch before reading its row, and do it with a counted
		// frame rather than a window. rowFor says why the frame itself is not
		// enough; this is the cheapest thing the library orders after that
		// write. One session's transitions are applied in sequence, so a second
		// patch on the socket proves the first patch's row was written before
		// it. That is an ordering, not a duration — no stall can invalidate it.
		//
		// Reading the log the instant the first patch landed is what this spec
		// used to do, and it lost roughly one run in eight on a 2-vCPU runner:
		// the gap it was racing is about 50 µs wide, so any deschedule of the
		// actor inside it produced "patch N appears in no provenance row". A
		// window sized to survive that would have been a guess about the
		// scheduler; this is not.
		m.feed.Sample(live.ID{}, 0)
		b.Await("the patch after it", 5*time.Second, func(f *livetest.Frame) bool {
			return carries(FragmentMeters)(f) && f.Patch.PatchID > patch.Patch.PatchID
		})

		// FR-41 in one assertion: given a patch identifier off the wire, the
		// provenance log names the effect that produced it and the regions it
		// carried. This is what `-provenance` is for, and it is the mechanism
		// the coalescing spec uses to resolve a client_ref to an event.
		found := m.prov.rowFor(patch.Patch.PatchID)
		Expect(found).NotTo(BeNil(), "patch %d appears in no provenance row", patch.Patch.PatchID)
		Expect(found.Source).To(Equal("effect:" + SourceSubscribe))
		Expect(found.Fragments).To(Equal([]string{FragmentMeters}))
	})

	It("refuses an event name that is not registered", func() {
		m := mount(nil)
		b := m.open("watcher")

		b.send("dashboard.sample", map[string]string{"cpu": "100"})

		f := b.Await("an error frame", 5*time.Second, func(f *livetest.Frame) bool { return f.Kind == livetest.FrameError })
		Expect(f.Error.Fatal).To(BeFalse(), "an unknown name is refused, not a reason to close")
		Expect(f.Error.Code).To(BeNumerically("==", codeUnknownEvent))

		// The reading the browser tried to inject reached nobody's screen.
		Expect(m.feed.Reading().Seq).To(BeZero())
	})
})

func get(rawURL string) (string, int) {
	GinkgoHelper()

	resp, err := http.Get(rawURL) //nolint:gosec,noctx // a spec against its own httptest server
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return string(body), resp.StatusCode
}

// scriptSrc pulls the gotth-live runtime's src out of the page.
func scriptSrc(html string) string {
	GinkgoHelper()

	const marker = `<script src="`
	for i := 0; ; {
		j := strings.Index(html[i:], marker)
		if j < 0 {
			Fail("the page renders no gotth-live script tag: " + html)
		}
		start := i + j + len(marker)
		end := strings.IndexByte(html[start:], '"')
		Expect(end).To(BeNumerically(">=", 0))
		src := html[start : start+end]
		if strings.Contains(src, "gotth-live") {
			return src
		}
		i = start + end
	}
}

// handshake performs the upgrade request and returns the status line's code.
//
// It is a raw socket rather than an http.Client because a successful upgrade
// hijacks the connection and there is no response body to read: the status is
// the whole answer, and this asks for exactly that.
func handshake(serverURL string, headers map[string]string) int {
	GinkgoHelper()

	u, err := url.Parse(serverURL)
	Expect(err).NotTo(HaveOccurred())

	conn, err := net.Dial("tcp", u.Host)
	Expect(err).NotTo(HaveOccurred())
	defer conn.Close()
	Expect(conn.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())

	req := "GET " + MountPath + " HTTP/1.1\r\nHost: " + u.Host + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Protocol: " + Subprotocol + "\r\n"
	for k, v := range headers {
		req += k + ": " + v + "\r\n"
	}
	req += "\r\n"

	_, err = conn.Write([]byte(req))
	Expect(err).NotTo(HaveOccurred())

	status, err := bufio.NewReader(conn).ReadString('\n')
	Expect(err).NotTo(HaveOccurred())

	code, err := strconv.Atoi(strings.Fields(status)[1])
	Expect(err).NotTo(HaveOccurred())
	return code
}

// metricAttr is the option shape the OTel API takes for one attribute.
func metricAttr(key, value string) metric.AddOption {
	return metric.WithAttributes(attribute.String(key, value))
}
