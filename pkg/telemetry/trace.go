package telemetry

import (
	"context"
	"errors"
	"fmt"

	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// TraceFlagsSampled is the W3C sampled flag.
const TraceFlagsSampled uint32 = uint32(oteltrace.FlagsSampled)

var (
	// ErrNilContext indicates that a propagation helper received a nil context.
	ErrNilContext = errors.New("telemetry: nil context")
	// ErrTraceContextNotFound indicates that a child span was requested without
	// a parent TraceContext in the context.Context.
	ErrTraceContextNotFound = errors.New("telemetry: trace context not found")
	tracer                  = sdktrace.NewTracerProvider().Tracer("github.com/candacelabs/csf/pkg/telemetry")
)

// NewTraceContext starts a trace with cryptographically random OpenTelemetry
// trace and span identifiers. traceFlags must fit in the W3C one-byte field.
func NewTraceContext(traceFlags uint32) (*telemetryv1.TraceContext, error) {
	if traceFlags > 0xff {
		return nil, fmt.Errorf("%w: trace_flags %d exceeds 255", ErrInvalidTraceContext, traceFlags)
	}
	_, span := tracer.Start(context.Background(), "candace.trace")
	spanContext := span.SpanContext()
	span.End()
	return protoFromSpanContext(oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    spanContext.TraceID(),
		SpanID:     spanContext.SpanID(),
		TraceFlags: oteltrace.TraceFlags(traceFlags),
	})), nil
}

// ContextWithTrace stores trace as an OpenTelemetry SpanContext in ctx.
func ContextWithTrace(ctx context.Context, trace *telemetryv1.TraceContext) (context.Context, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	spanContext, err := spanContextFromProto(trace)
	if err != nil {
		return nil, err
	}
	return oteltrace.ContextWithSpanContext(ctx, spanContext), nil
}

// TraceFromContext converts the OpenTelemetry SpanContext in ctx to protobuf.
func TraceFromContext(ctx context.Context) (*telemetryv1.TraceContext, bool) {
	if ctx == nil {
		return nil, false
	}
	spanContext := oteltrace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return nil, false
	}
	return protoFromSpanContext(spanContext), true
}

// ContextWithChildSpan derives a child of the span in ctx and returns a new
// context carrying it.
func ContextWithChildSpan(ctx context.Context) (context.Context, *telemetryv1.TraceContext, error) {
	if ctx == nil {
		return nil, nil, ErrNilContext
	}
	parent := oteltrace.SpanContextFromContext(ctx)
	if !parent.IsValid() {
		return nil, nil, ErrTraceContextNotFound
	}
	childContext, span := tracer.Start(ctx, "candace.child")
	child := span.SpanContext()
	span.End()
	return childContext, protoFromSpanContext(child), nil
}

func spanContextFromProto(trace *telemetryv1.TraceContext) (oteltrace.SpanContext, error) {
	if trace == nil {
		return oteltrace.SpanContext{}, fmt.Errorf("%w: nil TraceContext", ErrInvalidTraceContext)
	}
	if err := telemetryv1.ValidateTraceContext(trace); err != nil {
		return oteltrace.SpanContext{}, fmt.Errorf("%w: %w", ErrInvalidTraceContext, err)
	}
	traceID, err := oteltrace.TraceIDFromHex(trace.GetTraceId())
	if err != nil {
		return oteltrace.SpanContext{}, fmt.Errorf("%w: trace_id: %v", ErrInvalidTraceContext, err)
	}
	spanID, err := oteltrace.SpanIDFromHex(trace.GetSpanId())
	if err != nil {
		return oteltrace.SpanContext{}, fmt.Errorf("%w: span_id: %v", ErrInvalidTraceContext, err)
	}
	spanContext := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.TraceFlags(trace.GetTraceFlags()),
	})
	if !spanContext.IsValid() {
		return oteltrace.SpanContext{}, fmt.Errorf("%w: invalid OpenTelemetry SpanContext", ErrInvalidTraceContext)
	}
	return spanContext, nil
}

func protoFromSpanContext(spanContext oteltrace.SpanContext) *telemetryv1.TraceContext {
	return &telemetryv1.TraceContext{
		TraceId:    spanContext.TraceID().String(),
		SpanId:     spanContext.SpanID().String(),
		TraceFlags: uint32(spanContext.TraceFlags()),
	}
}
