package session

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"

	"go.opentelemetry.io/otel/attribute"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// ErrSessionSaturated is the sentinel behind the error returned to an effect
// whose emitted event could not be accepted, so that the effect learns about
// backpressure instead of the event vanishing.
//
// It is never returned bare, and that is FR-58's doing: an error handed to
// application code has to name the session it belongs to and the event that
// caused the work, and a package-level sentinel can name neither. [Actor.emitter]
// wraps it with both; this value is what survives the wrapping for errors.Is,
// and it carries the half of the message that is the same every time — why the
// event was dropped, and what to do about it.
var ErrSessionSaturated = errors.New(
	"the session mailbox is full: back off and emit again, or raise Config.Limits.MailboxDepth")

// ErrSessionClosing is the sentinel behind the error returned to an effect that
// emits after the session has begun shutting down. It is wrapped exactly as
// ErrSessionSaturated is, and for the same reason.
var ErrSessionClosing = errors.New(
	"the session is closing and will accept no further events: return from the effect rather than retrying, " +
		"because nothing this session emits from here reaches a reducer")

// emissionRefused composes the error an effect is handed when its emitted event
// was not accepted.
//
// It exists so that the two refusals are one sentence with one shape, and so
// that FR-58's three clauses are satisfied in one place rather than twice: the
// session, the causal edge the emission descends from, and — carried by the
// sentinel — what the effect should do next.
func (a *Actor[I]) emissionRefused(source string, scheduledBy uint64, why error) error {
	return fmt.Errorf("gotth-live: session %s: the event emitted by effect %q (%s) was dropped: %w",
		a.idStr, source, causalClause(scheduledBy), why)
}

// causalClause names the event an effect descends from, in the words a sentence
// uses rather than the words a log field uses.
//
// The log stream states the zero — effects.go's own record emits scheduled_by
// unconditionally, because a field that appears only sometimes cannot be
// queried for. A message read by a person is the other case: "scheduled by
// event 0" invites the reader to go looking for event 0, and there is no such
// event.
func causalClause(scheduledBy uint64) string {
	if scheduledBy == 0 {
		return "scheduled by the server itself, so no event caused it"
	}
	return "scheduled by event " + strconv.FormatUint(scheduledBy, 10)
}

// spawn starts the goroutine that runs one effect.
//
// It is not the only place this library starts a goroutine. The sentence that
// stood here — "every goroutine in this library is started here" — was false of
// the tree on the day it was written, and it is L9-1's C-49; RFC §3.4 carries
// the same correction on the document side, with the table this comment is the
// source half of.
//
// What spawn does own is every effect: Go has no supervision tree, so a panic
// anywhere kills the process unless something recovers it, and this installs
// the recover, the Goroutines gauge and the a.effects registration that
// shutdown drains under EffectDrainTimeout in one place. That is what makes an
// effect started with a bare go statement a defect a reviewer can look for —
// which is the true version of the claim, and narrower than the old one by
// exactly the four goroutines below.
//
// The four, so that "look for a bare go statement" has a set to compare
// against. Each satisfies what review checklist §6.4 actually asks — a named
// owner, a stop condition, and a place that waits:
//
//   - wsx/handler.go, the session goroutine, started once register has
//     succeeded. Waited for by IApp.Close through the conn's done channel, which
//     serve closes after deregistering (C-34).
//   - wsx/conn.go, the actor's Run. Joined by serve's own actorDone before that
//     done channel closes.
//   - wsx/handler.go, Close's per-session close fan-out. Concurrent on purpose,
//     so one unresponsive peer cannot serialize the drain; joined in the same
//     function.
//   - actor.go's waitFor helper, which blocks on a WaitGroup and closes a
//     channel. Deliberately NOT waited for, and that is what makes the drain's
//     deadline enforceable: if an effect never returns, this goroutine stays
//     blocked and the abandonment is counted (EffectAbandoned) rather than
//     hidden.
//
// Two more sit outside the runtime library, named here so that a grep which
// finds them does not make this comment a liar in the same way its predecessor
// was: gotth-live-dev reaps its child process on one, and livetest's Client
// reads its socket on one (joined by Close, and by the tb.Cleanup NewClient
// registers).
//
// Routing the four through spawn would make the original claim true and is
// wrong rather than merely large, which is why this is a comment change.
// spawn registers into a.effects and shutdown waits on it: the session and
// actor goroutines would be waiting for themselves to finish, and waitFor's
// helper — whose whole job is to bound that wait — would be waiting for the
// wait it bounds. Three of the four are also in wsx, which would have to hold
// an Actor to reach this method at all.
func (a *Actor[I]) spawn(ctx context.Context, site string, fn func(ctx context.Context)) {
	a.effects.Add(1)
	a.m.Goroutines(ctx, 1)
	go func() {
		defer a.effects.Done()
		defer a.m.Goroutines(ctx, -1)
		defer func() {
			if r := recover(); r != nil {
				a.m.Panic(ctx, site)
				a.log.Error(ctx, "gotth-live: a library goroutine panicked and was contained to this session",
					obs.Str("session_id", a.idStr),
					obs.Str("site", site),
					obs.Str("panic", sprint(r)),
					obs.Str("stack", string(debug.Stack())))
			}
		}()
		fn(ctx)
	}()
}

