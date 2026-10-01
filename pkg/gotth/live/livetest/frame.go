package livetest

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// FrameKind names the payload a frame carried.
//
// It is a string rather than the schema's oneof discriminator because a failure
// message is the main thing it is read in, and "snapshot" reads better in one
// than 9 does.
type FrameKind string

// The frame kinds. FrameOther is a payload this view does not model, which is
// a frame arriving that this package was not updated for rather than an error:
// a spec asserting on kinds should say so and fail, not be silently satisfied.
const (
	FramePatch     FrameKind = "patch"
	FrameSnapshot  FrameKind = "snapshot"
	FrameError     FrameKind = "error"
	FrameHeartbeat FrameKind = "heartbeat"
	FrameAck       FrameKind = "ack"
	FrameOther     FrameKind = "other"
)

// Frame is one decoded frame, as a plain value.
//
// The library's own generated protobuf types are what decoded it, and they stay
// on this side of the boundary deliberately: a spec that asserted on *pb.Frame
// would put the wire schema's generated Go into its import graph and make every
// regenerated field a compatibility event. api-surface.md §6 records that
// property; this type is how it is kept while still letting a spec see the wire.
type Frame struct {
	// Kind says which payload arrived, and therefore which of the fields
	// below is populated.
	Kind FrameKind

	// SessionID is the sixteen server-minted bytes every frame carries in both
	// directions. It is the same on every frame of one connection, which is
	// what makes a patch captured in isolation resolvable.
	SessionID []byte

	// Bytes is the encoded length of the frame as it arrived: the WebSocket
	// message payload, and therefore the bytes on the wire apart from the 2-14
	// byte RFC 6455 header. The library disables compression, so it is not a
	// compressed length. "What does a full resync cost" is a question only this
	// number answers — the server's own state does not hold it.
	Bytes int

	// Patch is set for both FramePatch and FrameSnapshot: their first five
	// fields are identical and only the update field number differs, so one
	// type reads both and Kind says which arrived.
	Patch *Patch

	// Error is set for FrameError.
	Error *Error

	// AckSeq is the sequence an Ack acknowledged, and is why a no-op resync is
	// distinguishable from a resync that produced nothing.
	AckSeq uint64
}

// Patch is a decoded Patch or Snapshot.
type Patch struct {
	// ServerSeq is the frame's position in this session's outbound order,
	// monotonic from 1. It is what an Ack acknowledges and what a gap in the
	// sequence is detected against, so a spec asserting "nothing was skipped"
	// asserts on this.
	ServerSeq uint64

	// PatchID names this emitted frame, one per Patch or Snapshot. It is the
	// identifier a client's apply-latency telemetry reports back.
	PatchID uint64

	// TransitionID names the reducer invocation this frame came out of, one
	// per invocation including a transition that changed nothing. Two patches
	// sharing a TransitionID were produced by one event.
	TransitionID uint64

	// StateVersion rises if and only if the transition changed state, so it
	// distinguishes a re-render from a state change. protocol.md §4.1.
	StateVersion uint64

	// Origin says what caused this frame — which is the whole provenance
	// question a wire capture is read to answer.
	Origin Origin

	// Updates is the rendered markup, one entry per region this frame carries.
	// A region absent here was not re-rendered, which is the independent-live-
	// regions property stated as data.
	Updates []Update

	// SupersededFrom and SupersededThrough are the resync supersession edge,
	// zero on a session's first snapshot. Without them nobody can say which
	// patches the markup a user is looking at replaced.
	SupersededFrom uint64

	// SupersededThrough is the inclusive upper end of that range: the last
	// server_seq this snapshot replaced. With SupersededFrom it is the only
	// record that the superseded patches ever existed, since they were emitted,
	// counted and then dropped.
	SupersededThrough uint64

	// The session parameters a snapshot carries, zero on a patch.
	HeartbeatIntervalMS uint32

	// MaxInboundFrameBytes is the largest frame the server will accept from
	// this client, announced once so the client needs no configuration of its
	// own. Zero on a patch.
	MaxInboundFrameBytes uint32

	// AckWindow is how many unacknowledged patches the server will hold before
	// it stops emitting, which is the backpressure threshold a slow-client
	// spec measures against. Zero on a patch.
	AckWindow uint32
}

