package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/render"
)

// Options configure one session actor.
type Options[I IIdentity] struct {
	// Peer is the identity and session identifier this actor is bound to for
	// its whole life. Neither changes: a re-authentication is a new session.
	Peer Peer[I]

	// App is the application behaviour — mount, reduce, render, execute — as
	// the type-erased interface this package can hold without knowing the
	// state type.
	App IApp[I]

	// Limits are the resource bounds. Zero fields are filled by Normalize, so
	// a caller may set one and leave the rest.
	Limits Limits

	// Framer is the only way out to the socket: it validates and marshals, so
	// there is no path from this actor to the transport that skips the
	// outbound boundary.
	Framer *protocol.Framer

	// Close ends the connection with an enumerated code. It is the transport's
	// close, called from whichever goroutine notices, and it is idempotent.
	Close func(code protocol.CloseCode, reason string)

	// Metrics, Tracer and Logger are the instrumentation triple, and each may
	// be nil, which is the disabled configuration rather than a missing
	// dependency: every method on all three is nil-receiver safe.
	Metrics *obs.Metrics

	// Tracer starts the session and transition spans. Nil disables tracing.
	Tracer *obs.Tracer

	// Logger writes the event, patch and provenance records. Nil disables
	// logging, including the provenance stream.
	Logger *obs.Logger

	// Dev is developer mode (FR-23). Its whole effect is on the message of the
	// Error frame a contained panic produces: see Actor.devMessage. It must be
	// false in production.
	Dev bool

	// Now and Ticks are the actor's only sources of time. They are injected
	// so that a test can drive a thirty-minute idle timeout without waiting
	// thirty minutes, and so that nothing on the transition path reads a clock
	// the test cannot see.
	Now func() time.Time

	// Ticks drives the heartbeat, the idle timeout and the slow-client grace.
	// A test supplies its own channel and delivers a tick when it wants one;
	// nothing here calls time.NewTicker.
	Ticks <-chan time.Time
}

// Actor implements the service owning one connection's widget state and effects.
// Its Run method processes events on one goroutine, serializing reducers and
// renderers. Its parent starts Run and waits for it at cleanup. Shared mutable
// objects accessed elsewhere still require synchronization or ownership transfer.
//
// It selects over three typed, bounded inputs: a mailbox of events, effect
// results and synthesized backpressure signals; a channel of client
// acknowledgements; and a heartbeat tick. Only the mailbox can reach a
// reducer, and exactly one function writes to it from the wire, which is what
// makes the per-event authorization hook impossible to route around.
type Actor[I IIdentity] struct {
	peer   Peer[I]
	app    IApp[I]
	lim    Limits
	fr     *protocol.Framer
	closer func(code protocol.CloseCode, reason string)
	m      *obs.Metrics
	tr     *obs.Tracer
	log    *obs.Logger
	now    func() time.Time
	dev    bool

	// idStr and idAttr are the session identifier in the two shapes
	// observability wants it, rendered ONCE.
	//
	// Peer.ID.String() hex-encodes sixteen bytes into a fresh thirty-two byte
	// string, and this session's identifier appears on every span this actor
	// opens, every log record it writes and every provenance row it emits — a
	// mount alone reaches it about ten times. It does not change for the life
	// of the session, so rendering it per call was allocation the G2 baseline's
	// GOGC line paid for twice (docs/bench/g2-baseline.md §5.3). Nothing about
	// what is emitted changes: it is the same string and the same attribute.
	idStr  string
	idAttr attribute.KeyValue

	mailbox chan *inbound
	acks    chan uint64
	ticks   <-chan time.Time
	stopped chan struct{}

	// stateComparable is the application's answer to "may == decide whether
	// this transition changed anything", asked once when the actor is built
	// rather than on every transition. See IApp.StateComparable.
	stateComparable bool

	// Actor-goroutine state. Nothing below this line is read or written from
	// any other goroutine, which is why none of it is guarded.
	state            any
	view             *render.Renderer
	win              *window
	serverSeq        uint64
	patchID          uint64
	transitionID     uint64
	stateVersion     uint64
	hbNonce          uint64
	panics           map[string]int
	pendingOrig      *protocol.Origin
	pendingIDs       []uint64
	slowNotified     bool
	coalesceNotified bool
	coalesceHeld     bool
	resyncBucket     *bucket
	resyncDenied     int

	// lastSnapshotSeq is the sequence of the most recent snapshot this session
	// put on the wire. It is the floor on the next supersession range's lower
	// bound: everything at or below it has either been delivered or been
	// replaced by that snapshot, so a later range reaching back below it would
	// supersede a range already superseded (P7, BR-9).
	lastSnapshotSeq uint64

	// Read-pump state. Owned by the single goroutine that calls Ingress.
	eventSeq     uint64
	eventBucket  *bucket
	eventDenied  int
	ingressReady chan struct{}

	// Shared across goroutines, and each one says why it is allowed to be.
	lastInboundNS atomic.Int64 // liveness: written by the read pump, read by the actor
	lastEventNS   atomic.Int64 // idle eviction: same
	closing       atomic.Bool
	closeOnce     sync.Once
	effects       sync.WaitGroup
	cancelEffects context.CancelFunc
}

// New builds an actor. It allocates the mailbox, the acknowledgement channel
// and the window, which is the moment a session's per-connection memory comes
// into existence — after authentication, never before.
func New[I IIdentity](o Options[I]) *Actor[I] {
	o.Limits = o.Limits.Normalize()
	if o.Now == nil {
		o.Now = time.Now
	}
	now := o.Now()

	a := &Actor[I]{
		peer:         o.Peer,
		app:          o.App,
		lim:          o.Limits,
		fr:           o.Framer,
		closer:       o.Close,
		m:            o.Metrics,
		tr:           o.Tracer,
		log:          o.Logger,
		now:          o.Now,
		dev:          o.Dev,
		mailbox:      make(chan *inbound, o.Limits.MailboxDepth),
		acks:         make(chan uint64, o.Limits.AckChannelDepth),
		ticks:        o.Ticks,
		stopped:      make(chan struct{}),
		ingressReady: make(chan struct{}),
		win:          newWindow(o.Limits.AckWindow),
		panics:       make(map[string]int, 3),
		eventBucket:  newBucket(o.Limits.MaxEventsPerSecond, o.Limits.EventBurst, now),
		resyncBucket: newBucket(1/o.Limits.MinResyncInterval.Seconds(), o.Limits.ResyncBurst, now),
	}
	a.idStr = o.Peer.ID.String()
	a.idAttr = attribute.String(obs.AttrSessionID, a.idStr)
	a.stateComparable = o.App.StateComparable()
	a.view = o.App.Registry().NewRenderer()
	a.observeFragments()
	a.lastInboundNS.Store(now.UnixNano())
	a.lastEventNS.Store(now.UnixNano())
	return a
}

// ID returns the session's identifier.
func (a *Actor[I]) ID() ID { return a.peer.ID }