// runEffects hands a transition's effects to the actor boundary. They are
// never executed inside a reducer, and the values they were declared as are
// what a test asserts on.
//
// scheduledBy is the identifier of the event whose transition returned these
// effects, or zero when the server started the transition itself. It is
// carried all the way to the patch an effect's emission produces, because a
// patch that names only the effect leaves an operator able to reach
// "effect:counter.watch" and unable to reach the click that scheduled it —
// which is the causal edge the whole provenance story is about.
func (a *Actor[I]) runEffects(ctx context.Context, effects []Effect[I], scheduledBy uint64) {
	for _, e := range effects {
		// The zero Effect is inert, which is the concrete struct's answer to
		// the nil element the old interface slice could hold: a conditional
		// effect that did not apply costs nothing. An Effect that named itself
		// and forgot its behaviour is NOT this case and is refused below.
		if e.Source == "" && e.Run == nil {
			continue
		}
		a.execute(ctx, e, scheduledBy)
	}
}

// effectSourceRefused is what an effect whose Source cannot be namespaced is
// told, as the error text on its own failure event.
//
// The budget is derived rather than typed: it is the schema's bound on
// Origin.source less the prefix this library prepends, and a message that
// states a number the code does not compute is the kind of number that is
// wrong one edit later.
var effectSourceRefused = fmt.Sprintf(
	"gotth-live: Effect.Source is not usable as an origin source: it must be at most %d bytes "+
		"and match ^[a-z][a-z0-9_.:/-]*$, because the library namespaces it as %q + source onto "+
		"the origin of every patch the effect causes",
	protocol.MaxOriginSource-len(protocol.SourceEffectPrefix), protocol.SourceEffectPrefix)

// effectRunMissing is what an effect that named itself and carried no behaviour
// is told, as the error text on its own failure event.
//
// It is a failure rather than a silent success for the reason this package
// keeps restating: an effect that never runs is a change that never happens,
// and a reducer that learns nothing about it is a session that quietly stops
// updating. The zero Effect is the deliberate no-op and never reaches here.
const effectRunMissing = "gotth-live: Effect.Run is nil: an effect that names itself and carries no " +
	"behaviour is a change that never happens — set Run, or return no effect at all"