// Origin is a decoded Origin: what caused this patch.
//
// Kind and the contributing identifiers are plain integers rather than the
// schema's generated enum, for the reason [Frame] gives. The values are in
// proto/gotthlive/v1/frame.proto, which is the artifact an operator holding a
// capture reads.
type Origin struct {
	// Kind is the category of cause: 1 a client event, 2 an effect, 3 a timer,
	// 4 a pubsub delivery, 5 the mount, 6 a resync. 0 means the frame named no
	// category, which the server never emits.
	Kind int32

	// EventID is the server-minted identity of the event that caused this
	// patch, zero when the server started the transition itself. It is the
	// authoritative causal root: unlike ClientRef, no client can name it.
	EventID uint64

	// ClientRef is the client's own correlation handle for the event, echoed
	// back unchanged. It is the one value in this struct that untrusted input
	// chose, and it exists so a browser can match a patch to the interaction
	// it sent before any server identifier reaches it.
	ClientRef uint64

	// Source is the human-readable cause, such as "event:counter.increment" or
	// "timer:slow_client". It is the field a failure message quotes.
	Source string

	// Contributing lists the events whose state changes this patch carries but
	// which were not individually patched, because coalescing collapsed them.
	// Empty is the ordinary case; a non-empty list is the evidence that
	// coalescing kept its provenance rather than discarding it.
	Contributing []uint64
}

// Update is one decoded FragmentUpdate.
type Update struct {
	// FragmentID is the live region this markup belongs to — the ID the
	// application gave the fragment, and the value of its data-gotth-region
	// attribute.
	FragmentID string

	// HTML is the region's complete rendered markup. There is no server-side
	// diff: the diff happens in the browser, against the live DOM.
	HTML string
}

// Error is a decoded Error frame.
type Error struct {
	// Code is the machine-readable classification, from frame.proto's
	// ErrorCode: 1 unsupported version, 2 unauthorized, and so on. It is what
	// a spec should assert on, because Message is deliberately uninformative
	// outside dev.
	Code int32

	// Message is generic in production and detailed when Config.Dev is set.
	// Asserting on its text is asserting on which mode the server was in.
	Message string

	// EventID names the event that caused the failure, zero when the server
	// started the transition itself.
	EventID uint64

	// ClientRef echoes the client's handle for that event, so a browser can
	// mark the right pending interaction as failed.
	ClientRef uint64

	// Fatal says the server is closing the connection after this frame. A
	// non-fatal error leaves the session running with its state unchanged.
	Fatal bool
}

// Fragment returns the markup this patch carries for one region, and whether it
// carried any at all.
//
// "Did this patch touch the controls" is the question the independent-regions
// property is decided by, and the two-value form is why it can be asked: a
// patch carrying an empty region and a patch carrying no region are different
// answers.
func (p *Patch) Fragment(id string) (string, bool) {
	for _, u := range p.Updates {
		if u.FragmentID == id {
			return u.HTML, true
		}
	}
	return "", false
}

// FragmentIDs returns the regions this patch carries, in wire order.
func (p *Patch) FragmentIDs() []string {
	out := make([]string, 0, len(p.Updates))
	for _, u := range p.Updates {
		out = append(out, u.FragmentID)
	}
	return out
}

// HTMLBytes returns the total rendered markup this patch carries and the
// per-fragment split. "A snapshot costs N bytes" is more useful beside "and
// here is which region spent them".
func (p *Patch) HTMLBytes() (int, map[string]int) {
	total := 0
	per := make(map[string]int, len(p.Updates))
	for _, u := range p.Updates {
		total += len(u.HTML)
		per[u.FragmentID] = len(u.HTML)
	}
	return total, per
}

