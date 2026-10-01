package obs

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// SourceLabelCap bounds the origin-source label. Nothing registers an effect,
// so this cardinality cannot be bounded by registration the way event and
// fragment names are; it is bounded here instead, and the overflow counter is
// how an explosion becomes visible at runtime rather than never.
const SourceLabelCap = 64

// SourceOverflowLabel is what a source value past the cap is recorded as.
const SourceOverflowLabel = "other"

// LabelOptionCap bounds how many distinct label values this package will hold
// a pre-built measurement option for.
//
// Every label domain in this library is already bounded — by the protocol's
// enumerations (frame kind, rejection reason, close code, patch operation,
// transition result, panic site), by application registration (event name), or
// by SourceLabelCap. The cap is the belt to those braces: a value past it is
// still recorded, correctly and with the same attributes, it is simply built
// per call rather than cached. A cache that could grow without bound would be
// its own memory finding.
const LabelOptionCap = 256

// measurement is one pre-built label set, held in the two shapes the metric
// API asks for.
//
// It exists because of what the alternative costs on a per-frame path.
// metric.WithAttributes copies the KeyValue slice, sorts it, de-duplicates it,
// computes a distinct key and heap-allocates the option — on EVERY call — and
// the variadic call itself allocates the option slice. Measured against the G2
// baseline that path was not merely allocation: it was deep enough to push the
// connection read pump's goroutine stack past a doubling boundary, which
// docs/bench/g2-baseline.md §5.1 attributed to observability and §6.1.2
// required to be engineered down or escalated. The label sets here do not vary
// per call — they vary per enumerated label VALUE — so they are built once.
//
// Nothing about what is emitted changes: same instrument, same attribute key,
// same attribute value. Only the number of times the same set is constructed.
type measurement struct {
	add    []metric.AddOption
	record []metric.RecordOption
}

// newMeasurement builds the option pair for one attribute set.
func newMeasurement(kvs ...attribute.KeyValue) *measurement {
	o := metric.WithAttributeSet(attribute.NewSet(kvs...))
	return &measurement{
		add:    []metric.AddOption{o},
		record: []metric.RecordOption{o},
	}
}

// noAttrs is the empty label set, for instruments recorded without one.
var noAttrs = &measurement{}

// labelOpts caches one measurement per value of a single label.
//
// sync.Map is the right structure here and not a lock: reads dominate by
// orders of magnitude, the key space is enumerated, and after the first frame
// of each kind every lookup is a read from the map's read-only half.
type labelOpts struct {
	key   string
	held  atomic.Int64
	cache sync.Map // string -> *measurement
}

func (l *labelOpts) of(value string) *measurement {
	if m, ok := l.cache.Load(value); ok {
		return m.(*measurement)
	}
	m := newMeasurement(attribute.String(l.key, value))
	if l.held.Load() < LabelOptionCap {
		if _, loaded := l.cache.LoadOrStore(value, m); !loaded {
			l.held.Add(1)
		}
	}
	return m
}

// pairOpts is labelOpts for the one metric carrying two labels.
type pairOpts struct {
	keyA, keyB string
	held       atomic.Int64
	cache      sync.Map // string -> *measurement
}

func (p *pairOpts) of(a, b string) *measurement {
	// The separator is a NUL because it cannot appear in a Go source constant
	// used as a label value here, so two different pairs cannot collide into
	// one cache entry.
	k := a + "\x00" + b
	if m, ok := p.cache.Load(k); ok {
		return m.(*measurement)
	}
	m := newMeasurement(attribute.String(p.keyA, a), attribute.String(p.keyB, b))
	if p.held.Load() < LabelOptionCap {
		if _, loaded := p.cache.LoadOrStore(k, m); !loaded {
			p.held.Add(1)
		}
	}
	return m
}