func (a *Actor[I]) execute(ctx context.Context, e Effect[I], scheduledBy uint64) {
	source := e.Source

	// BR-2. The source is application input with no registration step
	// (protocol.md §3.3), and it is one half of an Origin.source that
	// ValidateOutbound holds to a length bound and a charset. Refusing it here
	// is D-18's shape: the effect fails deterministically, in a failure event
	// the reducer handles, rather than the patch it would have caused being
	// dropped three layers later as an INTERNAL error nobody can act on.
	//
	// Checked per call rather than cached against the sources seen so far. The
	// check is a length compare and a scan of at most 64 bytes, which is
	// cheaper than the map lookup a cache would cost, and a cache keyed on an
	// application-supplied string is unbounded state on the transition path.
	if !protocol.ValidOriginSource(protocol.SourceEffectPrefix + source) {
		// Counted as "error" rather than under a label of its own: it is the
		// same outcome the application sees — the effect did not run and a
		// failure event says so — and instrumentation.md fixes this
		// instrument's result domain at ok, error, cancelled and panicked. The
		// source label is the stand-in rather than the offending string, so a
		// malformed source cannot mint a metric label either. The record below
		// is what distinguishes this from an effect that ran and failed.
		a.m.Effect(ctx, protocol.SourceEffectInvalid, "error")
		a.log.Error(ctx, "gotth-live: refused an effect whose source cannot name an origin: no patch it caused could have been sent",
			obs.Str("session_id", a.idStr),
			obs.Str("effect_source", source),
			obs.Int("effect_source_bytes", len(source)),
			obs.U64("scheduled_by", scheduledBy))
		a.emitFailure(source, effectSourceRefused, false, scheduledBy)
		return
	}

	// The other half of the concrete struct's two ways of being unusable, and
	// it is refused on exactly the terms above: deterministically, before a
	// goroutine is spawned for it, in a failure event the reducer handles.
	// Counted as an error under the effect's own source, because unlike a
	// malformed source this one is a name an operator can grep for.
	if e.Run == nil {
		a.m.Effect(ctx, source, "error")
		a.log.Error(ctx, "gotth-live: refused an effect with no behaviour: an effect that never runs is a change that never happens",
			obs.Str("session_id", a.idStr),
			obs.Str("effect_source", source),
			obs.U64("scheduled_by", scheduledBy))
		a.emitFailure(source, effectRunMissing, false, scheduledBy)
		return
	}

	a.spawn(ctx, "effect", func(ctx context.Context) {
		var span obs.Span
		if a.tr.Enabled() {
			ctx, span = a.tr.Start(ctx, obs.SpanEffect+source,
				a.idAttr,
				attribute.String(obs.AttrOriginSource, source),
				attribute.Int64(obs.AttrEventID, int64(scheduledBy)))
		}
		defer span.End()

		result := a.runOne(ctx, e, source, scheduledBy)
		a.m.Effect(ctx, source, result)
	})
}

// runOne performs one effect under the guard that turns any failure into a
// deterministic event the reducer can handle, rather than into silence.
func (a *Actor[I]) runOne(ctx context.Context, e Effect[I], source string, scheduledBy uint64) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = "panicked"
			// scheduled_by is FR-58's "causal ID where one exists", and this
			// site holds it as a parameter. Without it the record names the
			// effect and stops there, so an operator reads
			// "effect:counter.watch panicked" and cannot reach the click that
			// scheduled the watch — the edge runEffects' own comment argues
			// for, present on the resulting patch and absent from the log an
			// operator reads first. Emitted unconditionally, including the
			// zero the server's own transitions carry, because a field that
			// appears only sometimes cannot be queried for and zero already
			// means "no event caused this" everywhere else in this stream.
			a.log.Error(ctx, "gotth-live: an effect panicked: the reducer sees a failure event instead of silence",
				obs.Str("session_id", a.idStr),
				obs.Str("effect_source", source),
				obs.U64("scheduled_by", scheduledBy),
				obs.Str("site", "effect"),
				obs.Str("panic", sprint(r)),
				obs.Str("stack", string(debug.Stack())))
			a.m.Panic(ctx, "effect")
			// Terminal, and not merely by default. A panic is a bug in the
			// effect, and re-running the same bug is the definition of not
			// making progress; the panic budget would then close the session
			// on a loop the library scheduled for itself.
			a.emitFailure(source, sprint(r), false, scheduledBy)
		}
	}()

	// Called directly rather than through IApp. The effect carries its own
	// behaviour now, so the seam that used to type-assert one back out of an
	// interface has nothing left to do: the application's function is a field
	// on the value the reducer returned, already re-expressed in this package's
	// vocabulary by live's single adapter.
	err := e.Run(ctx, a.peer, scheduledBy, a.emitter(source, scheduledBy))
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// The session context was cancelled. An effect that already committed
		// externally stays committed even though its patch never reached the
		// client: that is the one place at-most-once delivery leaks, and it is
		// an application-visible contract rather than a bug.
		return "cancelled"
	default:
		a.emitFailure(source, err.Error(), IsRetryable(err), scheduledBy)
		return "error"
	}
}

