package obs

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

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
