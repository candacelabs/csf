package session

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// Resync results, which are also the metric's label domain. The split matters:
// a no-op is the free short circuit when there was no gap, rate_limited is the
// independent bucket refusing to amplify, and only snapshot costs a full
// re-render.
const (
	resyncSnapshot    = "snapshot"
	resyncNoop        = "noop"
	resyncRateLimited = "rate_limited"
)

// resync answers a client's request to re-render everything.
//
// This is the most expensive operation the server performs and the only one a
// client can trigger directly, which is why it has a rate budget of its own
// rather than drawing on the ordinary event bucket. Fifty full-state renders a
// second from one authenticated client would be a self-service denial of
// service, and sharing a budget with ordinary events is what would have
// allowed it.
func (a *Actor[I]) resync(ctx context.Context, m *inbound) {
	// A child of the span authorization ran under, for FR-36 clause 4's reason
	// and by its mechanism. A resync is authorized as a distinguished event
	// kind on the read pump, so its origin span had exactly the defect C-30
	// measured on the event path one path over: two roots, two independent
	// sampling decisions, and a client-triggered full re-render whose
	// authorization and whose render were never recorded together.
	var span obs.Span
	if a.tr.Enabled() {
		ctx, span = a.tr.StartChildOf(ctx, obs.SpanOrigin, m.span,
			a.idAttr,
			attribute.String(obs.AttrOriginKind, pb.OriginKind_RESYNC.String()),
			attribute.String(obs.AttrOriginSource, protocol.SourceResync),
			attribute.Int64(obs.AttrEventID, int64(m.ev.ID)))
	}
	defer span.End()

	// BR-6: the budget is taken first, before the no-op short circuit.
	//
	// H-14 has two clauses — "ResyncRequest obeys its OWN rate budget" and "a
	// request whose last_applied_seq already equals server_seq is answered with
	// an Ack" — and the second used to take precedence over the first, which
	// left a whole frame kind charged to no bucket at all: not this one, and
	// not the event bucket either, because ingressResync does not consult it.
	// Each such request still minted an event identifier, ran the application's
	// Authorize hook on the read pump, occupied a mailbox slot and produced an
	// outbound Ack. H-14's own amplification spec passed vacuously across it,
	// because that spec counts renders and this path performs none.
	//
	// The clause about the Ack is about what a request COSTS the server, not
	// about whether it is charged for: an answer is still an answer.
	if !a.resyncBucket.allow(a.now()) {
		a.m.ResyncRequest(ctx, resyncRateLimited, 0)
		a.resyncDenied++
		a.log.Warn(ctx, "gotth-live: a resync request was refused by its own rate budget",
			obs.Str("session_id", a.idStr),
			obs.U64("event_id", m.ev.ID),
			obs.U64("last_applied_seq", m.lastAppliedSeq))
		a.emitError(ctx, pb.ErrorCode_RATE_LIMITED,
			"resync requests are rate limited: wait before requesting another",
			m.ev.ID, m.ev.ClientRef, false)
		if a.resyncDenied >= consecutiveDenialsBeforeClose*a.lim.ResyncBurst {
			a.Close(protocol.CloseRateLimited, "sustained resync flood")
		}
		return
	}
	a.resyncDenied = 0

	// BR-9: the client's claim is clamped against what this server already
	// knows, before anything is derived from it.
	//
	// last_applied_seq is untrusted input that nothing requires to be
	// non-decreasing. H-8's seen_server_seq check lives in transition, and a
	// resync routes to this function, so it never runs for this field; the only
	// guard was the no-op short circuit, which catches an OVER-stated value and
	// lets every under-stated one through. A client sending last_applied_seq: 1
	// forever therefore produced overlapping supersession ranges [2, S1],
	// [2, S2], … and P7's "contiguous and non-overlapping per session" failed
	// through no server fault. validateSnapshot cannot catch it: it is a
	// cross-frame property and validation is per frame.
	//
	// Two floors, and both are needed.
	//
	// w.acked is the client's OWN previously stated high-water mark, and H-7
	// already refuses an acknowledgement that goes backwards, so a request
	// claiming to have applied less than it acknowledged is a contradiction the
	// client is making with itself.
	//
	// lastSnapshotSeq is the sequence of the last snapshot this session
	// emitted, and it is what closes the case max(last_applied, acked) alone
	// does not. The reachable interleaving is a retry that outruns an
	// acknowledgement: a client with a latched gap asks, is refused
	// RATE_LIMITED, and re-arms; the first request is answered with a snapshot
	// at X; the retry arrives carrying the same latched cursor S before the
	// client has acknowledged X. Against the acked floor alone, acked is still
	// at or below S, so the second range is [S+1, T2] and overlaps the first's
	// [S+1, X-1]. Against this floor it is [X+1, T2], which begins exactly
	// where a client that applied the snapshot at X now stands. That matters
	// beyond the audit: the shipped client enforces H-13's range clauses in
	// applied() and closes 4002 on an overlap or a hole, so the arithmetic here
	// is what decides whether a correct client is evicted.
	//
	// Both clamp rather than refuse. A refusal would be a close code for a
	// client that has already been told, by H-7, that its acknowledgements are
	// cumulative, and the range the server emits is the true one either way.
	// The self-contradiction is counted and logged; a cursor merely older than
	// a snapshot already in flight is neither, because it is not a fault.
	applied := m.lastAppliedSeq
	if acked := a.win.ackedSeq(); acked > applied {
		a.m.EventRejected(ctx, "understated_last_applied")
		a.log.Warn(ctx, "gotth-live: a resync request claimed to have applied less than it had already acknowledged: the superseded range is clamped to what this session knows",
			obs.Str("session_id", a.idStr),
			obs.U64("last_applied_seq", m.lastAppliedSeq),
			obs.U64("acked_seq", acked))
		applied = acked
	}
	applied = max(applied, a.lastSnapshotSeq)

	// The no-op short circuit. A request whose last applied sequence already
	// equals what the server has emitted describes no gap, so the answer is an
	// acknowledgement rather than a snapshot, and the common spurious request
	// costs a token and nothing else.
	//
	// It reads the clamped value, which is also what keeps the range below
	// non-empty: a request that reaches the snapshot has applied strictly less
	// than server_seq, so from = applied + 1 <= server_seq = through, and
	// validateSnapshot's "the range is empty" arm stays unreachable.
	if applied >= a.serverSeq {
		a.m.ResyncRequest(ctx, resyncNoop, 0)
		if _, err := a.fr.Send(ctx, protocol.NewAck(a.peer.ID, a.serverSeq)); err != nil {
			// The sequence the answer would have named is on the record
			// (FR-58). Without it this line says a resync failed and nothing
			// about which gap the client is still holding, which is the only
			// question a reader has: the client re-asks from the same cursor,
			// so an operator wants to know whether it is the same one twice.
			a.log.Error(ctx, "gotth-live: could not answer a resync request: the client still believes it has a gap and will ask again",
				obs.Str("session_id", a.idStr),
				obs.U64("server_seq", a.serverSeq),
				obs.U64("last_applied_seq", m.lastAppliedSeq),
				obs.Err(err))
		}
		return
	}

	// The supersession edge: the inclusive range of sequence numbers this
	// snapshot replaces. Without it an analyst holding a capture cannot say
	// which events produced the markup the user is now looking at, because the
	// superseded patches were emitted and counted and then dropped.
	sup := protocol.Supersession{
		FromSeq:    applied + 1,
		ThroughSeq: a.serverSeq,
	}
	origin := protocol.Origin{
		Kind:      pb.OriginKind_RESYNC,
		EventID:   m.ev.ID,
		ClientRef: m.ev.ClientRef,
		Source:    protocol.SourceResync,
	}

	a.transitionID++
	// A resync snapshot that could not be sent is survivable in a way a mount's
	// is not: the connection already has a snapshot on it and a sequence the
	// client can reference, so the failure is one dropped answer rather than a
	// session that was never established. It is counted with the size it did
	// not send, which is zero.
	n, _ := a.emitSnapshot(ctx, origin, sup, m.ev.ID)
	a.m.ResyncRequest(ctx, resyncSnapshot, n)
}

