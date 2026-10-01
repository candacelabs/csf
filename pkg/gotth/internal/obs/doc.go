// Package obs is the library's instrumentation: metrics, traces and the
// provenance log.
//
// It exists because every one of this library's degradations — a coalesce, a
// drop, a suppression, a rejection, an eviction — has to be observable, and
// the alternative to one package owning that is the catalogue living in
// whichever package last needed a counter. In all four of the systems this
// design was written against, the dominant production failure is a
// degradation with no signal; a degradation that is not observable is a defect
// here, not a tuning opportunity.
//
// # Nothing is computed when it is disabled
//
// Metrics, traces and logs are each disabled by leaving their provider nil, and
// a disabled configuration pays one predictable branch. The mechanism is a nil
// pointer with methods, not a no-op interface implementation: an interface
// would put an indirect call on the hot path and make the overhead budget a
// matter of the inliner's mood. Every method here begins by testing its
// receiver against nil, and calling any of them on a nil receiver is correct
// and free.
//
// # Cardinality
//
// No causal identifier is ever a metric label — not the session, the event,
// the patch or the transition. Those live in traces and in the provenance log,
// which carry the whole chain without multiplying a time series per
// connection. Event and fragment label values are bounded by registration.
// The origin source is not, because nothing registers an effect, so it is
// bounded at the metric: sixty-four distinct values, then a collapse to
// "other" with an overflow counter that says the collapse happened.
//
// # The provenance log is not the metrics path
//
// Provenance records are emitted by the session actor from the same values
// that construct the frame; the counters they are audited against are
// incremented by the framer and the transport. Different code, different sink.
// Coupling them would make the audit check a value against itself, which is
// the failure the audit exists to catch.
package obs
