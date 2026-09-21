package obs

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Span attribute keys. They are constants because a typo in an attribute key
// is invisible until someone queries for it and finds nothing.
const (
	AttrSessionID     = "gotthlive.session.id"
	AttrEventID       = "gotthlive.event.id"
	AttrEventName     = "gotthlive.event.name"
	AttrClientRef     = "gotthlive.event.client_ref"
	AttrSeenServerSeq = "gotthlive.event.seen_server_seq"
	AttrTransitionID  = "gotthlive.transition.id"
	AttrStateVersion  = "gotthlive.state.version"
	AttrPatchID       = "gotthlive.patch.id"
	AttrServerSeq     = "gotthlive.server_seq"
	AttrFragmentID    = "gotthlive.fragment.id"
	AttrSuppressed    = "gotthlive.fragment.suppressed"
	AttrFrameBytes    = "gotthlive.frame.bytes"
	AttrFrameKind     = "gotthlive.frame.kind"
	AttrWindowDepth   = "gotthlive.window.depth"
	AttrOriginKind    = "gotthlive.origin.kind"
	AttrOriginSource  = "gotthlive.origin.source"
	AttrResult        = "gotthlive.result"
	AttrMorphMicros   = "gotthlive.morph.duration_us"
	AttrApplyMicros   = "gotthlive.apply.duration_us"
	AttrTimingSource  = "gotthlive.timing.source"
)

// Span names, in the tree order of one event.
//
// SpanRenderFragment was drawn in instrumentation §3.1 and declared nowhere,
// deliberately: "a constant nothing starts is one more thing that reads as
// implemented", and the section said whoever starts the span declares the
// constant in the same change. This is that change.
const (
	SpanEvent          = "gotthlive.event"
	SpanParse          = "gotthlive.parse"
	SpanAuthorize      = "gotthlive.authorize"
	SpanReduce         = "gotthlive.reduce"
	SpanRender         = "gotthlive.render"
	SpanRenderFragment = "gotthlive.render.fragment"
	SpanEncode         = "gotthlive.encode"
	SpanSend           = "gotthlive.send"
	SpanOrigin         = "gotthlive.origin"
	SpanClientMorph    = "gotthlive.client.morph"
	SpanEffect         = "gotthlive.effect."
)

// Tracer wraps a provider. A nil *Tracer is the disabled configuration and
// every method on it is correct and free.
//
// The provider is taken explicitly rather than read from the OpenTelemetry
// global. That is an architectural constraint and not a style preference: it
// is what lets this library depend on the trace and metric API submodules
// instead of the root module that carries the global.
type Tracer struct {
	t trace.Tracer
}

// NewTracer builds a tracer from a provider. A nil provider returns nil,
// which is the disabled configuration and is not an error.
func NewTracer(tp trace.TracerProvider) *Tracer {
	if tp == nil {
		return nil
	}
	return &Tracer{t: tp.Tracer("github.com/candacelabs/csf/pkg/gotth")}
}

// Enabled reports whether spans are being recorded.
//
// It is the single boolean instrumentation §4.2 requires a disabled
// configuration to branch on, and hot-path callers check it *before* building
// an attribute list rather than relying on the nil check inside Start. The
// difference is measurable and is not a style preference: the variadic attrs
// escape into the tracer, so a call site that builds them unconditionally
// heap-allocates the backing array on every event even when tracing is off.
func (t *Tracer) Enabled() bool { return t != nil }

// Start begins a span. On a nil tracer it returns the context unchanged and a
// span whose methods do nothing.
func (t *Tracer) Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, Span) {
	if t == nil {
		return ctx, Span{}
	}
	ctx, s := t.t.Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, Span{s: s}
}