// TrackedBytes is the exactly-sized cost of the structures this actor owns:
// the window, the two channel backing arrays, and the fragment hashes. It is
// what the per-session memory gauge reports, and it deliberately does not
// pretend to know the heap cost of application state, because Go has no
// per-goroutine heap attribution.
func (a *Actor[I]) TrackedBytes() int64 {
	const pointerSize, uint64Size = 8, 8
	return a.win.trackedBytes() +
		int64(a.lim.MailboxDepth)*pointerSize +
		int64(a.lim.AckChannelDepth)*uint64Size +
		int64(a.app.Registry().Len())*uint64Size
}

// Run drives the session until its context is cancelled or the session closes.
// It returns when the actor goroutine is finished, having drained or abandoned
// in-flight effects and run the teardown hook exactly once.
func (a *Actor[I]) Run(ctx context.Context) {
	effCtx, cancel := context.WithCancel(ctx)
	a.cancelEffects = cancel
	defer a.shutdown(ctx, cancel)

	a.mount(effCtx)
	close(a.ingressReady)

	for {
		select {
		case <-ctx.Done():
			return
		case m := <-a.mailbox:
			a.m.MailboxDepth(effCtx, len(a.mailbox))
			a.step(effCtx, m)
		case seq := <-a.acks:
			a.onAck(effCtx, seq)
		case t := <-a.ticks:
			a.onTick(effCtx, t)
		}
	}
}