// emitter returns the injection function handed to an effect.
func (a *Actor[I]) emitter(source string, scheduledBy uint64) Emit {
	return func(ev Event) error {
		if a.closing.Load() {
			return a.emissionRefused(source, scheduledBy, ErrSessionClosing)
		}
		// A server-initiated transition carries no client identifiers of its
		// own — the origin source names its cause, which is what H-6 binds —
		// but it does carry the event that scheduled it, as a contributing
		// edge. Those are different claims: "this patch was caused by event N"
		// and "event N is among the events whose state changes this patch
		// carries". The second is the true one here.
		ev.ID = 0
		ev.ClientRef = 0
		ev.At = a.now()

		m := getInbound()
		m.kind = msgEffectResult
		m.ev = ev
		// Two edges, from two parties that each know only their half. The
		// library knows which event scheduled this effect; the application
		// knows which event produced the value the effect is delivering, which
		// on a fan-out through shared state is a different event and is
		// unknowable from here. Both are contributing edges, neither is the
		// cause, and the union is deduplicated at emit time.
		m.origin = protocol.Origin{
			Kind:         pb.OriginKind_EFFECT,
			Source:       effectOrigin(source),
			Contributing: append(scheduledEdge(scheduledBy), ev.Contributing...),
		}
		if !a.post(m) {
			return a.emissionRefused(source, scheduledBy, ErrSessionSaturated)
		}
		return nil
	}
}

// effectOrigin is the Origin.source an effect's emission carries.
//
// It is total by construction: a source that cannot be namespaced collapses to
// the stand-in rather than composing a value the outbound boundary would
// refuse. execute already refuses such an effect before it runs, so the only
// value this arm carries in practice is the failure event that refusal emits —
// but the two together are what make "no application string reaches
// ValidateOutbound and fails it" a property of the code rather than of one
// call site remembering to check.
func effectOrigin(source string) string {
	if s := protocol.SourceEffectPrefix + source; protocol.ValidOriginSource(s) {
		return s
	}
	return protocol.SourceEffectInvalid
}

// scheduledEdge is the contributing list for an effect's emission: the event
// that scheduled it, or nothing when the server scheduled itself.
func scheduledEdge(scheduledBy uint64) []uint64 {
	if scheduledBy == 0 {
		return nil
	}
	return []uint64{scheduledBy}
}

// emitFailure turns a failed effect into an ordinary event, so the application
// handles the failure in the reducer where it is replayable, rather than
// discovering it in a log.
//
// The classification rides as a field rather than as a second event name. A
// reducer that does not care matches one name; a reducer that does reads one
// field, and one that reads it wrongly reads a value that is not "true", which
// is the terminal answer and the safe one.
func (a *Actor[I]) emitFailure(source, detail string, retryable bool, scheduledBy uint64) {
	if a.closing.Load() {
		return
	}
	m := getInbound()
	m.kind = msgEffectResult
	m.ev = Event{
		Name: EffectFailedEvent,
		At:   a.now(),
		Fields: []Field{
			{Key: EffectFailedSourceField, Value: source},
			{Key: EffectFailedErrorField, Value: detail},
			{Key: EffectFailedRetryableField, Value: strconv.FormatBool(retryable)},
		},
	}
	m.origin = protocol.Origin{
		Kind:         pb.OriginKind_EFFECT,
		Source:       effectOrigin(source),
		Contributing: scheduledEdge(scheduledBy),
	}
	a.post(m)
}
