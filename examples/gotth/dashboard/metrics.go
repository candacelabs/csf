package main

// An OpenTelemetry meter provider built out of the OTel API alone, so that this
// example can show the FR-34 backpressure metrics moving.
//
// PRD Phase 3 asks for queue depth, drops and coalesce ratio to be exported and
// demonstrated. The library exports them through `Config.Metrics`, which takes a
// `metric.MeterProvider` — an interface from `go.opentelemetry.io/otel/metric`,
// the API module, which is already in this module's build graph because
// gotth-live itself depends on it. Nothing here adds a module to anybody's
// `go list -m all`, and that is the reason this is 180 lines of interface
// implementation rather than three lines of `sdkmetric.NewMeterReader`: the OTel
// **SDK** is a separate module, dependencies.md §2.3 admits it only into
// satellite modules nobody builds, and an example is a thing people copy. A
// reader who copies this file gets a working metrics sink and no new
// dependency; a reader who wants Prometheus swaps this for the OTel Prometheus
// exporter and changes one line in main.go.
//
// What it does NOT do is aggregate the way a real backend does. There is no
// temporality, no delta/cumulative distinction, no exemplars, and histograms
// keep four summary numbers instead of buckets. That is enough for "did the
// counter move, and by how much", which is what a demonstration and a spec both
// need, and it is deliberately not enough to be mistaken for a metrics pipeline.
//
// It is bounded. An earlier shape retained every observation, which is what
// internal/obstest does because a spec wants the sequence — but this one runs
// inside a demo that may be left open for a day at twenty samples a second per
// session, and a metrics sink whose memory grows with the traffic it measures
// would be an unbounded allocation shipped as an example of resilience. So
// counters sum in place, histograms fold in place, and the map is keyed by the
// instrument and its attributes, whose cardinality the library bounds
// (instrumentation.md §2.1).

