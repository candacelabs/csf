package session

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// ResyncEventName is the reserved name a resync request is authorized under.
//
// A resync reaches the actor and triggers work proportional to the whole
// state, so it is not exempt from authorization the way the pure plumbing
// frames are; it is authorized as a distinguished event kind instead. An
// application that authorizes by name must permit this one or its clients
// cannot recover from a gap.
const ResyncEventName = "gotth.resync"

// consecutiveDenialsBeforeClose is how many refusals in a row turn a rate
// limit into a close. A client that keeps sending after being told to stop is
// not backing off, and the limit exists to bound work rather than to be
// repeated at forever.
const consecutiveDenialsBeforeClose = 3

// Ingress is the only path from the wire into this actor.
//
// It runs on the connection's read pump, not on the actor goroutine, and that
// placement is the security property: an event is rate limited, checked
// against the registered names, and authorized before it occupies a mailbox
// slot. A new client frame kind cannot skip the hook, because this switch is
// exhaustive over a sum type closed in another package and there is no other
// way into the mailbox from the wire.
//
// The frames that do not pass through Authorize are exactly three, and each is
// accounted for: an acknowledgement and a heartbeat are transport plumbing
// that no reducer can observe, and client telemetry is a report about a patch
// this session already sent. None of them can reach application state.
func (a *Actor[I]) Ingress(ctx context.Context, in protocol.IInbound) {
	now := a.now()
	a.lastInboundNS.Store(now.UnixNano())

	switch v := in.(type) {
	case protocol.InboundEvent:
		a.lastEventNS.Store(now.UnixNano())
		a.ingressEvent(ctx, v, now)
	case protocol.InboundResyncRequest:
		a.lastEventNS.Store(now.UnixNano())
		a.ingressResync(ctx, v, now)
	case protocol.InboundAck:
		a.ingressAck(ctx, v)
	case protocol.InboundClientTelemetry:
		a.ingressTelemetry(ctx, v)
	case protocol.InboundHeartbeat:
		// Liveness, recorded above. A heartbeat reaches nothing else.
	}
}

func (a *Actor[I]) ingressEvent(ctx context.Context, in protocol.InboundEvent, now time.Time) {
	name := in.Name()
	clientRef := in.ClientRef()

	// The event identifier is minted before any refusal, not after the last
	// one. Every error this function can emit is scoped to one event, and an
	// error frame's two causal identifiers are set or clear together, so an
	// identifier that only exists on the accepting path would leave every
	// rejection unable to say which interaction it refused.
	a.eventSeq++
	eventID := a.eventSeq

	if !a.eventBucket.allow(now) {
		a.m.EventRejected(ctx, "rate_limited")
		a.eventDenied++
		a.log.Warn(ctx, "gotth-live: an event was refused by the inbound rate limit",
			obs.Str("session_id", a.idStr),
			obs.Str("event_name", name),
			obs.U64("client_ref", clientRef))
		a.emitError(ctx, pb.ErrorCode_RATE_LIMITED,
			"too many events: slow down and retry", eventID, clientRef, false)
		if a.eventDenied >= consecutiveDenialsBeforeClose*a.lim.EventBurst {
			a.Close(protocol.CloseRateLimited, "sustained inbound event flood")
		}
		return
	}
	a.eventDenied = 0

	// Default-deny. An event whose name is not registered is refused and
	// counted, never dispatched and never ignored.
	if !a.app.Registered(name) {
		a.m.EventRejected(ctx, "unknown_event")
		a.emitError(ctx, pb.ErrorCode_UNKNOWN_EVENT,
			"the event name is not registered by this application: declare it in Config.Events",
			eventID, clientRef, false)
		return
	}

	fragmentID := in.FragmentID()
	if !a.app.Registry().AdmitsID(fragmentID) {
		a.rejectUnknownFragment(ctx, eventID, clientRef)
		return
	}

	ev := Event{
		ID:            eventID,
		ClientRef:     clientRef,
		SeenServerSeq: in.SeenServerSeq(),
		Name:          name,
		FragmentID:    fragmentID,
		At:            now,
		Fields:        copyFields(in.Fields()),
	}

	authorized, ok := a.authorize(ctx, ev)
	if !ok {
		return
	}

	m := getInbound()
	m.kind = msgEvent
	m.ev = ev
	m.span = authorized
	m.origin = protocol.Origin{
		Kind:      pb.OriginKind_CLIENT_EVENT,
		EventID:   ev.ID,
		ClientRef: ev.ClientRef,
		Source:    protocol.SourceEventPrefix + name,
	}
	if !a.post(m) {
		a.m.EventRejected(ctx, "mailbox_full")
		a.log.Warn(ctx, "gotth-live: the mailbox is full: the event was dropped rather than queued",
			obs.Str("session_id", a.idStr),
			obs.Str("event_name", name),
			obs.Int("mailbox_depth", a.lim.MailboxDepth))
		a.emitError(ctx, pb.ErrorCode_RATE_LIMITED,
			"the session is saturated: slow down and retry", ev.ID, ev.ClientRef, false)
		return
	}
	a.m.EventReceived(ctx, name)
}