// String renders a frame the way a failure message wants to read it. It is the
// reason Await can say what it saw instead.
func (f *Frame) String() string {
	switch f.Kind {
	case FramePatch, FrameSnapshot:
		return fmt.Sprintf("%s{seq=%d origin=%s/%d fragments=%v contributing=%v}",
			f.Kind, f.Patch.ServerSeq, f.Patch.Origin.Source, f.Patch.Origin.Kind,
			f.Patch.FragmentIDs(), f.Patch.Origin.Contributing)
	case FrameError:
		return fmt.Sprintf("error{code=%d fatal=%t message=%q}", f.Error.Code, f.Error.Fatal, f.Error.Message)
	case FrameAck:
		return fmt.Sprintf("ack{seq=%d}", f.AckSeq)
	}
	return string(f.Kind)
}

// decodeFrame reads one frame off the wire with the library's own generated
// types and projects it onto the view above.
//
// The projection is the whole value of doing it here: three suites and a
// mounting test each wrote a protowire decoder against frame.proto's field
// numbers because the generated types are internal/, and five copies of a
// protocol decoder are five places for the wire format to be understood
// slightly wrong.
func decodeFrame(b []byte) (*Frame, error) {
	var msg pb.Frame
	if err := proto.Unmarshal(b, &msg); err != nil {
		return nil, fmt.Errorf("decoding a frame of %d bytes: %w: the bytes are not an encoded "+
			"gotthlive.v1.Frame, so either something other than this library wrote to the socket "+
			"or a spec called WriteRaw and the server answered in kind", len(b), err)
	}
	if v := msg.GetProtocolVersion(); v != 1 {
		return nil, fmt.Errorf("a frame arrived with protocol version %d and this package speaks 1: "+
			"livetest is compiled from the same module as the server under test, so a mismatch here "+
			"means the frame came from somewhere else", v)
	}

	f := &Frame{Kind: FrameOther, SessionID: msg.GetSessionId(), Bytes: len(b)}
	switch {
	case msg.GetPatch() != nil:
		p := msg.GetPatch()
		f.Kind = FramePatch
		f.Patch = &Patch{
			ServerSeq:    p.GetServerSeq(),
			PatchID:      p.GetPatchId(),
			TransitionID: p.GetTransitionId(),
			StateVersion: p.GetStateVersion(),
			Origin:       decodeOrigin(p.GetOrigin()),
			Updates:      decodeUpdates(p.GetUpdates()),
		}
	case msg.GetSnapshot() != nil:
		s := msg.GetSnapshot()
		f.Kind = FrameSnapshot
		f.Patch = &Patch{
			ServerSeq:            s.GetServerSeq(),
			PatchID:              s.GetPatchId(),
			TransitionID:         s.GetTransitionId(),
			StateVersion:         s.GetStateVersion(),
			Origin:               decodeOrigin(s.GetOrigin()),
			Updates:              decodeUpdates(s.GetUpdates()),
			SupersededFrom:       s.GetSupersededFromSeq(),
			SupersededThrough:    s.GetSupersededThroughSeq(),
			HeartbeatIntervalMS:  s.GetHeartbeatIntervalMs(),
			MaxInboundFrameBytes: s.GetMaxInboundFrameBytes(),
			AckWindow:            s.GetAckWindow(),
		}
	case msg.GetError() != nil:
		e := msg.GetError()
		f.Kind = FrameError
		f.Error = &Error{
			Code:      int32(e.GetCode()),
			Message:   e.GetMessage(),
			EventID:   e.GetEventId(),
			ClientRef: e.GetClientRef(),
			Fatal:     e.GetFatal(),
		}
	case msg.GetHeartbeat() != nil:
		f.Kind = FrameHeartbeat
	case msg.GetAck() != nil:
		f.Kind = FrameAck
		f.AckSeq = msg.GetAck().GetServerSeq()
	}
	return f, nil
}

func decodeOrigin(o *pb.Origin) Origin {
	return Origin{
		Kind:         int32(o.GetKind()),
		EventID:      o.GetEventId(),
		ClientRef:    o.GetClientRef(),
		Source:       o.GetSource(),
		Contributing: o.GetContributingEventIds(),
	}
}

func decodeUpdates(in []*pb.FragmentUpdate) []Update {
	out := make([]Update, 0, len(in))
	for _, u := range in {
		out = append(out, Update{FragmentID: u.GetFragmentId(), HTML: u.GetHtml()})
	}
	return out
}