import (
	"context"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// The instrument names this example reads by name. They are the library's, from
// docs/instrumentation.md §2.2 and §2.3, and they are constants here so that a
// rename upstream is a compile-time change in one place rather than a spec that
// quietly asserts about a series nobody emits any more.
//
// Nothing enforces that these strings exist — a meter provider is told the names
// the library creates and cannot ask for a name the library never created. What
// stands in for enforcement is Meters.Registered plus the spec in
// wire_test.go that asserts each of these was registered, so a typo here is a
// red spec rather than a permanently-zero number in a report.
const (
	// MetricWindowDepth is the outbound window occupancy: unacknowledged
	// patches in flight. This is FR-34's "server→client queue depth", and it is
	// the queue whose bound FR-51 makes a claim about.
	MetricWindowDepth = "gotthlive_outbound_window_depth"
	// MetricMailboxDepth is the inbound mailbox occupancy, the other queue.
	MetricMailboxDepth = "gotthlive_mailbox_depth"
	// MetricPatchesCoalesced counts patches that carried transitions which were
	// never individually emitted — backpressure stage 1.
	MetricPatchesCoalesced = "gotthlive_patches_coalesced_total"
	// MetricSlowClientEvents counts stage 2: the window went full and the
	// library synthesized a backpressure event into the session's own mailbox.
	MetricSlowClientEvents = "gotthlive_slow_client_events_total"
	// MetricFramesSent counts frames written, by kind. It is the denominator of
	// the coalesce ratio.
	MetricFramesSent = "gotthlive_frames_sent_total"
	// MetricPatchesSuppressed counts renders dropped for producing bytes the
	// client already has. It is the closest thing the library exports to
	// FR-34's "patch drops"; see Meters.Report for why that is not a synonym,
	// and FRICTION.md item F-5 for why the requirement's wording is what should
	// move rather than the library's metric set. It is also, per FRICTION.md
	// O-1, the only place an over-declared fragment Dirty is visible at all,
	// which is why two of this example's specs assert it is zero.
	MetricPatchesSuppressed = "gotthlive_patches_suppressed_total"
	// MetricEventsRejected counts events refused before a reducer saw them, by
	// reason. `mailbox_full` is an inbound drop and is real.
	MetricEventsRejected = "gotthlive_events_rejected_total"
	// MetricFramesRejected counts inbound frames refused, by reason.
	// `ack_channel_full` is the one place the library documents a drop policy.
	MetricFramesRejected = "gotthlive_frames_rejected_total"
	// MetricResyncBytes is the encoded size of a resync snapshot, which is the
	// library's own answer to the Phase 3 resync-cost criterion. resync.go
	// measures the same thing off the wire; the two are compared in the report,
	// because a number that agrees with an independently-derived one is worth
	// more than either alone.
	MetricResyncBytes = "gotthlive_resync_bytes"
	// MetricWireBytes counts bytes on the wire by direction.
	MetricWireBytes = "gotthlive_wire_bytes_total"
)

// Distribution is what this sink keeps for a histogram: enough to state a mean
// and a worst case, and deliberately not enough to state a percentile.
//
// A p99 needs the values or a bucket layout, and a sink that reported one from
// four numbers would be inventing it. The library records real histograms into
// whatever provider it is given; what is thin here is this example's sink, and
// saying so is cheaper than a percentile nobody can defend.
type Distribution struct {
	Count int64
	Sum   float64
	Last  float64
	Max   float64
}

// Mean is the arithmetic mean, or zero for an empty distribution.
func (d Distribution) Mean() float64 {
	if d.Count == 0 {
		return 0
	}
	return d.Sum / float64(d.Count)
}

// Meters is a metric.MeterProvider that folds every observation into a bounded
// summary.
//
// It is safe for concurrent use: the library records from every session's
// goroutine at once, and the HTTP handler that prints the report reads from
// another.
type Meters struct {
	embedded.MeterProvider

	mu         sync.Mutex
	counters   map[string]float64
	histograms map[string]*Distribution
	registered map[string]bool
}

// NewMeters returns an empty sink.
func NewMeters() *Meters {
	return &Meters{
		counters:   map[string]float64{},
		histograms: map[string]*Distribution{},
		registered: map[string]bool{},
	}
}

// Meter returns the recording meter. The name is ignored: the library uses one.
func (m *Meters) Meter(string, ...metric.MeterOption) metric.Meter {
	return recordingMeter{sink: m}
}

// Registered reports every instrument the library asked this provider to
// create, sorted.
//
// It is the only way to tell "this metric is zero" from "this metric does not
// exist", and those are different facts: the first is a system at rest and the
// second is a requirement nobody implemented. An instrument that is never
// created can never be emitted, and that failure is completely silent at
// runtime.
func (m *Meters) Registered() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.registered))
	for name := range m.registered {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// Counter returns one counter's total across every attribute set.
func (m *Meters) Counter(name string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	var sum float64
	for key, v := range m.counters {
		if instrumentOf(key) == name {
			sum += v
		}
	}
	return sum
}

// CounterWith returns one counter's total for a single attribute value —
// `gotthlive_frames_sent_total{kind="patch"}` and nothing else.
func (m *Meters) CounterWith(name, key, value string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[seriesKey(name, []attribute.KeyValue{attribute.String(key, value)})]
}

// Histogram returns one histogram's summary across every attribute set.
func (m *Meters) Histogram(name string) Distribution {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out Distribution
	for key, d := range m.histograms {
		if instrumentOf(key) != name {
			continue
		}
		out.Count += d.Count
		out.Sum += d.Sum
		out.Max = math.Max(out.Max, d.Max)
		out.Last = d.Last
	}
	return out
}

// CoalesceRatio is the fraction of patch frames that carried a transition which
// was never individually emitted.
//
// It is DERIVED, and the derivation is the point. PRD Phase 3 asks for a
// "coalesce ratio" and the library exports no such series, correctly: a ratio of
// two counters is a recording rule, not a metric, and instrumentation.md §2.2
// makes the same argument for reconnects. The numerator is
// `gotthlive_patches_coalesced_total`; the denominator is the patch frames the
// framer wrote, which is `gotthlive_frames_sent_total{kind="patch"}` and not
// `gotthlive_patches_sent_total` — that one counts fragment updates, so a patch
// carrying three regions increments it three times and would make this ratio
// silently wrong in the safe-looking direction.
//
// Zero patch frames returns zero rather than NaN. A session that sent nothing
// coalesced nothing, and a NaN in a report is a number nobody can act on.
func (m *Meters) CoalesceRatio() float64 {
	sent := m.CounterWith(MetricFramesSent, "kind", "patch")
	if sent == 0 {
		return 0
	}
	return m.Counter(MetricPatchesCoalesced) / sent
}

// Report writes the backpressure picture as plain text.
//
// It is what `/metrics.txt` serves and what the resync measurement prints. It is
// NOT the OpenTelemetry or Prometheus exposition format and does not pretend to
// be: a real deployment points `Config.Metrics` at an exporter, and this is a
// demonstration that the numbers exist and move.
func (m *Meters) Report(w io.Writer) {
	window := m.Histogram(MetricWindowDepth)
	mailbox := m.Histogram(MetricMailboxDepth)

	fmt.Fprintf(w, "queue depth (FR-34)\n")
	fmt.Fprintf(w, "  %-40s samples=%d mean=%.2f max=%.0f last=%.0f\n",
		MetricWindowDepth, window.Count, window.Mean(), window.Max, window.Last)
	fmt.Fprintf(w, "  %-40s samples=%d mean=%.2f max=%.0f last=%.0f\n",
		MetricMailboxDepth, mailbox.Count, mailbox.Mean(), mailbox.Max, mailbox.Last)

	fmt.Fprintf(w, "\nbackpressure ladder\n")
	fmt.Fprintf(w, "  %-40s %.0f\n", MetricPatchesCoalesced, m.Counter(MetricPatchesCoalesced))
	fmt.Fprintf(w, "  %-40s %.0f\n", MetricSlowClientEvents, m.Counter(MetricSlowClientEvents))
	fmt.Fprintf(w, "  %-40s %.0f\n",
		MetricFramesSent+`{kind="patch"}`, m.CounterWith(MetricFramesSent, "kind", "patch"))
	fmt.Fprintf(w, "  %-40s %.3f  (derived: coalesced / patch frames)\n",
		"coalesce_ratio", m.CoalesceRatio())

	// The honest paragraph. FR-34 names "patch drops" and this library has no
	// such counter, because on this design a patch is never dropped: under
	// pressure it coalesces into the next one with its provenance intact, then
	// it is deferred entirely, and if the client still does not acknowledge the
	// SESSION is closed with `slow_client`. Losing a patch while keeping the
	// connection would make the DOM disagree with the server with nothing
	// saying so, which is the one outcome the protocol will not produce. What
	// follows are the drops that are real, each of a different thing.
	fmt.Fprintf(w, "\ndrops — of things that are actually dropped\n")
	fmt.Fprintf(w, "  %-40s %.0f  (identical bytes, not a loss)\n",
		MetricPatchesSuppressed, m.Counter(MetricPatchesSuppressed))
	fmt.Fprintf(w, "  %-40s %.0f\n",
		MetricEventsRejected+`{reason="mailbox_full"}`,
		m.CounterWith(MetricEventsRejected, "reason", "mailbox_full"))
	fmt.Fprintf(w, "  %-40s %.0f\n",
		MetricFramesRejected+`{reason="ack_channel_full"}`,
		m.CounterWith(MetricFramesRejected, "reason", "ack_channel_full"))
	fmt.Fprintf(w, "  a patch itself is never dropped: it coalesces, then defers,\n")
	fmt.Fprintf(w, "  then the session is closed with slow_client. See README.md.\n")

	fmt.Fprintf(w, "\nwire\n")
	fmt.Fprintf(w, "  %-40s %.0f\n",
		MetricWireBytes+`{direction="out"}`, m.CounterWith(MetricWireBytes, "direction", "out"))
	fmt.Fprintf(w, "  %-40s %.0f\n",
		MetricWireBytes+`{direction="in"}`, m.CounterWith(MetricWireBytes, "direction", "in"))
}

func (m *Meters) register(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered[name] = true
}

func (m *Meters) add(name string, v float64, attrs []attribute.KeyValue) {
	key := seriesKey(name, attrs)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[key] += v
}

func (m *Meters) record(name string, v float64, attrs []attribute.KeyValue) {
	key := seriesKey(name, attrs)
	m.mu.Lock()
	defer m.mu.Unlock()

	d := m.histograms[key]
	if d == nil {
		d = &Distribution{}
		m.histograms[key] = d
	}
	d.Count++
	d.Sum += v
	d.Last = v
	d.Max = math.Max(d.Max, v)
}

// seriesKey identifies one series: the instrument plus its attribute set, in a
// stable order. Sorted because the library is free to pass attributes in any
// order and a map keyed by an unsorted rendering would split one series in two.
func seriesKey(name string, attrs []attribute.KeyValue) string {
	if len(attrs) == 0 {
		return name
	}
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		parts = append(parts, string(a.Key)+"="+a.Value.String())
	}
	slices.Sort(parts)
	return name + "{" + strings.Join(parts, ",") + "}"
}