// Metrics is the library's metric set. A nil *Metrics is a fully valid,
// fully disabled instrument: every method tests its receiver, so a
// configuration with no meter provider pays one branch per call site.
type Metrics struct {
	framesReceived  metric.Int64Counter
	framesSent      metric.Int64Counter
	framesRejected  metric.Int64Counter
	outboundInvalid metric.Int64Counter
	sourceOverflow  metric.Int64Counter
	resyncRequests  metric.Int64Counter
	eventsReceived  metric.Int64Counter
	eventsRejected  metric.Int64Counter
	transitions     metric.Int64Counter
	patchesSent     metric.Int64Counter
	patchesSuppress metric.Int64Counter
	patchesCoalesce metric.Int64Counter
	slowClient      metric.Int64Counter
	wireBytes       metric.Int64Counter
	effects         metric.Int64Counter
	effectsAbandon  metric.Int64Counter
	panics          metric.Int64Counter
	connections     metric.Int64Counter
	connectionsShut metric.Int64Counter
	telemetryDrop   metric.Int64Counter

	reduceDuration metric.Float64Histogram
	renderDuration metric.Float64Histogram
	encodeDuration metric.Float64Histogram
	sendDuration   metric.Float64Histogram
	frameBytes     metric.Int64Histogram
	mailboxDepth   metric.Int64Histogram
	windowDepth    metric.Int64Histogram
	clientMorph    metric.Float64Histogram
	clientApply    metric.Float64Histogram
	resyncBytes    metric.Int64Histogram

	sessionsActive metric.Int64UpDownCounter
	goroutines     metric.Int64UpDownCounter
	trackedBytes   metric.Int64UpDownCounter

	// FragmentLabels enables the opt-in fragment label on the render
	// histogram. It is off by default because the product of fragments and
	// sessions is the one cardinality an application can grow without noticing.
	FragmentLabels bool

	// sources bounds the origin-source label. The mutex guards a process-wide
	// registry rather than any session's state, which is the one place a lock
	// is the right answer in this library.
	sourcesMu sync.Mutex
	sources   map[string]bool

	// The pre-built label sets. One per label a hot path carries; see
	// measurement for why they are built once rather than per call.
	kindAttr      labelOpts
	directionAttr labelOpts
	reasonAttr    labelOpts
	eventAttr     labelOpts
	resultAttr    labelOpts
	opAttr        labelOpts
	siteAttr      labelOpts
	codeAttr      labelOpts
	fragmentAttr  labelOpts
	effectAttr    pairOpts
}

// durationBuckets straddle the 50 ms and 150 ms budgets rather than using the
// generic default set, so the two numbers this project is judged on land
// inside buckets instead of between them.
var durationBuckets = []float64{
	0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025,
	0.05, 0.075, 0.1, 0.15, 0.25, 0.5, 1, 2.5, 5,
}

