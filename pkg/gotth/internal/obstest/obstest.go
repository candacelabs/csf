// Package obstest records what the library actually emits, so that a spec can
// assert on a signal rather than on a method having been called.
//
// It exists because two exit criteria — the metric set flowing, and one trace
// spanning receive through send — rested on no-op providers, which exercise
// every call site and assert nothing about the result. A no-op provider cannot
// distinguish "the counter was incremented with the right name and labels"
// from "a method was called".
//
// # Why this is hand-written rather than the OpenTelemetry SDK
//
// The SDK has a manual reader and a span recorder built for exactly this, and
// using them would cost a consumer five modules in their build list: the SDK,
// the metric SDK, an experimental metric package, google/uuid and goleak. The
// testing frameworks this project mandates already put sixteen unlinked
// modules into a consumer's graph, which the dependency ledger discloses rather
// than hides, and adding five more for an assertion this size is a poor trade.
//
// The cost of writing it instead is small because the OpenTelemetry API ships
// no-op implementations designed to be embedded: each type below embeds the
// no-op and overrides only the handful of methods a spec reads. New API methods
// therefore arrive as no-ops rather than as build failures, which is the right
// default for a recorder.
//
// Nothing here is a general-purpose OpenTelemetry implementation. It records
// what was emitted, in order, with attributes flattened to strings, and it is
// safe for concurrent use because the library emits from several goroutines.
package obstest

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	traceembedded "go.opentelemetry.io/otel/trace/embedded"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Measurement is one recorded observation.
type Measurement struct {
	// Instrument is the metric name, exactly as the library registered it.
	Instrument string
	// Value is the amount added or recorded.
	Value float64
	// Attrs are the observation's attributes, flattened to strings so a spec
	// can compare them without reproducing OpenTelemetry's value model.
	Attrs map[string]string
}

// Attr returns one attribute's value, or the empty string.
func (m Measurement) Attr(key string) string { return m.Attrs[key] }

// Metrics is a metric.MeterProvider that records every observation.
type Metrics struct {
	embedded.MeterProvider

	mu           sync.Mutex
	measurements []Measurement
	registered   map[string]bool
}

// NewMetrics returns a recording meter provider.
func NewMetrics() *Metrics {
	return &Metrics{registered: map[string]bool{}}
}

// Meter returns a recording meter. The name is ignored: this library uses one.
func (m *Metrics) Meter(name string, options ...metric.MeterOption) metric.Meter {
	return recordingMeter{rec: m}
}

func (m *Metrics) register(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered[name] = true
}

func (m *Metrics) record(name string, value float64, attrs []attribute.KeyValue) {
	flat := make(map[string]string, len(attrs))
	for _, a := range attrs {
		flat[string(a.Key)] = a.Value.String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.measurements = append(m.measurements, Measurement{Instrument: name, Value: value, Attrs: flat})
}

// Registered reports every instrument name the library created, sorted. It is
// what a spec asserts the catalogue against: an instrument that is never
// created cannot ever be emitted, and that failure is silent at runtime.
func (m *Metrics) Registered() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.registered))
	for name := range m.registered {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// All returns every observation, in the order it was recorded.
func (m *Metrics) All() []Measurement {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Measurement(nil), m.measurements...)
}

// Observations returns the observations of one instrument.
func (m *Metrics) Observations(instrument string) []Measurement {
	var out []Measurement
	for _, obs := range m.All() {
		if obs.Instrument == instrument {
			out = append(out, obs)
		}
	}
	return out
}

// Total sums one instrument's observations, which is what a counter means.
func (m *Metrics) Total(instrument string) float64 {
	var sum float64
	for _, obs := range m.Observations(instrument) {
		sum += obs.Value
	}
	return sum
}

// recordingMeter embeds the no-op meter and overrides the four instrument
// kinds this library uses. Everything else stays a no-op, so a new API method
// is not a build failure here.
type recordingMeter struct {
	metricnoop.Meter
	rec *Metrics
}

func (m recordingMeter) Int64Counter(name string, _ ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	m.rec.register(name)
	return int64Adder{name: name, rec: m.rec}, nil
}

func (m recordingMeter) Int64UpDownCounter(name string, _ ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	m.rec.register(name)
	return int64UpDown{name: name, rec: m.rec}, nil
}

func (m recordingMeter) Int64Histogram(name string, _ ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	m.rec.register(name)
	return int64Recorder{name: name, rec: m.rec}, nil
}

func (m recordingMeter) Float64Histogram(name string, _ ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	m.rec.register(name)
	return float64Recorder{name: name, rec: m.rec}, nil
}

type int64Adder struct {
	metricnoop.Int64Counter
	name string
	rec  *Metrics
}

func (i int64Adder) Add(_ context.Context, v int64, opts ...metric.AddOption) {
	i.rec.record(i.name, float64(v), addAttrs(opts))
}

type int64UpDown struct {
	metricnoop.Int64UpDownCounter
	name string
	rec  *Metrics
}

func (i int64UpDown) Add(_ context.Context, v int64, opts ...metric.AddOption) {
	i.rec.record(i.name, float64(v), addAttrs(opts))
}

type int64Recorder struct {
	metricnoop.Int64Histogram
	name string
	rec  *Metrics
}

func (i int64Recorder) Record(_ context.Context, v int64, opts ...metric.RecordOption) {
	i.rec.record(i.name, float64(v), recordAttrs(opts))
}

type float64Recorder struct {
	metricnoop.Float64Histogram
	name string
	rec  *Metrics
}

func (f float64Recorder) Record(_ context.Context, v float64, opts ...metric.RecordOption) {
	f.rec.record(f.name, v, recordAttrs(opts))
}