// instrumentOf recovers the instrument name from a series key.
func instrumentOf(key string) string {
	if i := strings.IndexByte(key, '{'); i >= 0 {
		return key[:i]
	}
	return key
}

// recordingMeter embeds the API's no-op meter and overrides the four instrument
// kinds this library creates. Everything else stays a no-op, so a new method on
// the OTel API is not a build failure here — which matters for a file a reader
// is invited to copy.
type recordingMeter struct {
	metricnoop.Meter
	sink *Meters
}

func (m recordingMeter) Int64Counter(name string, _ ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	m.sink.register(name)
	return counterInstrument{name: name, sink: m.sink}, nil
}

func (m recordingMeter) Int64UpDownCounter(name string, _ ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	m.sink.register(name)
	return upDownInstrument{name: name, sink: m.sink}, nil
}

func (m recordingMeter) Int64Histogram(name string, _ ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	m.sink.register(name)
	return int64Instrument{name: name, sink: m.sink}, nil
}

func (m recordingMeter) Float64Histogram(name string, _ ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	m.sink.register(name)
	return float64Instrument{name: name, sink: m.sink}, nil
}

type counterInstrument struct {
	metricnoop.Int64Counter
	name string
	sink *Meters
}

func (c counterInstrument) Add(_ context.Context, v int64, opts ...metric.AddOption) {
	c.sink.add(c.name, float64(v), addAttrs(opts))
}

type upDownInstrument struct {
	metricnoop.Int64UpDownCounter
	name string
	sink *Meters
}

func (c upDownInstrument) Add(_ context.Context, v int64, opts ...metric.AddOption) {
	c.sink.add(c.name, float64(v), addAttrs(opts))
}

type int64Instrument struct {
	metricnoop.Int64Histogram
	name string
	sink *Meters
}

func (h int64Instrument) Record(_ context.Context, v int64, opts ...metric.RecordOption) {
	h.sink.record(h.name, float64(v), recordAttrs(opts))
}

type float64Instrument struct {
	metricnoop.Float64Histogram
	name string
	sink *Meters
}

func (h float64Instrument) Record(_ context.Context, v float64, opts ...metric.RecordOption) {
	h.sink.record(h.name, v, recordAttrs(opts))
}

// The API models attributes as options, and the only way to read them back is to
// apply the options to the config the API provides for that purpose.
func addAttrs(opts []metric.AddOption) []attribute.KeyValue {
	set := metric.NewAddConfig(opts).Attributes()
	return set.ToSlice()
}

func recordAttrs(opts []metric.RecordOption) []attribute.KeyValue {
	set := metric.NewRecordConfig(opts).Attributes()
	return set.ToSlice()
}