// authorize runs the application's per-event hook and reports whether the
// event may proceed, along with a reference to the span it ran under.
//
// The reference travels with the event into the mailbox and is what the
// transition span descends from. Authorization happens on the read pump,
// before the event reaches the actor, so the transition cannot be a LEXICAL
// child of it — the context does not cross the goroutine boundary and this
// design will not put one on a mailbox message. The reference does cross, and
// an ended span is still a valid parent, so the edge is a real parent edge
// rather than the link it was until FR-36 clause 4: the difference is that a
// parent edge carries the sampling decision and a link does not, which is the
// whole of C-30 (obs.Tracer.StartChildOf).
//
// This span is itself a child of gotthlive.parse, which the read pump opened
// for the frame these bytes arrived in. So the sampler decides once, at the
// frame, and everything from here to the write inherits it.
func (a *Actor[I]) authorize(ctx context.Context, ev Event) (obs.SpanRef, bool) {
	var span obs.Span
	if a.tr.Enabled() {
		ctx, span = a.tr.Start(ctx, obs.SpanAuthorize,
			a.idAttr,
			attribute.Int64(obs.AttrEventID, int64(ev.ID)),
			attribute.String(obs.AttrEventName, ev.Name))
	}
	defer span.End()

	err := a.app.Authorize(ctx, a.peer, ev)
	if err == nil {
		return span.Ref(), true
	}
	span.RecordError(err)
	a.m.EventRejected(ctx, "unauthorized")

	var fatal *FatalDenyError
	if errors.As(err, &fatal) {
		a.log.Error(ctx, "gotth-live: an event was denied fatally: the connection is closing",
			obs.Str("session_id", a.idStr),
			obs.Str("subject", a.peer.Identity.Subject()),
			obs.Str("event_name", ev.Name),
			obs.U64("event_id", ev.ID))
		a.emitError(ctx, pb.ErrorCode_UNAUTHORIZED, "not permitted", ev.ID, ev.ClientRef, true)
		a.Close(protocol.CloseUnauthorized, "authorization denied fatally")
		return obs.SpanRef{}, false
	}

	a.log.Warn(ctx, "gotth-live: an event was denied: the connection stays open and no state changed",
		obs.Str("session_id", a.idStr),
		obs.Str("event_name", ev.Name),
		obs.U64("event_id", ev.ID))
	a.emitError(ctx, pb.ErrorCode_UNAUTHORIZED, "not permitted", ev.ID, ev.ClientRef, false)
	return obs.SpanRef{}, false
}

func (a *Actor[I]) ingressResync(ctx context.Context, in protocol.InboundResyncRequest, now time.Time) {
	a.eventSeq++
	ev := Event{
		ID:            a.eventSeq,
		ClientRef:     0,
		SeenServerSeq: in.LastAppliedSeq(),
		Name:          ResyncEventName,
		At:            now,
	}
	// A resync request carries no client_ref of its own on the wire, and the
	// causal identifiers on a resync snapshot must be set or clear together,
	// so the event identifier stands in for both.
	ev.ClientRef = ev.ID

	authorized, ok := a.authorize(ctx, ev)
	if !ok {
		return
	}

	m := getInbound()
	m.kind = msgResync
	m.ev = ev
	m.span = authorized
	m.lastAppliedSeq = in.LastAppliedSeq()
	m.origin = protocol.Origin{
		Kind:      pb.OriginKind_RESYNC,
		EventID:   ev.ID,
		ClientRef: ev.ClientRef,
		Source:    protocol.SourceResync,
	}
	if !a.post(m) {
		a.m.EventRejected(ctx, "mailbox_full")
		a.emitError(ctx, pb.ErrorCode_RATE_LIMITED,
			"the session is saturated: slow down and retry", ev.ID, ev.ClientRef, false)
	}
}

func (a *Actor[I]) ingressAck(ctx context.Context, in protocol.InboundAck) {
	select {
	case a.acks <- in.ServerSeq():
	default:
		// Dropping is lossless in the limit: an acknowledgement is a
		// cumulative high-water mark, so the next one supersedes this one and
		// the window re-opens a round trip later. Blocking here would stall
		// the read pump's own liveness handling, and an unbounded channel
		// would be a memory vector under a flood from an authenticated client.
		a.m.FrameRejected(ctx, protocol.ReasonAckChannelFull)
	}
}

func (a *Actor[I]) ingressTelemetry(ctx context.Context, in protocol.InboundClientTelemetry) {
	m := getInbound()
	m.kind = msgTelemetry
	m.patchID = in.PatchID()
	m.morphMicros = in.MorphMicros()
	m.applyMicros = in.ApplyMicros()
	if !a.post(m) {
		a.m.ClientTelemetryDropped(ctx, "mailbox_full")
	}
}

func copyFields(in []protocol.EventField) []Field {
	if len(in) == 0 {
		return nil
	}
	// The fields are copied into the session's domain type rather than retaining
	// the protocol snapshot slice.
	out := make([]Field, len(in))
	for i, f := range in {
		out[i] = Field{Key: f.Key, Value: f.Value}
	}
	return out
}

const unknownFragmentMetric = "unknown_fragment"

func (a *Actor[I]) rejectUnknownFragment(ctx context.Context, eventID, clientRef uint64) {
	a.m.EventRejected(ctx, unknownFragmentMetric)
	a.emitError(ctx, pb.ErrorCode_UNKNOWN_FRAGMENT,
		"the event names a fragment this session does not currently declare", eventID, clientRef, false)
}