// NewMetrics builds the metric set from a provider. A nil provider returns a
// nil *Metrics, which is the disabled configuration and is not an error.
func NewMetrics(mp metric.MeterProvider) (*Metrics, error) {
	if mp == nil {
		return nil, nil
	}
	m := mp.Meter("github.com/candacelabs/csf/pkg/gotth")

	var err error
	ms := &Metrics{
		sources:       make(map[string]bool, SourceLabelCap),
		kindAttr:      labelOpts{key: "kind"},
		directionAttr: labelOpts{key: "direction"},
		reasonAttr:    labelOpts{key: "reason"},
		eventAttr:     labelOpts{key: "event"},
		resultAttr:    labelOpts{key: "result"},
		opAttr:        labelOpts{key: "op"},
		siteAttr:      labelOpts{key: "site"},
		codeAttr:      labelOpts{key: "code"},
		fragmentAttr:  labelOpts{key: "fragment"},
		effectAttr:    pairOpts{keyA: "source", keyB: "result"},
	}

	counter := func(name, desc, unit string) metric.Int64Counter {
		c, cerr := m.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if cerr != nil && err == nil {
			err = fmt.Errorf("gotth-live: could not create the metric %s: %w: check the meter provider", name, cerr)
		}
		return c
	}
	seconds := func(name, desc string) metric.Float64Histogram {
		h, herr := m.Float64Histogram(name,
			metric.WithDescription(desc),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(durationBuckets...))
		if herr != nil && err == nil {
			err = fmt.Errorf("gotth-live: could not create the metric %s: %w: check the meter provider", name, herr)
		}
		return h
	}
	amount := func(name, desc, unit string) metric.Int64Histogram {
		h, herr := m.Int64Histogram(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if herr != nil && err == nil {
			err = fmt.Errorf("gotth-live: could not create the metric %s: %w: check the meter provider", name, herr)
		}
		return h
	}
	gauge := func(name, desc, unit string) metric.Int64UpDownCounter {
		g, gerr := m.Int64UpDownCounter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if gerr != nil && err == nil {
			err = fmt.Errorf("gotth-live: could not create the metric %s: %w: check the meter provider", name, gerr)
		}
		return g
	}

	ms.framesReceived = counter("gotthlive_frames_received_total", "Frames accepted from clients, by kind.", "{frame}")
	ms.framesSent = counter("gotthlive_frames_sent_total", "Frames written to clients, by kind. The framer is the only incrementer.", "{frame}")
	ms.framesRejected = counter("gotthlive_frames_rejected_total", "Inbound frames refused, by reason.", "{frame}")
	ms.outboundInvalid = counter("gotthlive_outbound_validation_failed_total", "Constructed frames that failed outbound validation. Never a client's doing: check this library and the bounds it applies to what an application emits.", "{frame}")
	ms.sourceOverflow = counter("gotthlive_source_label_overflow_total", "Origin source values recorded as other because the label cap was reached.", "{value}")
	ms.resyncRequests = counter("gotthlive_resync_requests_total", "Resync requests, by result: snapshot, noop or rate_limited.", "{request}")
	ms.eventsReceived = counter("gotthlive_events_received_total", "Events dispatched to a reducer, by registered name.", "{event}")
	ms.eventsRejected = counter("gotthlive_events_rejected_total", "Events refused before a reducer saw them, by reason.", "{event}")
	ms.transitions = counter("gotthlive_transitions_total", "Reducer invocations, by result: applied, no_change or panicked.", "{transition}")
	ms.patchesSent = counter("gotthlive_patches_sent_total", "Fragment updates emitted, by operation.", "{update}")
	ms.patchesSuppress = counter("gotthlive_patches_suppressed_total", "Renders dropped because their bytes had not changed.", "{render}")
	ms.patchesCoalesce = counter("gotthlive_patches_coalesced_total", "Patches carrying transitions that were not individually emitted.", "{patch}")
	ms.slowClient = counter("gotthlive_slow_client_events_total", "Backpressure events synthesized into a session's own mailbox.", "{event}")
	ms.wireBytes = counter("gotthlive_wire_bytes_total", "Bytes on the wire, by direction.", "By")
	ms.effects = counter("gotthlive_effects_total", "Effects executed, by source and result.", "{effect}")
	ms.effectsAbandon = counter("gotthlive_effects_abandoned_total", "Effects still running when the drain timeout expired.", "{effect}")
	ms.panics = counter("gotthlive_panics_total", "Recovered panics, by site: reduce, render or effect.", "{panic}")
	ms.connections = counter("gotthlive_connections_total", "Connections opened.", "{connection}")
	ms.connectionsShut = counter("gotthlive_connections_closed_total", "Connections closed, by close code label.", "{connection}")
	ms.telemetryDrop = counter("gotthlive_client_telemetry_dropped_total", "Client telemetry reports discarded, by reason.", "{report}")

	ms.reduceDuration = seconds("gotthlive_reduce_duration_seconds", "Time in a reducer.")
	ms.renderDuration = seconds("gotthlive_render_duration_seconds", "Time rendering a transition's dirty fragments.")
	ms.encodeDuration = seconds("gotthlive_encode_duration_seconds", "Time validating and marshalling a frame.")
	ms.sendDuration = seconds("gotthlive_send_duration_seconds", "Time in the transport write, the write-stall signal.")
	ms.clientMorph = seconds("gotthlive_client_morph_duration_seconds", "Client-reported morph duration. Untrusted input.")
	ms.clientApply = seconds("gotthlive_client_apply_duration_seconds", "Client-reported apply duration. Untrusted input.")

	ms.frameBytes = amount("gotthlive_frame_bytes", "Encoded frame size, by direction.", "By")
	ms.mailboxDepth = amount("gotthlive_mailbox_depth", "Mailbox occupancy, sampled per step.", "{message}")
	ms.windowDepth = amount("gotthlive_outbound_window_depth", "Unacknowledged patches in flight, the slow-client signal before eviction.", "{patch}")
	ms.resyncBytes = amount("gotthlive_resync_bytes", "Encoded size of a resync snapshot.", "By")

	ms.sessionsActive = gauge("gotthlive_sessions_active", "Live sessions.", "{session}")
	ms.goroutines = gauge("gotthlive_goroutines", "Goroutines this library owns.", "{goroutine}")
	ms.trackedBytes = gauge("gotthlive_session_tracked_bytes", "Bytes in structures the library owns and can size exactly.", "By")

	if err != nil {
		return nil, err
	}
	return ms, nil
}

// Enabled reports whether metrics are being recorded.
func (m *Metrics) Enabled() bool { return m != nil }

// add records one counter increment under a pre-built label set.
//
// The option slice is passed rather than constructed, which is the difference
// between one heap allocation per measurement and none: a variadic call site
// builds a fresh slice, and the slice escapes into an interface method.
func add(ctx context.Context, c metric.Int64Counter, n int64, o *measurement) {
	if c == nil {
		return
	}
	c.Add(ctx, n, o.add...)
}

// record does the same for an integer histogram.
func record(ctx context.Context, h metric.Int64Histogram, v int64, o *measurement) {
	if h == nil {
		return
	}
	h.Record(ctx, v, o.record...)
}

// FrameReceived counts one accepted inbound frame and its size.
//
// This is the connection read pump's per-frame path, and the one the G2
// baseline's goroutine-stack line turned out to be about (§5.1, and the
// remediation section that corrects it). Every label set it uses is built once
// per label value and reused.
func (m *Metrics) FrameReceived(ctx context.Context, kind string, bytes int) {
	if m == nil {
		return
	}
	add(ctx, m.framesReceived, 1, m.kindAttr.of(kind))
	in := m.directionAttr.of("in")
	add(ctx, m.wireBytes, int64(bytes), in)
	record(ctx, m.frameBytes, int64(bytes), in)
}

// FrameSent counts one written frame and its size. Only the framer calls it.
func (m *Metrics) FrameSent(ctx context.Context, kind string, bytes int) {
	if m == nil {
		return
	}
	add(ctx, m.framesSent, 1, m.kindAttr.of(kind))
	out := m.directionAttr.of("out")
	add(ctx, m.wireBytes, int64(bytes), out)
	record(ctx, m.frameBytes, int64(bytes), out)
}

// FrameRejected counts one refused inbound frame.
func (m *Metrics) FrameRejected(ctx context.Context, reason string) {
	if m == nil {
		return
	}
	add(ctx, m.framesRejected, 1, m.reasonAttr.of(reason))
}

// OutboundInvalid counts a frame this library constructed and could not
// validate. Any non-zero value is actionable, and it is never a client's doing:
// the frame was built here, from state this library owns.
//
// It used to say "any non-zero value is a library bug", and that sent the
// person holding the pager to the wrong repository. Some of what a frame
// carries comes from the application — Event.Contributing above all — and until
// D-18 an over-long one reached the outbound validator and was counted here, so
// an application could raise an alert about its own input that named this
// library as the defect. The emit path now rejects that input with an error
// naming the caller, so what is left really is a defect in this library: either
// a frame built wrong, or a bound it failed to apply before building one.
func (m *Metrics) OutboundInvalid(ctx context.Context, kind string) {
	if m == nil {
		return
	}
	add(ctx, m.outboundInvalid, 1, m.kindAttr.of(kind))
}

// EventReceived counts one event dispatched to a reducer.
func (m *Metrics) EventReceived(ctx context.Context, name string) {
	if m == nil {
		return
	}
	add(ctx, m.eventsReceived, 1, m.eventAttr.of(name))
}

// EventRejected counts one event refused before a reducer saw it.
func (m *Metrics) EventRejected(ctx context.Context, reason string) {
	if m == nil {
		return
	}
	add(ctx, m.eventsRejected, 1, m.reasonAttr.of(reason))
}

// Transition counts one reducer invocation by result.
func (m *Metrics) Transition(ctx context.Context, result string, seconds float64, event string) {
	if m == nil {
		return
	}
	add(ctx, m.transitions, 1, m.resultAttr.of(result))
	if m.reduceDuration != nil {
		m.reduceDuration.Record(ctx, seconds, m.eventAttr.of(event).record...)
	}
}

// RenderDuration records the time spent rendering a transition's fragments.
func (m *Metrics) RenderDuration(ctx context.Context, seconds float64, fragment string) {
	if m == nil || m.renderDuration == nil {
		return
	}
	if m.FragmentLabels && fragment != "" {
		m.renderDuration.Record(ctx, seconds, m.fragmentAttr.of(fragment).record...)
		return
	}
	m.renderDuration.Record(ctx, seconds)
}

// EncodeDuration records validation plus marshal time.
func (m *Metrics) EncodeDuration(ctx context.Context, seconds float64) {
	if m == nil || m.encodeDuration == nil {
		return
	}
	m.encodeDuration.Record(ctx, seconds)
}

// SendDuration records time in the transport write.
func (m *Metrics) SendDuration(ctx context.Context, seconds float64) {
	if m == nil || m.sendDuration == nil {
		return
	}
	m.sendDuration.Record(ctx, seconds)
}

// PatchesSent counts emitted fragment updates by operation.
func (m *Metrics) PatchesSent(ctx context.Context, op string, n int) {
	if m == nil || n == 0 {
		return
	}
	add(ctx, m.patchesSent, int64(n), m.opAttr.of(op))
}

// PatchesSuppressed counts renders dropped for producing unchanged bytes.
func (m *Metrics) PatchesSuppressed(ctx context.Context, n int) {
	if m == nil || n == 0 {
		return
	}
	add(ctx, m.patchesSuppress, int64(n), noAttrs)
}

// PatchCoalesced counts one patch carrying transitions that were not
// individually emitted.
func (m *Metrics) PatchCoalesced(ctx context.Context) {
	if m == nil {
		return
	}
	add(ctx, m.patchesCoalesce, 1, noAttrs)
}

// SlowClientEvent counts one synthesized backpressure event.
func (m *Metrics) SlowClientEvent(ctx context.Context) {
	if m == nil {
		return
	}
	add(ctx, m.slowClient, 1, noAttrs)
}

// ResyncRequest counts one resync request by result, and the snapshot's size
// when it produced one.
func (m *Metrics) ResyncRequest(ctx context.Context, result string, bytes int) {
	if m == nil {
		return
	}
	add(ctx, m.resyncRequests, 1, m.resultAttr.of(result))
	if bytes > 0 && m.resyncBytes != nil {
		m.resyncBytes.Record(ctx, int64(bytes))
	}
}

// Effect counts one executed effect. The source label is capped, because
// nothing registers an effect.
func (m *Metrics) Effect(ctx context.Context, source, result string) {
	if m == nil {
		return
	}
	add(ctx, m.effects, 1, m.effectAttr.of(m.SourceLabel(ctx, source), result))
}

// EffectAbandoned counts one effect still running when the drain window closed.
func (m *Metrics) EffectAbandoned(ctx context.Context) {
	if m == nil {
		return
	}
	add(ctx, m.effectsAbandon, 1, noAttrs)
}

// Panic counts one recovered panic by site.
func (m *Metrics) Panic(ctx context.Context, site string) {
	if m == nil {
		return
	}
	add(ctx, m.panics, 1, m.siteAttr.of(site))
}

// ConnectionOpened counts a connection admitted to the live registry.
func (m *Metrics) ConnectionOpened(ctx context.Context) {
	if m == nil {
		return
	}
	add(ctx, m.connections, 1, noAttrs)
}

// ConnectionClosed counts one close by its code label.
func (m *Metrics) ConnectionClosed(ctx context.Context, codeLabel string) {
	if m == nil {
		return
	}
	add(ctx, m.connectionsShut, 1, m.codeAttr.of(codeLabel))
}

// SessionsActive follows registry membership, independently of handshake refusals.
func (m *Metrics) SessionsActive(ctx context.Context, delta int64) {
	if m == nil || m.sessionsActive == nil {
		return
	}
	m.sessionsActive.Add(ctx, delta)
}

// Goroutines adjusts the count of goroutines this library owns.
func (m *Metrics) Goroutines(ctx context.Context, delta int64) {
	if m == nil || m.goroutines == nil {
		return
	}
	m.goroutines.Add(ctx, delta)
}

// TrackedBytes adjusts the exactly-sized per-session total. It covers only
// structures the library owns and can size: the window, the mailbox and ack
// backing arrays, the fragment hashes and the registry. Go has no
// per-goroutine heap attribution and this does not pretend otherwise.
func (m *Metrics) TrackedBytes(ctx context.Context, delta int64) {
	if m == nil || m.trackedBytes == nil {
		return
	}
	m.trackedBytes.Add(ctx, delta)
}

// MailboxDepth samples mailbox occupancy.
func (m *Metrics) MailboxDepth(ctx context.Context, depth int) {
	if m == nil || m.mailboxDepth == nil {
		return
	}
	m.mailboxDepth.Record(ctx, int64(depth))
}

// WindowDepth samples the unacknowledged window, which is the slow-client
// signal and is exported so degradation is visible before eviction.
func (m *Metrics) WindowDepth(ctx context.Context, depth int) {
	if m == nil || m.windowDepth == nil {
		return
	}
	m.windowDepth.Record(ctx, int64(depth))
}

// ClientTiming records a client-reported morph and apply duration. Both are
// untrusted input, are named client_ so no dashboard mistakes them for a
// server measurement, and are bounded by the schema so a fabricated value
// cannot skew a histogram to infinity.
func (m *Metrics) ClientTiming(ctx context.Context, morphMicros, applyMicros uint32) {
	if m == nil {
		return
	}
	if m.clientMorph != nil {
		m.clientMorph.Record(ctx, float64(morphMicros)/1e6)
	}
	if m.clientApply != nil {
		m.clientApply.Record(ctx, float64(applyMicros)/1e6)
	}
}

// ClientTelemetryDropped counts a report naming a patch this session did not
// send, which is either a forgery or a stale echo and is never used to
// fabricate a span.
func (m *Metrics) ClientTelemetryDropped(ctx context.Context, reason string) {
	if m == nil {
		return
	}
	add(ctx, m.telemetryDrop, 1, m.reasonAttr.of(reason))
}

// SourceLabel maps an origin source to its label value, collapsing to "other"
// once the cap is reached and counting that it happened.
//
// Traces and the provenance log carry the full value; only the label is
// capped, so nothing is lost, it is only moved to the signal that can afford
// the cardinality.
func (m *Metrics) SourceLabel(ctx context.Context, source string) string {
	if m == nil {
		return source
	}
	m.sourcesMu.Lock()
	known := m.sources[source]
	if !known && len(m.sources) < SourceLabelCap {
		m.sources[source] = true
		known = true
	}
	m.sourcesMu.Unlock()

	if known {
		return source
	}
	add(ctx, m.sourceOverflow, 1, noAttrs)
	return SourceOverflowLabel
}