// The API models attributes as options, and the only way to read them back is
// to apply the options to the config the API provides for that purpose.
func addAttrs(opts []metric.AddOption) []attribute.KeyValue {
	set := metric.NewAddConfig(opts).Attributes()
	return set.ToSlice()
}

func recordAttrs(opts []metric.RecordOption) []attribute.KeyValue {
	set := metric.NewRecordConfig(opts).Attributes()
	return set.ToSlice()
}

// Span is one recorded span.
type Span struct {
	// Name is the span name as the library asked for it.
	Name string

	// TraceID and SpanID are the recorded identity. They are minted by this
	// package's own provider, so a spec can assert two spans share a trace.
	TraceID trace.TraceID

	// SpanID identifies this span within TraceID.
	SpanID trace.SpanID

	// ParentID is the enclosing span, zero for a root. It is what a spec
	// asserting on span structure reads.
	ParentID trace.SpanID
	// Attrs are the span's attributes, flattened to strings.
	Attrs map[string]string
	// Links are the span contexts this span was linked to, which is how a
	// client's morph timing rejoins the trace that produced the patch.
	Links []trace.SpanContext

	// Ended reports whether End was called. A span recorded but never ended is
	// a leak the library would otherwise ship silently, so it is observable
	// here rather than inferred.
	Ended bool
}

// Attr returns one attribute's value, or the empty string.
func (s Span) Attr(key string) string { return s.Attrs[key] }

// Traces is a trace.TracerProvider that records every span.
type Traces struct {
	traceembedded.TracerProvider

	mu    sync.Mutex
	spans []*Span
	next  uint64
	trace trace.TraceID
}

// NewTraces returns a recording tracer provider. Every span it creates shares
// one trace identifier, because this library is the trace root and the
// property under test is that one trace spans the whole path.
func NewTraces() *Traces {
	t := &Traces{}
	t.trace = trace.TraceID{0x9e, 0x1c, 0x2f, 0x44, 0x5a, 0x60, 0x71, 0x82,
		0x93, 0xa4, 0xb5, 0xc6, 0xd7, 0xe8, 0xf9, 0x0a}
	return t
}

// Tracer returns a recording tracer. The name is ignored: this library uses one.
func (t *Traces) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return recordingTracer{rec: t}
}

// Spans returns every span created, in creation order.
func (t *Traces) Spans() []Span {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]Span, 0, len(t.spans))
	for _, s := range t.spans {
		// A struct copy shares the map and slice headers, and the actor's
		// goroutine keeps writing through them after the snapshot is taken —
		// the caller must get contents, not live references.
		c := *s
		c.Attrs = make(map[string]string, len(s.Attrs))
		for k, v := range s.Attrs {
			c.Attrs[k] = v
		}
		c.Links = append([]trace.SpanContext(nil), s.Links...)
		out = append(out, c)
	}
	return out
}

// Named returns the spans with one name.
func (t *Traces) Named(name string) []Span {
	var out []Span
	for _, s := range t.Spans() {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// Names returns every span name recorded, in creation order and with
// duplicates removed.
func (t *Traces) Names() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range t.Spans() {
		if !seen[s.Name] {
			seen[s.Name] = true
			out = append(out, s.Name)
		}
	}
	return out
}

type recordingTracer struct {
	tracenoop.Tracer
	rec *Traces
}

func (t recordingTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)

	t.rec.mu.Lock()
	t.rec.next++
	var id trace.SpanID
	for i := 0; i < 8; i++ {
		id[7-i] = byte(t.rec.next >> (8 * i))
	}
	recorded := &Span{
		Name:     name,
		TraceID:  t.rec.trace,
		SpanID:   id,
		ParentID: trace.SpanContextFromContext(ctx).SpanID(),
		Attrs:    map[string]string{},
		Links:    make([]trace.SpanContext, 0, len(cfg.Links())),
	}
	for _, a := range cfg.Attributes() {
		recorded.Attrs[string(a.Key)] = a.Value.String()
	}
	for _, l := range cfg.Links() {
		recorded.Links = append(recorded.Links, l.SpanContext)
	}
	t.rec.spans = append(t.rec.spans, recorded)
	t.rec.mu.Unlock()

	live := &recordingSpan{rec: t.rec, span: recorded}
	ctx = trace.ContextWithSpan(ctx, live)
	return ctx, live
}

type recordingSpan struct {
	tracenoop.Span
	rec  *Traces
	span *Span
}

func (s *recordingSpan) SpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    s.span.TraceID,
		SpanID:     s.span.SpanID,
		TraceFlags: trace.FlagsSampled,
	})
}

func (s *recordingSpan) IsRecording() bool { return true }

func (s *recordingSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	for _, a := range attrs {
		s.span.Attrs[string(a.Key)] = a.Value.String()
	}
}

func (s *recordingSpan) End(options ...trace.SpanEndOption) {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.span.Ended = true
}

func (s *recordingSpan) RecordError(err error, _ ...trace.EventOption) {
	if err == nil {
		return
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.span.Attrs["error"] = err.Error()
}

// Describe renders the recorded spans for a failure message, because a trace
// assertion that fails without saying what was recorded costs a rerun.
func (t *Traces) Describe() string {
	out := ""
	for _, s := range t.Spans() {
		out += fmt.Sprintf("  %-28s trace=%s span=%s parent=%s attrs=%v links=%d ended=%v\n",
			s.Name, s.TraceID, s.SpanID, s.ParentID, s.Attrs, len(s.Links), s.Ended)
	}
	return out
}
