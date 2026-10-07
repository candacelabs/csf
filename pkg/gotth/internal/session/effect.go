package session

import "context"

// Effect is one unit of I/O the actor performs at its boundary.
//
// It mirrors the public live.Effect, which is a concrete struct by operator
// ruling of 2026-09-03, and it is a second declaration rather than an alias
// because Run is in THIS package's vocabulary: the public signature speaks of a
// live.Session and a live.Emitter, neither of which an internal package can
// name. live's own adapter re-expresses one as the other, once, and that is the
// only translation between the two.
type Effect[I IIdentity] struct {
	// Source names the effect for provenance and metrics. It is refined before
	// the effect runs, because it becomes half of an Origin.source.
	Source string

	// Run performs the effect, on the goroutine spawn starts for it.
	//
	// scheduledBy is the identifier of the event whose transition returned this
	// effect, or zero when the server started the transition itself. It is
	// handed over rather than re-derived because FR-58 requires every
	// library-produced error to name the causal identifier where one exists,
	// and the errors the public adapter raises against an emitted event are
	// raised before any identifier of their own is minted.
	//
	// A nil Run is a mistake rather than a no-op, and execute refuses it with a
	// failure event: an effect that never runs is a change that never happens.
	Run func(ctx context.Context, p Peer[I], scheduledBy uint64, emit Emit) error
}

// EffectFailedEvent is the name of the event a failed effect is turned into,
// so that a reducer sees a deterministic failure rather than silence.
//
// It is spelled again in package live, which is where an application can reach
// it; live's own suite asserts the two are equal, because a reducer that
// matches on the wrong string handles nothing and looks like it handles
// something.
const EffectFailedEvent = "gotth.effect_failed"

// The fields an EffectFailedEvent carries: which effect failed, what it said,
// and whether the failure was classified as transient.
const (
	EffectFailedSourceField    = "source"
	EffectFailedErrorField     = "error"
	EffectFailedRetryableField = "retryable"
)