// StartChildOf begins a span whose parent is a span that has already ended,
// named by the reference the caller carried across a goroutine boundary.
//
// This is FR-36 clause 4's mechanism. An ended span is still a valid parent —
// a parent-child edge asserts that the parent's work caused the child's, not
// that the parent's clock encloses it — so the reference the ingress already
// holds is enough to make the whole server-side path descend from one root.
// The consequence is the one clause 4 exists for: a sampler decides once, at
// that root, and every span below inherits the decision. Three roots meant
// three decisions, and under the documented default `ParentBased(TraceIDRatioBased(0.05))`
// that produced 0 of 300 interactions recording both `authorize` and `event`
// (L9-1's C-30 measurement).
//
// An invalid reference is not an error and is not faked: the span starts from
// whatever the context already carries, which for a server-initiated
// transition is nothing, and it becomes a root. That is truthful — no client
// frame authorized it — and it is why this does not silently invent an edge.
func (t *Tracer) StartChildOf(ctx context.Context, name string, parent SpanRef, attrs ...attribute.KeyValue) (context.Context, Span) {
	if t == nil {
		return ctx, Span{}
	}
	if sc := parent.SpanContext(); sc.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, sc)
	}
	ctx, s := t.t.Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, Span{s: s}
}

// StartLinked begins a span linked to a span that has already ended. It is how
// a client's morph timing rejoins the trace that produced the patch: the
// timing arrives after the encode span closed, and the span's start timestamp
// is derived rather than observed (instrumentation §3.3), so a parent edge
// would assert an enclosure this design does not measure.
//
// It is the last link site under FR-36 clause 3. The other one — the
// transition linking back to authorization — became a real parent edge under
// clause 4, which clause 3 says is always permitted.
func (t *Tracer) StartLinked(ctx context.Context, name string, to SpanRef, attrs ...attribute.KeyValue) (context.Context, Span) {
	if t == nil {
		return ctx, Span{}
	}
	opts := []trace.SpanStartOption{trace.WithAttributes(attrs...)}
	if sc := to.SpanContext(); sc.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: sc}))
	}
	ctx, s := t.t.Start(ctx, name, opts...)
	return ctx, Span{s: s}
}

// Span is a nil-safe wrapper over an OpenTelemetry span.
type Span struct {
	s trace.Span
}

// End closes the span.
func (s Span) End() {
	if s.s != nil {
		s.s.End()
	}
}

// SetAttributes adds attributes to the span.
func (s Span) SetAttributes(attrs ...attribute.KeyValue) {
	if s.s != nil {
		s.s.SetAttributes(attrs...)
	}
}

// RecordError marks the span as failed.
func (s Span) RecordError(err error) {
	if s.s != nil && err != nil {
		s.s.RecordError(err)
	}
}

// Ref returns a compact reference to this span, for storing in the outbound
// window until the client reports back.
func (s Span) Ref() SpanRef {
	if s.s == nil {
		return SpanRef{}
	}
	sc := s.s.SpanContext()
	return SpanRef{
		TraceID: sc.TraceID(),
		SpanID:  sc.SpanID(),
		Flags:   byte(sc.TraceFlags()),
	}
}

// SpanRef is a 32-byte reference to a span, held per slot in the outbound
// window so a client's morph timing can be linked back to the span that
// encoded the patch.
//
// It is deliberately not a trace.SpanContext. A real SpanContext is 56 to 64
// bytes on amd64 once the trace flags, the remote bit and a TraceState's slice
// header are counted, and this library's memory budget is made of decisions
// this size. Reconstructing the context at link time is exact for this use,
// because the library never populates a TraceState.
type SpanRef struct {
	// TraceID is the W3C trace identifier, raw rather than a trace.TraceID so
	// this struct's size is a fact about this file.
	TraceID [16]byte

	// SpanID identifies the span within that trace.
	SpanID [8]byte

	// Flags is the W3C trace-flags byte, which carries the sampled bit. It is
	// kept because a reconstructed context that lost it would relocate the
	// sampling decision to link time.
	Flags byte

	_ [7]byte // pad to 32 bytes, which is the figure the budget carries
}

// SpanContext reconstructs the reference for linking.
func (r SpanRef) SpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    r.TraceID,
		SpanID:     r.SpanID,
		TraceFlags: trace.TraceFlags(r.Flags),
		Remote:     false,
	})
}