// Ready blocks until the mount transition has emitted its snapshot. The read
// pump waits on it so that a client cannot have a frame accepted before the
// snapshot that establishes the sequence it must reference.
func (a *Actor[I]) Ready(ctx context.Context) error {
	select {
	case <-a.ingressReady:
		return nil
	case <-a.stopped:
		// FR-58: the session is named, there is no causal identifier because
		// nothing this session did has one yet, and the next step is the point
		// — this is not a frame to retry, it is a mount that failed, and the
		// record that says why was written by mount itself.
		return fmt.Errorf("gotth-live: session %s closed before it was established: "+
			"the mount transition did not produce a snapshot, so read this session's "+
			"earlier Error record for the mount failure rather than retrying here", a.idStr)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// shutdown is the ordered teardown: stop accepting, cancel in-flight effects,
// give them a bounded window to return, run the application's teardown hook,
// and deregister exactly once.
func (a *Actor[I]) shutdown(ctx context.Context, cancel context.CancelFunc) {
	a.closing.Store(true)
	cancel()

	if !waitFor(&a.effects, a.lim.EffectDrainTimeout) {
		// An effect that will not return does not get to hold the connection
		// open. It is counted, because an abandoned effect is a degradation
		// and a degradation without a signal is a defect.
		a.m.EffectAbandoned(ctx)
		a.log.Warn(ctx, "gotth-live: abandoned an effect that outlived the drain window",
			obs.Str("session_id", a.idStr),
			obs.Dur("drain_timeout_ms", a.lim.EffectDrainTimeout))
	}

	a.app.Teardown(context.WithoutCancel(ctx), a.peer, a.state)
	a.closeOnce.Do(func() { close(a.stopped) })
}

// waitFor waits for wg with a deadline, reporting whether it finished in time.
func waitFor(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// Done reports a channel closed when the actor has finished.
func (a *Actor[I]) Done() <-chan struct{} { return a.stopped }

// Close ends the session with an enumerated code. It is safe to call from any
// goroutine and is idempotent.
func (a *Actor[I]) Close(code protocol.CloseCode, reason string) {
	if a.closing.Swap(true) {
		return
	}
	if a.closer != nil {
		a.closer(code, reason)
	}
}

// mount runs the application's mount hook as the session's first transition
// and emits the snapshot that establishes the sequence.
func (a *Actor[I]) mount(ctx context.Context) {
	var span obs.Span
	if a.tr.Enabled() {
		ctx, span = a.tr.Start(ctx, obs.SpanOrigin,
			a.idAttr,
			attribute.String(obs.AttrOriginKind, pb.OriginKind_MOUNT.String()),
			attribute.String(obs.AttrOriginSource, protocol.SourceMount))
	}
	defer span.End()

	state, effects, err := a.app.Init(ctx, a.peer)
	if err != nil {
		a.log.Error(ctx, "gotth-live: the mount hook failed: the session cannot be established",
			obs.Str("session_id", a.idStr), obs.Err(err))
		a.emitError(ctx, pb.ErrorCode_INTERNAL, "the session could not be established", 0, 0, true)
		a.Close(protocol.CloseInternalError, "mount failed")
		return
	}

	a.state = state
	a.transitionID = 1
	a.stateVersion = 1
	a.m.TrackedBytes(ctx, a.TrackedBytes())

	// BR-5. H-10 makes Snapshot the first frame on a connection, and mount's
	// own contract is that a mount which cannot be established emits
	// Error{INTERNAL, fatal} and closes 4012. emitSnapshot's result used to be
	// discarded, and Run releases the read pump immediately afterwards — so a
	// snapshot refused survivably (a fragment past FragmentUpdate.html's bound,
	// say) left server_seq at 0, no snapshot ever written, no close code named,
	// and the connection accepting frames. The shipped client correctly sends
	// nothing without a sequence, so the session sat open through heartbeats
	// until IdleTimeout evicted it as 4011 session_evicted — a close code
	// describing the wrong thing, thirty minutes late.
	origin := protocol.Origin{Kind: pb.OriginKind_MOUNT, Source: protocol.SourceMount}
	if _, ok := a.emitSnapshot(ctx, origin, protocol.Supersession{}, 0); !ok {
		a.log.Error(ctx, "gotth-live: the mount snapshot could not be sent: the session has no first frame and cannot be established",
			obs.Str("session_id", a.idStr))
		a.emitError(ctx, pb.ErrorCode_INTERNAL, "the session could not be established", 0, 0, true)
		a.Close(protocol.CloseInternalError, "the mount snapshot could not be sent")
		return
	}
	// Nothing scheduled the mount, so its effects carry no upstream edge.
	a.runEffects(ctx, effects, 0)
}

// step performs one mailbox message. It is the only place a reducer is called.
func (a *Actor[I]) step(ctx context.Context, m *inbound) {
	defer putInbound(m)

	switch m.kind {
	case msgEvent, msgEffectResult, msgSynthetic:
		a.transition(ctx, m.ev, m.origin, m.span)
	case msgResync:
		a.resync(ctx, m)
	case msgTelemetry:
		a.telemetry(ctx, m)
	}
}

// transition is one reducer invocation and everything that follows from it.
func (a *Actor[I]) transition(ctx context.Context, ev Event, origin protocol.Origin, parent obs.SpanRef) {
	// A true child of the span authorization ran under, through the reference
	// the ingress carried across the goroutine boundary (FR-36 clause 4).
	//
	// This was a link until C-30, and the link's shape was defensible: nothing
	// on the read pump can be a lexical parent of work on the actor goroutine.
	// What made it wrong was not the shape but the arithmetic. A link leaves
	// this span a sampler ROOT, ParentBased does not follow links, and at
	// instrumentation §3.5's stated default the two roots agreed 0 times in
	// 300 interactions — so a span that was unreachable and a span that was
	// merely unsampled looked identical, which is exactly the distinction
	// FR-36 clause 1 asserts. An ended span is still a valid parent, the edge
	// runs in the truthful causal direction, and one decision now covers the
	// whole server-side path.
	//
	// A server-initiated transition — an effect's emission, a synthesized
	// backpressure signal — carries no reference and starts a root, because no
	// authorization admitted it. That is not a partial graph; it is a
	// different graph.
	var span obs.Span
	if a.tr.Enabled() {
		ctx, span = a.tr.StartChildOf(ctx, obs.SpanEvent, parent,
			a.idAttr,
			attribute.Int64(obs.AttrEventID, int64(ev.ID)),
			attribute.String(obs.AttrEventName, ev.Name),
			attribute.Int64(obs.AttrClientRef, int64(ev.ClientRef)),
			attribute.Int64(obs.AttrSeenServerSeq, int64(ev.SeenServerSeq)))
	}
	defer span.End()

	// H-8: a client cannot claim to have seen a patch the server never sent.
	// It is checked here rather than at the ingress because the highest
	// emitted sequence is the actor's state and nothing else may read it.
	if ev.SeenServerSeq > a.serverSeq {
		a.m.EventRejected(ctx, "invalid_causation")
		a.emitError(ctx, pb.ErrorCode_INVALID_FRAME,
			"the event claims to have seen a patch this session has not sent: reconnect and resynchronise",
			ev.ID, ev.ClientRef, false)
		return
	}

	// Dynamic membership is checked here, on its owner. A browser may answer
	// a just-written structural patch before send returns; ingress must not
	// reject that valid event against the previous committed member set.
	if origin.Kind == pb.OriginKind_CLIENT_EVENT && !a.view.KnownID(ev.FragmentID) {
		a.rejectUnknownFragment(ctx, ev.ID, ev.ClientRef)
		return
	}

	prev := a.state
	a.transitionID++

	start := a.now()
	next, effects, failure := a.reduce(ctx, prev, ev)
	elapsed := a.now().Sub(start)

	if failure != nil {
		// The pre-transition state is intact and correct, because a reducer
		// may not mutate its input. Rollback is free rather than implemented.
		a.m.Transition(ctx, "panicked", elapsed.Seconds(), ev.Name)
		a.emitError(ctx, pb.ErrorCode_INTERNAL,
			a.devMessage(reduceFailedMessage, sprint(failure.value), string(failure.stack)),
			ev.ID, ev.ClientRef, false)
		a.notePanic(ctx, "reduce")
		a.provenance(ctx, origin, ev, nil, 0, 0, protocol.Supersession{})
		return
	}

	changed := !a.sameState(prev, next)
	a.state = next
	if changed {
		a.stateVersion++
		a.m.Transition(ctx, "applied", elapsed.Seconds(), ev.Name)
	} else {
		a.m.Transition(ctx, "no_change", elapsed.Seconds(), ev.Name)
	}
	span.SetAttributes(
		attribute.Int64(obs.AttrTransitionID, int64(a.transitionID)),
		attribute.Int64(obs.AttrStateVersion, int64(a.stateVersion)),
		attribute.Bool(obs.AttrResult, changed))

	// Mark runs now; reporting what it caught waits until the transition has
	// emitted whatever it was still able to emit. One rule at all three sites:
	// the fragments that survived reach the client before the error saying one
	// of them did not.
	defer a.noteRenderFailures(ctx, a.view.Mark(prev, next), ev.ID, ev.ClientRef)

	a.emitPatch(ctx, origin, ev, false)
	a.runEffects(ctx, effects, ev.ID)
}

// panicDetail is what a recovered panic knows about itself.
//
// It exists because the site that recovers is not the site that answers the
// client: the guard inside reduce holds the value and the stack, and the Error
// frame that carries them in dev mode is emitted by the caller, which is also
// the only place that holds the causal identifiers H-12 binds them to.
type panicDetail struct {
	value any
	stack []byte
}

// reduce calls the application's reducer under the panic guard. It returns nil
// unless the reducer panicked.
//
// The gotthlive.reduce span is opened here rather than at the call site so it
// closes over exactly the application's reducer and the guard around it — the
// thing an operator attributing latency inside one event is asking about — and
// so a panicking reducer's span records the error rather than ending clean.
func (a *Actor[I]) reduce(ctx context.Context, state any, ev Event) (next any, effects []Effect[I], failure *panicDetail) {
	var span obs.Span
	if a.tr.Enabled() {
		_, span = a.tr.Start(ctx, obs.SpanReduce,
			a.idAttr,
			attribute.String(obs.AttrEventName, ev.Name),
			attribute.Int64(obs.AttrTransitionID, int64(a.transitionID)))
	}
	defer span.End()

	defer func() {
		if r := recover(); r != nil {
			// One stack, captured once and used twice: the log gets it in both
			// modes, and the caller puts it on the wire in dev mode only. Two
			// separate debug.Stack() calls would print two different stacks for
			// one panic, which is the kind of small lie that costs a bisect.
			stack := debug.Stack()
			next, effects, failure = state, nil, &panicDetail{value: r, stack: stack}
			span.RecordError(errors.New("gotth-live: the reducer panicked: " + sprint(r)))
			a.log.Error(ctx, "gotth-live: a reducer panicked: the transition was not applied and the session survives",
				obs.Str("session_id", a.idStr),
				obs.U64("event_id", ev.ID),
				obs.U64("transition_id", a.transitionID),
				obs.Str("event_name", ev.Name),
				obs.Str("site", "reduce"),
				obs.Str("panic", sprint(r)),
				obs.Str("stack", string(stack)))
		}
	}()
	next, effects = a.app.Reduce(state, ev)
	return next, effects, nil
}

// emitPatch renders the dirty fragments and sends one patch, or defers it onto
// the next emission.
//
// It is the backpressure ladder's first two stages. Coalesce engages at half
// the window, where the client is falling behind but headroom remains: a
// transition stops emitting a frame of its own and collapses into the next
// emission, so a session under pressure sends roughly one patch per
// acknowledgement instead of one per transition. Degrade engages at a full
// window, where there is no headroom at all: nothing is emitted until an
// acknowledgement re-opens it, and the application is told through a
// synthesized event. Eviction is the third stage and lives in onTick.
//
// forced is set when the caller already knows headroom exists — an
// acknowledgement, or a heartbeat finding deferred work — so that a deferral
// has a bounded end and cannot sit in the coalesce stage indefinitely.
//
// Coalescing defers one transition and merges it into the next, rather than
// deferring until something external intervenes. That is what keeps all three
// stages reachable: a stage that stopped emitting entirely would hold the
// depth at half the window forever and degrade could never be entered, which
// would be two stages wearing three stages' clothes.
func (a *Actor[I]) emitPatch(ctx context.Context, origin protocol.Origin, ev Event, forced bool) {
	a.m.WindowDepth(ctx, a.win.depth())

	// The flush trigger. The contributing-event union has a schema ceiling,
	// and that ceiling is reachable in ordinary operation rather than only
	// under attack, so it is a flush rather than a truncation or an error:
	// truncating would lose provenance silently, and erroring would let a slow
	// client kill its own session by a path nobody designed.
	//
	// It is evaluated against the union this emission would actually build,
	// not against len(pendingIDs). Those were different numbers, and the
	// difference was the application's: deferPatch folds an application's
	// Contributing into pendingIDs, but the origin about to be emitted carries
	// its own, which unionEdges merges in below and which the count above it
	// never saw. A legal per-event Contributing plus a legal CoalesceFlushAt
	// could therefore still build a frame the schema refuses — the flush
	// trigger becoming the emission failure it exists to prevent, which is
	// D-14's failure reached by an input D-14 did not bound (D-18, C-31).
	mustFlush := a.unionReaches(origin, a.lim.CoalesceFlushAt)

	switch {
	case mustFlush:
		// Emit even though the window says otherwise: one extra frame to a
		// client that is already behind, against losing provenance.
	case a.win.full():
		a.degrade(ctx, origin)
		a.provenance(ctx, origin, ev, nil, 0, 0, protocol.Supersession{})
		return
	case a.win.coalescing() && !forced && !a.coalesceHeld:
		a.coalesceHeld = true
		a.enterCoalesce(ctx, origin)
		a.provenance(ctx, origin, ev, nil, 0, 0, protocol.Supersession{})
		return
	}

	a.coalesceHeld = false
	origin, contributing := a.takePending(origin)

	start := a.now()
	res := a.renderPass(ctx, false)
	a.m.RenderDuration(ctx, a.now().Sub(start).Seconds(), firstFragment(res.Updates))

	// Deferred so the fragments that did render reach the client before the
	// error saying one of them did not, on every exit from here including the
	// one where there was nothing left to patch at all.
	defer a.noteRenderFailures(ctx, res.Failed, ev.ID, ev.ClientRef)
	a.m.PatchesSuppressed(ctx, len(res.Suppressed))

	if len(res.Updates) == 0 {
		// BR-4. Nothing is emitted here, so nothing may be spent here.
		// takePending is a take and not a commit: this exit is reachable
		// whenever a flushing transition's render is fully suppressed — the
		// fragments were marked dirty by a real state change and rendered to
		// the bytes already on the wire — and the union taken above used to be
		// discarded with the local, absent from the wire AND from the
		// provenance row, which carried the pre-union origin.
		a.redefer(ctx, origin, contributing)

		// A suppressed render still produced a transition, and the record of
		// it is what makes "the state version rises exactly when state
		// changed" checkable at all.
		a.provenance(ctx, origin, ev, nil, 0, 0, protocol.Supersession{})
		return
	}

	origin.Contributing = unionEdges(origin, contributing)
	causal := protocol.Causal{
		ServerSeq:    a.serverSeq + 1,
		PatchID:      a.patchID + 1,
		TransitionID: a.transitionID,
		StateVersion: a.stateVersion,
	}
	frame := protocol.NewPatch(a.peer.ID, causal, origin, toWireUpdates(res.Updates))

	_, span, ok := a.send(ctx, frame, causal)
	if !ok {
		// BR-3. The render pass computed hashes and cleared dirty bits; none of
		// that may stand, because the markup it describes never reached the
		// socket. Discard puts the fragments back in the dirty set and leaves
		// their hashes uninstalled, so the next render of the same bytes is
		// emitted rather than suppressed as already-delivered.
		a.view.Discard(res)
		a.noteStale(ctx, res)
		// BR-4. origin.Contributing is the whole union by now, and the frame
		// carrying it never left. It is owed to the next emission, not to the
		// local that is about to go out of scope.
		a.redefer(ctx, origin, contributing)
		return
	}
	a.view.Commit(res)
	if len(contributing) > 0 {
		a.m.PatchCoalesced(ctx)
	}
	a.countUpdates(ctx, res.Updates)
	a.win.push(slot{
		serverSeq: causal.ServerSeq,
		patchID:   causal.PatchID,
		span:      span,
	})
	a.provenance(ctx, origin, ev, fragmentIDs(res.Updates), causal.PatchID, causal.ServerSeq, protocol.Supersession{})
}

// emitSnapshot renders every fragment and sends a snapshot, reporting the
// encoded size and whether it reached the transport.
//
// The second return is not derivable from the first by a caller: a zero size
// and a failure look alike only by accident today, and H-10 turns on the
// difference — mount must close a connection whose snapshot never went out
// rather than serve it (BR-5).
func (a *Actor[I]) emitSnapshot(ctx context.Context, origin protocol.Origin, sup protocol.Supersession, eventID uint64) (int, bool) {
	start := a.now()
	res := a.renderPass(ctx, true)
	a.m.RenderDuration(ctx, a.now().Sub(start).Seconds(), "")

	// Deferred, and here the ordering is load-bearing rather than merely
	// tidier: H-10 says the Snapshot is a connection's first frame, so a
	// fragment that panics during mount must not put an Error in front of it.
	defer a.noteRenderFailures(ctx, res.Failed, eventID, origin.ClientRef)

	origin, contributing := a.takePending(origin)
	origin.Contributing = unionEdges(origin, contributing)

	causal := protocol.Causal{
		ServerSeq:    a.serverSeq + 1,
		PatchID:      a.patchID + 1,
		TransitionID: a.transitionID,
		StateVersion: a.stateVersion,
	}
	params := protocol.SessionParams{
		HeartbeatIntervalMS:  uint32(a.lim.HeartbeatInterval / time.Millisecond),
		MaxInboundFrameBytes: uint32(a.lim.MaxInboundFrameBytes),
		AckWindow:            uint32(a.lim.AckWindow),
	}
	frame := protocol.NewSnapshot(a.peer.ID, causal, origin, params, sup, toWireUpdates(res.Updates))

	n, span, ok := a.send(ctx, frame, causal)
	if !ok {
		a.view.Discard(res)
		a.noteStale(ctx, res)
		a.redefer(ctx, origin, contributing)
		return 0, false
	}
	a.view.Commit(res)
	a.lastSnapshotSeq = causal.ServerSeq
	a.countUpdates(ctx, res.Updates)
	a.win.push(slot{
		serverSeq: causal.ServerSeq,
		patchID:   causal.PatchID,
		span:      span,
	})
	a.provenance(ctx,
		origin,
		Event{ID: eventID, ClientRef: origin.ClientRef},
		fragmentIDs(res.Updates), causal.PatchID, causal.ServerSeq, sup)
	return n, true
}

const patchUpdatesField = "gotthlive.v1.Patch.updates"

// renderPass runs one render under gotthlive.render. The per-fragment spans
// inside it come from the observer installed once in New.
func (a *Actor[I]) renderPass(ctx context.Context, all bool) render.Result {
	var span obs.Span
	if a.tr.Enabled() {
		ctx, span = a.tr.Start(ctx, obs.SpanRender,
			a.idAttr,
			attribute.Int64(obs.AttrTransitionID, int64(a.transitionID)))
	}
	defer span.End()

	if all {
		return a.view.RenderAll(ctx, a.state)
	}
	result := a.view.Render(ctx, a.state)
	bound, _ := protocol.ListBound(patchUpdatesField)
	if len(result.Updates) > bound {
		// A large child update set fits as the existing parent regions. Never
		// install hashes for an oversized pass the protocol would refuse.
		a.view.Discard(result)
		complete := a.view.RenderAll(ctx, a.state)
		complete.Failed = append(result.Failed, complete.Failed...)
		return complete
	}
	return result
}

// observeFragments installs gotthlive.render.fragment, one span per fragment a
// pass considers.
//
// It is a closure handed to the renderer rather than a tracer the renderer
// holds, and that is structural: an architecture test forbids anything on the
// render path from reaching a clock, a logger or the outside world, and
// internal/obs imports log/slog and time. It is installed once per session,
// never per pass, and only when tracing is on — so a disabled configuration
// leaves the renderer's hook nil and pays one branch per fragment.
func (a *Actor[I]) observeFragments() {
	if !a.tr.Enabled() {
		return
	}
	a.view.Observe(func(ctx context.Context, fragmentID string) (context.Context, func(suppressed, failed bool)) {
		ctx, span := a.tr.Start(ctx, obs.SpanRenderFragment,
			a.idAttr,
			attribute.Int64(obs.AttrTransitionID, int64(a.transitionID)),
			attribute.String(obs.AttrFragmentID, fragmentID))
		return ctx, func(suppressed, failed bool) {
			span.SetAttributes(attribute.Bool(obs.AttrSuppressed, suppressed))
			if failed {
				// The panic value and the stack are in the log record and in
				// gotthlive_panics_total{site="render"}; what the span owes an
				// operator is which fragment is the stale one.
				span.RecordError(errFragmentRender)
			}
			span.End()
		}
	})
}

// errFragmentRender marks a render.fragment span whose fragment did not
// produce markup. It is a sentinel because the span carries the fact and the
// log record carries the panic value; putting the value on the span too would
// duplicate a payload §6.4 keeps out of records that leave the process.
var errFragmentRender = errors.New("gotth-live: the fragment could not be rendered: that region is stale and the log record for this transition carries the panic")

// send validates, encodes and writes one sequenced frame.
//
// The sequence counters advance only after the write succeeds. A frame this
// library could not validate is therefore dropped without leaving a hole in
// the sequence, which is what lets the wire audit assert contiguity and treat
// any gap as evidence of a second write path.
//
// Encode and write are timed and spanned separately. They were one call until
// FR-36's gotthlive.send span landed, and the cost of that was not only a
// missing span: gotthlive_send_duration_seconds is defined as "time in
// Conn.Write, the write-stall signal" and was recording validate-plus-marshal
// as well, so it and gotthlive_encode_duration_seconds were equal by
// construction and neither could isolate a stalling client.
func (a *Actor[I]) send(ctx context.Context, frame *pb.Frame, causal protocol.Causal) (int, obs.SpanRef, bool) {
	var span obs.Span
	if a.tr.Enabled() {
		ctx, span = a.tr.Start(ctx, obs.SpanEncode,
			a.idAttr,
			attribute.Int64(obs.AttrTransitionID, int64(causal.TransitionID)),
			attribute.Int64(obs.AttrPatchID, int64(causal.PatchID)),
			attribute.Int64(obs.AttrServerSeq, int64(causal.ServerSeq)))
	}
	ref := span.Ref()

	start := a.now()
	encoded, err := a.fr.Encode(frame)
	a.m.EncodeDuration(ctx, a.now().Sub(start).Seconds())

	var n int
	if err == nil {
		span.SetAttributes(attribute.Int(obs.AttrFrameBytes, encoded.Len()))

		var sendSpan obs.Span
		sendCtx := ctx
		if a.tr.Enabled() {
			sendCtx, sendSpan = a.tr.Start(ctx, obs.SpanSend,
				a.idAttr,
				attribute.Int64(obs.AttrTransitionID, int64(causal.TransitionID)),
				attribute.Int64(obs.AttrPatchID, int64(causal.PatchID)),
				attribute.Int64(obs.AttrServerSeq, int64(causal.ServerSeq)),
				attribute.Int(obs.AttrFrameBytes, encoded.Len()),
				attribute.Int(obs.AttrWindowDepth, a.win.depth()))
		}
		wrote := a.now()
		n, err = a.fr.Write(sendCtx, encoded)
		a.m.SendDuration(ctx, a.now().Sub(wrote).Seconds())
		sendSpan.RecordError(err)
		sendSpan.End()
	}

	span.SetAttributes(attribute.Int(obs.AttrWindowDepth, a.win.depth()))
	span.RecordError(err)
	span.End()

	if err == nil {
		a.serverSeq = causal.ServerSeq
		a.patchID = causal.PatchID
		return n, ref, true
	}

	var invalid *protocol.InvalidFrameError
	if errors.As(err, &invalid) {
		// Constructing a frame we cannot validate is this library's fault and
		// never the client's: the frame was built here. It is not always a
		// coding bug, and the message stops short of saying so — until D-18 an
		// over-long Event.Contributing from the application reached exactly
		// this branch, and telling an operator "this is a library bug" about
		// their own emitted event sends them to the wrong repository. That
		// input is now refused at the emit path with an error naming the
		// caller; what reaches here is a bound this library did not apply.
		//
		// The frame is dropped, the sequence stays contiguous, and an error
		// carrying the same causal chain goes in its place.
		a.log.Error(ctx, "gotth-live: refused to send a frame this library could not validate: the frame was built by this library, so this is not a client problem",
			obs.Str("session_id", a.idStr),
			obs.U64("patch_id", causal.PatchID),
			obs.U64("transition_id", causal.TransitionID),
			obs.Err(err))
		a.emitError(ctx, pb.ErrorCode_INTERNAL, "the server could not encode an update", 0, 0, false)
		return 0, ref, false
	}

	if a.win.full() {
		a.Close(protocol.CloseSlowClient, "the write stalled with a full outbound window")
	} else {
		a.Close(protocol.CloseNormal, "the connection failed on write")
	}
	return 0, ref, false
}

// noteStale records that a pass's markup did not reach the client and the
// fragments it named are being re-marked for the next one.
//
// There is no instrument for this: the frame's own failure is already counted
// by gotthlive_outbound_validation_failed_total or ends the connection, and
// what this record adds is which regions the client is missing until the retry
// lands. A counter would need a name and a row in instrumentation.md, which is
// a decision rather than a fix.
func (a *Actor[I]) noteStale(ctx context.Context, res render.Result) {
	if len(res.Updates) == 0 {
		return
	}
	a.log.Warn(ctx, "gotth-live: a patch did not reach the client: those regions are re-marked and will be rendered again rather than suppressed as already delivered",
		obs.Str("session_id", a.idStr),
		obs.U64("transition_id", a.transitionID),
		obs.Int("fragments", len(res.Updates)))
}

// deferPatch records a transition whose patch was not emitted, so the next
// emission carries its provenance rather than losing it.
//
// Nothing is dropped on the way: the transition being displaced hands over
// both its own event identifier and the edges it had already collected.
func (a *Actor[I]) deferPatch(origin protocol.Origin) {
	if prev := a.pendingOrig; prev != nil {
		if prev.EventID != 0 {
			a.pendingIDs = append(a.pendingIDs, prev.EventID)
		}
		a.pendingIDs = append(a.pendingIDs, prev.Contributing...)
	}
	o := origin
	a.pendingOrig = &o
}

// enterCoalesce is the ladder's first stage: half the window is outstanding,
// so transitions stop emitting a frame each and collapse into the next one.
func (a *Actor[I]) enterCoalesce(ctx context.Context, origin protocol.Origin) {
	a.deferPatch(origin)

	if !a.coalesceNotified {
		a.coalesceNotified = true
		a.log.Warn(ctx, "gotth-live: the outbound window is half full: patches are being coalesced",
			obs.Str("session_id", a.idStr),
			obs.Int("window_depth", a.win.depth()),
			obs.Int("ack_window", a.lim.AckWindow))
	}
	a.win.noteFullness(a.now())
}

// degrade is the ladder's second stage: the window is full, so nothing is
// emitted at all until an acknowledgement re-opens it.
func (a *Actor[I]) degrade(ctx context.Context, origin protocol.Origin) {
	a.deferPatch(origin)

	if !a.slowNotified {
		a.slowNotified = true
		a.m.SlowClientEvent(ctx)
		a.log.Warn(ctx, "gotth-live: the outbound window is full: rendering is paused until the client acknowledges",
			obs.Str("session_id", a.idStr),
			obs.Int("window_depth", a.win.depth()))
		a.synthesize(protocol.SourceSlowClient)
	}
	a.win.noteFullness(a.now())
}

// unionReaches reports whether the contributing union the frame this emission
// would build reaches n.
//
// It answers the question the flush trigger asks, and it answers it about the
// frame rather than about the deferred set, which is the whole of C-31(b): the
// two counts differ by exactly what the application contributed to the event
// being emitted, and that term was the one nobody had bounded.
//
// The sum of the parts settles it first, and that is the path every ordinary
// emission takes: a union is never larger than the sum of the sets it is built
// from, so a sum below n is an answer without allocating a map. The exact set
// is built only when the sum has already reached n — where being wrong costs a
// frame, and where the map is about to be built by unionEdges anyway.
//
// The exact half must agree with unionEdges exactly, so it applies the same
// three rules: the origin's own event identifier is not a contributor to its
// own patch, zero is not an identifier, and an identifier named twice is named
// once.
func (a *Actor[I]) unionReaches(origin protocol.Origin, n int) bool {
	upper := len(a.pendingIDs) + len(origin.Contributing)
	if prev := a.pendingOrig; prev != nil {
		upper += 1 + len(prev.Contributing)
	}
	if upper < n {
		return false
	}

	seen := make(map[uint64]struct{}, upper)
	if origin.EventID != 0 {
		seen[origin.EventID] = struct{}{}
	}
	count := 0
	add := func(ids ...uint64) {
		for _, id := range ids {
			if id == 0 {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			count++
		}
	}

	add(origin.Contributing...)
	add(a.pendingIDs...)
	if prev := a.pendingOrig; prev != nil {
		add(prev.EventID)
		add(prev.Contributing...)
	}
	return count >= n
}

// redefer puts back what takePending took, for an exit that took a
// transition's deferred provenance and then emitted nothing.
//
// H-4 makes the contributing bound a coalescing flush trigger, "never a
// truncation", and P5 states the union over a run as SET EQUALITY with the
// events that changed state and were not individually patched. takePending
// clears pendingIDs and pendingOrig unconditionally, and emitPatch has two
// exits after it that never emit — a fully suppressed render, and a survivable
// send failure — so the union was built and then discarded with the local.
// emitSnapshot has the third. This is the emit-or-restore half.
//
// The origin itself is redeferred through deferPatch, not merely its edges: the
// transition that did not get its own patch is a contributor to whatever patch
// comes next, which is exactly what deferPatch is for.
//
// The one bound. pendingIDs is a raw list rather than a set — it is deduplicated
// when the union is built — so a session whose emissions keep failing would grow
// it without limit, and a session that cannot send is exactly the one that keeps
// failing. It is capped at the schema's own ceiling on the field it feeds:
// past that, no frame could carry the list anyway, and unbounded per-session
// memory behind a failure the operator is already being told about is the worse
// of the two. Reaching it is loud, because every emission that got here has
// already logged its own failure.
func (a *Actor[I]) redefer(ctx context.Context, origin protocol.Origin, contributing []uint64) {
	a.pendingIDs = append(a.pendingIDs, contributing...)
	if over := len(a.pendingIDs) - protocol.CoalesceFlushCeiling; over > 0 {
		a.pendingIDs = a.pendingIDs[:protocol.CoalesceFlushCeiling]
		a.log.Error(ctx, "gotth-live: dropped deferred provenance that no frame could carry: this session has not been able to emit for long enough to accumulate more contributing events than the schema's ceiling",
			obs.Str("session_id", a.idStr),
			obs.U64("transition_id", a.transitionID),
			obs.Int("dropped", over),
			obs.Int("ceiling", protocol.CoalesceFlushCeiling))
	}
	a.deferPatch(origin)
}

// takePending folds any deferred transitions into the origin about to be
// emitted. The most recent deferred transition is the proximate cause when the
// emission has none of its own; the rest contribute, and none is ever dropped.
//
// It is a TAKE, not a commit. An exit that does not emit what it took must hand
// it back through redefer.
func (a *Actor[I]) takePending(origin protocol.Origin) (protocol.Origin, []uint64) {
	contributing := a.pendingIDs
	a.pendingIDs = nil

	if prev := a.pendingOrig; prev != nil {
		if prev.EventID != 0 && prev.EventID != origin.EventID {
			contributing = append(contributing, prev.EventID)
		}
		contributing = append(contributing, prev.Contributing...)
		a.pendingOrig = nil
	}
	return origin, contributing
}

// unionEdges merges an origin's own contributing edges with the ones collected
// from deferred transitions, in first-seen order and without duplicates.
//
// The origin's own event identifier is excluded: an event is either the cause
// of a patch or a contributor to it, and counting it twice would make the
// provenance set-equality property false in the direction that is hardest to
// notice.
func unionEdges(origin protocol.Origin, deferred []uint64) []uint64 {
	if len(origin.Contributing) == 0 && len(deferred) == 0 {
		return nil
	}

	seen := make(map[uint64]struct{}, len(origin.Contributing)+len(deferred))
	if origin.EventID != 0 {
		seen[origin.EventID] = struct{}{}
	}

	out := make([]uint64, 0, len(origin.Contributing)+len(deferred))
	for _, id := range append(append([]uint64(nil), origin.Contributing...), deferred...) {
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// synthesize injects a backpressure signal into the actor's own mailbox.
//
// Backpressure reaches the application as an event rather than as a call into
// transport state, because a reducer that could read the window would produce
// different results for the same event log under different network
// conditions, which would destroy replayability.
func (a *Actor[I]) synthesize(source string) {
	m := getInbound()
	m.kind = msgSynthetic
	m.ev = Event{Name: source, At: a.now()}
	m.origin = protocol.Origin{Kind: pb.OriginKind_TIMER, Source: source}
	a.post(m)
}

// onAck applies a client acknowledgement and renders once, from current state,
// if the window re-opened onto pending work.
func (a *Actor[I]) onAck(ctx context.Context, seq uint64) {
	if err := a.win.ack(seq); err != nil {
		a.log.Error(ctx, "gotth-live: a client acknowledged a patch this session never sent",
			obs.Str("session_id", a.idStr), obs.U64("server_seq", seq), obs.Err(err))
		a.Close(protocol.CloseProtocolViolation, "invalid acknowledgement")
		return
	}
	a.m.WindowDepth(ctx, a.win.depth())
	a.win.noteFullness(a.now())

	if !a.win.coalescing() {
		a.coalesceNotified = false
		a.coalesceHeld = false
	}
	if a.win.full() || !a.view.Pending() {
		return
	}
	if a.slowNotified {
		a.slowNotified = false
		a.synthesize(protocol.SourceClientRecovered)
	}

	origin := protocol.Origin{Kind: pb.OriginKind_TIMER, Source: protocol.SourceClientRecovered}
	if a.pendingOrig != nil {
		origin = *a.pendingOrig
	}
	a.emitPatch(ctx, origin, Event{ID: origin.EventID, ClientRef: origin.ClientRef}, true)
}

// onTick is liveness, idle eviction, slow-client eviction and the heartbeat.
func (a *Actor[I]) onTick(ctx context.Context, now time.Time) {
	if now.IsZero() {
		now = a.now()
	}

	if since := now.Sub(time.Unix(0, a.lastInboundNS.Load())); since > a.lim.HeartbeatTimeout {
		a.Close(protocol.CloseHeartbeatTimeout, "no frame from the client within the heartbeat timeout")
		return
	}
	if since := now.Sub(time.Unix(0, a.lastEventNS.Load())); since > a.lim.IdleTimeout {
		a.Close(protocol.CloseSessionEvicted, "no activity within the idle timeout")
		return
	}
	if full := a.win.noteFullness(now); !full.IsZero() && now.Sub(full) > a.lim.SlowClientGrace {
		a.Close(protocol.CloseSlowClient, "the outbound window stayed full past the grace period")
		return
	}

	// The coalesce stage defers a patch until something re-opens the window,
	// and an acknowledgement is normally that something. A session that has
	// gone quiet has no acknowledgement coming, so the tick is what bounds the
	// deferral: deferred work waits at most one heartbeat interval, never
	// indefinitely.
	if a.view.Pending() && !a.win.full() {
		origin := protocol.Origin{Kind: pb.OriginKind_TIMER, Source: protocol.SourceClientRecovered}
		if a.pendingOrig != nil {
			origin = *a.pendingOrig
		}
		a.emitPatch(ctx, origin, Event{ID: origin.EventID, ClientRef: origin.ClientRef}, true)
	}

	a.hbNonce++
	frame := protocol.NewHeartbeat(a.peer.ID, a.hbNonce, uint32(a.lim.HeartbeatInterval/time.Millisecond))
	if _, err := a.fr.Send(ctx, frame); err != nil {
		a.Close(protocol.CloseNormal, "the connection failed on a heartbeat write")
	}
}

// notePanic records a recovered panic against the session's budget. A session
// that keeps panicking at one site closes; every other session keeps serving.
func (a *Actor[I]) notePanic(ctx context.Context, site string) {
	a.m.Panic(ctx, site)
	a.panics[site]++
	if a.panics[site] >= a.lim.PanicBudget {
		a.Close(protocol.CloseInternalError, "the session exceeded its panic budget at "+site)
	}
}

// The generic messages the client is told in production. They are fixed
// strings so that what reaches a browser does not vary with what failed
// (FR-23, checklist §5.9); dev mode appends to them and changes neither.
const (
	reduceFailedMessage = "the transition failed"
	renderFailedMessage = "a region of the page could not be rendered and is stale"
)

// noteRenderFailures reports one render pass's recovered panics: each of them
// to the log and to the session's panic budget, and one Error frame for the
// pass as a whole (RFC §9 — every recovery gets a frame carrying the causal
// ID).
//
// One frame per pass rather than one per fragment. In production the message
// is a fixed string, so N frames for N fragments broken by one shared helper
// would repeat one sentence N times and tell a client nothing the first did
// not, while a registry of sixty-four fragments would put sixty-four writes on
// a connection in the same instant the panic budget closed it. The per-fragment
// record is the log line and the gotthlive_panics_total{site="render"}
// increment, and both still happen exactly once per failure, so neither the
// budget's arithmetic nor an operator's view of it changes.
//
// eventID and clientRef are the causal chain, and they are both zero on the
// passes no client frame caused — a mount snapshot, a timer, an effect's
// emission — which is the pairing H-12 requires. They are passed rather than
// derived because only the caller knows which transition this pass belongs to.
//
// The failure is not fatal. A stale region is not a dead session: the other
// fragments patched, resync is deliberately not triggered (a render that panics
// will panic again), and the budget is what ends a session that cannot stop.
func (a *Actor[I]) noteRenderFailures(ctx context.Context, failures []render.Failure, eventID, clientRef uint64) {
	if len(failures) == 0 {
		return
	}
	for _, f := range failures {
		a.log.Error(ctx, "gotth-live: rendering a fragment failed: that region is stale and the others patched normally",
			obs.Str("session_id", a.idStr),
			obs.Str("fragment_id", f.FragmentID),
			obs.Str("site", f.Site),
			// FR-58's causal clause, and it was missing until the Phase 4 error
			// audit walked this record. eventID is a parameter of this function
			// and was already being put on the error frame; the log line an
			// operator reads FIRST dropped it, so a stale region could be read
			// off the record and the click that produced it could not. That is
			// exactly the shape checkpoint 2 closed at the effect-panic site
			// (8fb6ade9), one site over. Emitted unconditionally, including the
			// zero a server-initiated transition carries, because a field that
			// appears only sometimes cannot be queried for.
			obs.U64("event_id", eventID),
			obs.U64("transition_id", a.transitionID),
			obs.Str("panic", sprint(f.Value)),
			obs.Str("stack", string(f.Stack)))
	}

	// The frame goes out before the budget is charged, exactly as the reducer
	// path does it: notePanic can close the session, and the reason for a close
	// is owed to the client before the close arrives.
	a.emitError(ctx, pb.ErrorCode_INTERNAL,
		a.devMessage(renderFailedMessage, renderDetail(failures)...),
		eventID, clientRef, false)

	for range failures {
		a.notePanic(ctx, "render")
	}
}

// renderDetail is dev mode's addition to a render failure's Error frame.
//
// Only the first failure of a pass is carried, with a count of the rest.
// Error.message is capped at 512 bytes by the schema, so a second stack would
// displace the first rather than join it, and the log already holds every one
// of them in full.
func renderDetail(failures []render.Failure) []string {
	f := failures[0]
	head := "fragment " + f.FragmentID + " (" + f.Site + "): " + sprint(f.Value)
	if rest := len(failures) - 1; rest > 0 {
		head += fmt.Sprintf(" (and %d more in this pass; the log has them all)", rest)
	}
	return []string{head, string(f.Stack)}
}

// devMessage is FR-23's dev/prod split, and it is the whole of what Config.Dev
// does.
//
// In production it returns the fixed generic string and nothing else, so the
// bytes a client receives do not depend on what failed: the client learns that
// something failed and which of its events it failed on, and every internal
// detail stays in the operator's log (checklist §5.9).
//
// In dev mode the detail is appended, one line each. The frame is a pointer
// into the log rather than a replacement for it — protocol.md bounds
// Error.message at 512 bytes and protocol.NewError truncates to fit, so what
// reaches a browser is the head of a stack whose whole is logged at error level
// in both modes. That is the trade the existing field already makes, and it is
// why this needed no protocol change.
func (a *Actor[I]) devMessage(generic string, detail ...string) string {
	if !a.dev {
		return generic
	}
	var b strings.Builder
	b.WriteString(generic)
	for _, d := range detail {
		if d == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(d)
	}
	return b.String()
}

// emitError sends an error frame carrying the causal chain.
func (a *Actor[I]) emitError(ctx context.Context, code pb.ErrorCode, message string, eventID, clientRef uint64, fatal bool) {
	frame := protocol.NewError(a.peer.ID, code, message, eventID, clientRef, fatal)
	if _, err := a.fr.Send(ctx, frame); err != nil {
		// The causal identifiers are on the record and not only on the frame
		// that failed to go out. An undelivered Error frame is the one case
		// where the client will never learn which of its interactions was
		// refused, so the server-side record is the only surviving copy of
		// that edge (FR-58).
		a.log.Error(ctx, "gotth-live: could not deliver an error frame to the client: that interaction's failure reached nobody",
			obs.Str("session_id", a.idStr),
			obs.U64("event_id", eventID),
			obs.U64("client_ref", clientRef),
			obs.Bool("fatal", fatal),
			obs.Err(err))
	}
}

// provenance emits one transition's causal row.
func (a *Actor[I]) provenance(ctx context.Context, o protocol.Origin, ev Event, fragments []string, patchID, serverSeq uint64, sup protocol.Supersession) {
	a.log.Provenance(ctx, obs.Provenance{
		SessionID:            a.idStr,
		EventID:              ev.ID,
		ClientRef:            ev.ClientRef,
		TransitionID:         a.transitionID,
		StateVersion:         a.stateVersion,
		PatchID:              patchID,
		ServerSeq:            serverSeq,
		OriginKind:           o.Kind.String(),
		OriginSource:         o.Source,
		FragmentIDs:          fragments,
		ContributingEventIDs: o.Contributing,
		SupersededFromSeq:    sup.FromSeq,
		SupersededThroughSeq: sup.ThroughSeq,
	})
}

func (a *Actor[I]) countUpdates(ctx context.Context, updates []render.Update) {
	var morph, appendOp, prepend, remove int
	for _, u := range updates {
		switch u.Op {
		case render.OpAppend:
			appendOp++
		case render.OpPrepend:
			prepend++
		case render.OpRemove:
			remove++
		default:
			morph++
		}
	}
	a.m.PatchesSent(ctx, "morph", morph)
	a.m.PatchesSent(ctx, "append", appendOp)
	a.m.PatchesSent(ctx, "prepend", prepend)
	a.m.PatchesSent(ctx, "remove", remove)
}

func toWireUpdates(us []render.Update) []protocol.Update {
	out := make([]protocol.Update, len(us))
	for i, u := range us {
		out[i] = protocol.Update{FragmentID: u.FragmentID, Op: wireOp(u.Op), HTML: u.HTML}
	}
	return out
}

func wireOp(op render.Op) pb.PatchOp {
	switch op {
	case render.OpAppend:
		return pb.PatchOp_APPEND
	case render.OpPrepend:
		return pb.PatchOp_PREPEND
	case render.OpRemove:
		return pb.PatchOp_REMOVE
	default:
		return pb.PatchOp_MORPH
	}
}

func fragmentIDs(us []render.Update) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.FragmentID
	}
	return out
}

func firstFragment(us []render.Update) string {
	if len(us) == 0 {
		return ""
	}
	return us[0].FragmentID
}

// sameState reports whether a transition left state unchanged, which is what
// decides whether the state version rises.
//
// Go cannot compare two arbitrary values without risking a panic, so a
// comparable state type is compared and anything else is treated as changed.
// That is the safe direction: reporting a change that did not happen costs a
// suppressed render, while reporting no change that did happen would freeze
// the version and make the provenance property false.
//
// BR-7: "comparable" used to mean reflect.Type.Comparable, and a POINTER is
// comparable — so for S = *Foo the fast path compared identity rather than
// value, and a reducer that mutated in place and returned the same pointer got
// "unchanged" for a change that happened. That is the unsafe direction the
// comment above claims was avoided: measured over two in-place transitions,
// zero patches and state_version frozen at 1, so P4 was false on the wire.
// With Dirty == nil the patches did go out — every fragment is force-marked —
// and state_version was frozen anyway.
//
// The predicate is now the application's, resolved once at construction
// (IApp.StateComparable), so the reference kinds fall through to "changed" and
// the decision about what actually moved is left to each fragment's Dirty
// declaration and to suppression, which compare the rendered bytes.
//
// What this cannot repair is the aliasing itself: a.view.Mark(prev, next)
// receives two handles to one mutated value, so an application's Dirty compares
// something against itself. Only the reducer can avoid that, by returning a new
// value, which is what the purity rule requires; live.Config[S] documents it at
// the type parameter.
func (a *Actor[I]) sameState(prev, next any) bool {
	if prev == nil || next == nil {
		return prev == next
	}
	if !a.stateComparable {
		return false
	}
	// Still checked per transition: StateComparable is an answer about the
	// declared type, and this is an assertion about the two values actually in
	// hand. A mismatch would make == panic.
	if reflect.TypeOf(prev) != reflect.TypeOf(next) {
		return false
	}
	return prev == next
}

func sprint(v any) string {
	if err, ok := v.(error); ok {
		return err.Error()
	}
	return fmt.Sprintf("%v", v)
}