// telemetry records a client's report about how long it took to apply a patch.
//
// Every field of it is untrusted input. The patch identifier must name a patch
// this session actually sent and that is still inside the window; anything
// else is either a forgery or a stale echo, and is dropped and counted rather
// than used to fabricate a span.
func (a *Actor[I]) telemetry(ctx context.Context, m *inbound) {
	sent, ok := a.win.slotFor(m.patchID)
	if !ok {
		a.m.ClientTelemetryDropped(ctx, "unknown_patch")
		a.log.Warn(ctx, "gotth-live: a client reported timing for a patch this session did not send",
			obs.Str("session_id", a.idStr), obs.U64("patch_id", m.patchID))
		return
	}

	a.m.ClientTiming(ctx, m.morphMicros, m.applyMicros)

	// Linked, and deliberately still a root: this is FR-36 clause 3's last
	// link site and clause 4's named second sampling decision. The duration is
	// client-measured and authoritative; the span's start is derived from it
	// (receive time minus the reported duration) and is explicitly
	// approximate, so a parent edge would assert an enclosure this design does
	// not observe. What independent sampling costs here is attribution and not
	// measurement — the same duration is an unsampled histogram.
	//
	// The context is deliberately the actor's rather than the read pump's
	// parse context: the derived start precedes the telemetry frame's arrival,
	// so descending from that frame's parse span would be the same invented
	// enclosure by another route.
	var span obs.Span
	if a.tr.Enabled() {
		_, span = a.tr.StartLinked(ctx, obs.SpanClientMorph, sent.span,
			a.idAttr,
			attribute.Int64(obs.AttrPatchID, int64(m.patchID)),
			attribute.Int64(obs.AttrServerSeq, int64(sent.serverSeq)),
			attribute.Int64(obs.AttrMorphMicros, int64(m.morphMicros)),
			attribute.Int64(obs.AttrApplyMicros, int64(m.applyMicros)),
			attribute.String(obs.AttrTimingSource, "client_reported"))
	}
	span.End()
}
