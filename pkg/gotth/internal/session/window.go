package session

import (
	"fmt"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
)

// slot is one unacknowledged patch, in two halves: the acknowledgement
// metadata, and the compact reference to the span that encoded it.
//
// The second half is what closes the trace loop to the browser, so it is not
// optional and the honest per-slot cost is both halves together.
type slot struct {
	serverSeq uint64
	patchID   uint64
	span      obs.SpanRef
}

// window is the bounded set of patches in flight, plus the most recent
// acknowledged ones.
//
// It retains metadata, never frame bytes. Retaining bytes for replay would
// cost the entire per-connection memory budget, and there would be nothing to
// replay into: a session does not outlive its connection, so a reconnect gets
// a fresh actor and a snapshot regardless. Dropping replay collapses two
// recovery paths into the one that is exercised on every reconnect and every
// deploy.
//
// # Why the ring outlives the acknowledgement
//
// An acknowledgement moves acked and nothing else. It used to also evict every
// slot at or below the acknowledged sequence, and that made H-11 unsatisfiable
// for the client this library ships: applied() sends the ack and then the
// telemetry report for the SAME patch, the two arrive on different channels,
// Run's select imposes no order between them, and an idle actor drains the ack
// first essentially every time. slotFor then missed and every legitimate report
// was counted as a forgery — 100 % of them, with a Warn record accusing the
// client per patch. Reordering the two client sends does not fix it, because
// the select is still unordered.
//
// So eviction is by age rather than by acknowledgement, and the retention bound
// is the one push already enforced: the newest retentionSlots patches, acked or
// not. H-11 keeps its meaning — an identifier that names no patch this session
// sent still misses, and one older than the bound misses and is counted as
// stale — while a report the client sent about a patch it just acknowledged has
// somewhere to land.
type window struct {
	slots []slot
	cap   int

	// acked is the highest contiguous sequence the client has applied, and
	// highest is the highest the server has emitted. Their difference is the
	// depth, which is the detection signal the slow-client stages read.
	acked   uint64
	highest uint64

	fullSince time.Time
}

func newWindow(capacity int) *window {
	return &window{slots: make([]slot, 0, capacity+1), cap: capacity}
}

// retentionSlots is how many emitted patches the ring remembers, and so the
// bound on both the memory it costs and how far back a client telemetry report
// may name a patch.
//
// It is AckWindow + 1 because that is what push already allowed: cap patches
// may be in flight, and a provenance flush is allowed to push exactly one past
// the bound rather than lose a coalesced union. Nothing here widens it — before
// the eviction change above, a healthy client's ring simply sat near empty and
// the same allocation was there to be used.
func (w *window) retentionSlots() int { return w.cap + 1 }

// ackedSeq is the highest sequence the client has told this server it applied.
//
// It is server-held knowledge about the client, which is what makes it usable
// as a floor on the client's own claims: a resync request understating
// last_applied_seq is contradicting an acknowledgement it already sent, and
// H-7 has already refused an acknowledgement that goes backwards.
func (w *window) ackedSeq() uint64 { return w.acked }

func (w *window) depth() int { return int(w.highest - w.acked) }

func (w *window) full() bool { return w.depth() >= w.cap }

// coalescing reports whether the window has reached the stage at which
// patches are collapsed rather than emitted one per transition.
func (w *window) coalescing() bool { return w.depth() >= w.cap/2 }

// push records an emitted patch. The caller has already confirmed there is
// room, or has deliberately flushed past the bound to keep a coalesced
// patch's provenance intact.
func (w *window) push(s slot) {
	w.highest = s.serverSeq
	w.slots = append(w.slots, s)
	// The one eviction rule: oldest first, past the retention bound. It is what
	// bounds the ring's memory, and after the acknowledgement stopped evicting
	// it is also what bounds how stale a telemetry report may be.
	for len(w.slots) > w.retentionSlots() {
		w.slots = w.slots[1:]
	}
}

// ack applies a client acknowledgement. It moves the high-water mark and
// evicts nothing: see the type's comment for why eviction is by age.
//
// H-7: the sequence must not exceed what the server has emitted and must not
// go backwards. A client that acknowledges a patch that was never sent is not
// confused, so the violation closes the connection rather than being ignored.
func (w *window) ack(seq uint64) error {
	if seq > w.highest {
		return fmt.Errorf(
			"gotth-live: acknowledged sequence %d was never emitted (highest is %d): "+
				"acknowledge only patches this session sent", seq, w.highest)
	}
	if seq < w.acked {
		return fmt.Errorf(
			"gotth-live: acknowledged sequence %d is below the high-water mark %d: "+
				"an acknowledgement is cumulative and never goes backwards", seq, w.acked)
	}
	w.acked = seq
	return nil
}

// slotFor returns the retained patch a client's report names.
//
// It searches the whole ring — the unacknowledged patches and the
// acknowledged ones still inside retentionSlots — because a report about a
// patch always arrives at or after the acknowledgement of it.
//
// A patch identifier that is not here is either forged or older than the
// retention bound, and either way the report naming it is dropped and counted
// rather than used to fabricate a span. The whole slot is returned rather than
// only its span reference because the sequence is in it, and instrumentation
// §3.2 requires gotthlive.server_seq on every span from encode onward — a rule
// the morph span was measured breaking at C-29 and which was left for whoever
// implemented the missing spans.
func (w *window) slotFor(patchID uint64) (slot, bool) {
	for i := range w.slots {
		if w.slots[i].patchID == patchID {
			return w.slots[i], true
		}
	}
	return slot{}, false
}

// noteFullness tracks how long the window has been continuously full, which
// is the eviction signal. It returns the moment fullness began, zero when the
// window is not full.
func (w *window) noteFullness(now time.Time) time.Time {
	if !w.full() {
		w.fullSince = time.Time{}
		return time.Time{}
	}
	if w.fullSince.IsZero() {
		w.fullSince = now
	}
	return w.fullSince
}

// trackedBytes is the exactly-sized cost of a window, for the per-session
// memory gauge. Forty-eight bytes per slot: sixteen of acknowledgement
// metadata — the sequence and the patch identifier — and a thirty-two byte
// span reference.
//
// It counts retentionSlots rather than cap, which is one slot more than this
// figure used to report. That slot was always allocatable — push has always
// been allowed to overshoot by one for a provenance flush — so the figure was
// understating a cost the library could already pay; what changed is that the
// ring now holds it in steady state rather than only under a flush.
func (w *window) trackedBytes() int64 { return int64(w.retentionSlots()) * 48 }
