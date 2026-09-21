// Package observability is the compiled source for
// docs/guide/observability.md.
//
// Three fields on Config turn on the whole instrumentation surface. The
// providers themselves are the application's: this library never constructs a
// logger, never sets a global, and never reads the OpenTelemetry global.
package observability

import (
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Instrument fills in the three observability fields and nothing else.
//
//   - Logger enables the library's structured logs AND the provenance log,
//     one record per transition on the logger name "gotthlive.provenance".
//     Nil disables both, and the reverse lookup from a captured patch back to
//     its cause goes with them. The frames still carry the causal chain either
//     way; what is lost is the server-side index.
//   - Metrics takes a metric.MeterProvider from go.opentelemetry.io/otel/metric
//     and enables every gotthlive_* metric.
//   - Tracer takes a trace.TracerProvider from go.opentelemetry.io/otel/trace
//     and enables every gotthlive.* span. The provider is passed explicitly
//     rather than read from the OTel global, which is what lets this library
//     depend on the API submodules and not on the OTel root.
//
// A nil provider is legal for all three and costs one predictable branch per
// call site.
func Instrument[S any, I live.IIdentity](cfg live.Config[S, I], logger *slog.Logger, mp metric.MeterProvider, tp trace.TracerProvider) live.Config[S, I] {
	cfg.Logger = logger
	cfg.Metrics = mp
	cfg.Tracer = tp
	return cfg
}

// ProvenanceLogger is a logger shaped for the provenance stream: JSON, at Info,
// on stdout.
//
// The provenance log is a structured log stream and not a library-owned store.
// One record per transition, carrying session_id, event_id, client_ref,
// transition_id, state_version, patch_id, server_seq, origin_kind,
// origin_source and fragment_ids — about 200 bytes serialized. The operator's
// existing log pipeline is the storage and the query engine.
//
// A transition that emitted no patch still gets a record, with patch_id 0.
// Without it the transitions that produced nothing would be invisible, and
// "the state version rises exactly when state changed" would be unverifiable.
func ProvenanceLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
