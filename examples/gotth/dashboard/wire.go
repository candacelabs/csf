package main

// A frame reader and writer, written from the schema rather than from the
// generated code.
//
// Three of FR-62's five properties are claims about what is on the wire — which
// fragments a patch carries, which events a coalesced patch names, how many
// patches a client that stops acknowledging can be sent — and an application
// cannot check any of them by looking at its own state. Something has to read
// the bytes. The resync measurement needs the same thing for a different
// reason: "bytes for a full resync" is a length, and a length of what the
// server sent is not a number the server's own state has.
//
// The generated protobuf types live under pkg/gotth/internal/. This example is
// not under that prefix — it sits at examples/gotth/dashboard, a sibling of
// pkg/ — so Go's internal rule refuses the import outright. It refused it by
// choice before the examples moved out of the library tree, when the rule would
// have permitted it, and the reason is the same either way: a consumer's module
// is not under that prefix and could never do it, so an example that proved
// these properties with the library's own private codec would be proving them
// with a tool no reader of the example can pick up. live/livetest.Client is the
// supported answer to that — the chat example's FRICTION.md item F-1 asked for
// it, this example's F-2 was the second application of the same tax — and it is
// built; what it cannot do is run outside a test, which is the paragraph below
// and the whole reason this file is still here.
//
// So this is a reader and a writer over google.golang.org/protobuf/encoding/protowire
// — a public package — against the field numbers in
// pkg/gotth/proto/gotthlive/v1/frame.proto, which is the same public artifact
// an operator holding a capture would read.
//
// It is in a NON-test file because MeasureResync uses it, and that is now the
// ONLY reason it survives.
//
// wire_test.go used to share it. It does not any more: live/livetest.Client is
// built, it is in the library's second exported package, and it satisfies the
// constraint above — a reader of this example can pick it up — so the specs
// dropped their driver and their decoder onto it. MeasureResync cannot follow
// them, and the reason is structural rather than temporary: livetest.Client
// takes a testing.TB first, deliberately, and MeasureResync runs from
// `go run . -resync-cost 200` in the example binary. Making it reachable would
// mean either linking testing into an example binary or fabricating a
// testing.TB in main, and the whole argument for a separate livetest package is
// that neither of those should be easy. So what is left below is exactly what
// one non-test measurement needs, and nothing that only a spec wanted.

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/encoding/protowire"
)

// Subprotocol is the WebSocket subprotocol the handshake requires, from
// docs/protocol.md §2. It is spelled out here because the library exports no
// constant for it: the client runtime sends it, the server checks it, and
// anything else that dials — a measurement, a spec, an operator's debugging
// client — has to know the string. FRICTION.md item F-2 records that.
const Subprotocol = "gotth-live.v1"

// The Frame field numbers, from proto/gotthlive/v1/frame.proto.
const (
	fieldFrameProtocolVersion protowire.Number = 1
	fieldFrameSessionID       protowire.Number = 2
	fieldFrameAck             protowire.Number = 4
	fieldFrameHeartbeat       protowire.Number = 5
	fieldFrameResyncRequest   protowire.Number = 7
	fieldFramePatch           protowire.Number = 8
	fieldFrameSnapshot        protowire.Number = 9
	fieldFrameError           protowire.Number = 10
)

// OriginKind values, from the same file. Only the ones a spec in this module
// asserts on are named; an unnamed value arriving is a failure with a number in
// it, which is more useful to read than a missing constant.
const (
	originClientEvent = 1
	originEffect      = 2
	originMount       = 5
	originResync      = 6
)

// ResyncReason values.
const (
	resyncReasonClientRequest = 3
)

// ErrorCode values, from the same file.
const (
	codeUnknownEvent = 4
	codeRateLimited  = 6
)

// WireFrame is one decoded frame.
type WireFrame struct {
	// Kind is "patch", "snapshot", "error", "heartbeat", "ack" or "other".
	Kind      string
	SessionID []byte
	// Bytes is the encoded length of the frame as it arrived, which is the
	// WebSocket message payload and therefore the bytes on the wire apart from
	// the 2-14 byte RFC 6455 frame header. Compression is disabled by the
	// library (internal/wsx sets CompressionDisabled), so this is not a
	// compressed length.
	Bytes int
	Patch *WirePatch
	Error *WireError
}

// WirePatch is a decoded Patch or Snapshot. Their first five fields are
// identical and their update list differs only in field number, which is why
// one type and one decoder read both.
type WirePatch struct {
	ServerSeq    uint64
	PatchID      uint64
	TransitionID uint64
	StateVersion uint64
	Origin       WireOrigin
	Updates      []WireUpdate
	// SupersededFrom and SupersededThrough are the resync supersession edge,
	// zero on a session's first snapshot.
	SupersededFrom    uint64
	SupersededThrough uint64
}

// WireOrigin is a decoded Origin.
type WireOrigin struct {
	Kind         uint64
	EventID      uint64
	ClientRef    uint64
	Source       string
	Contributing []uint64
}

// WireUpdate is one decoded FragmentUpdate.
type WireUpdate struct {
	FragmentID string
	Op         uint64
	HTML       string
}

// WireError is a decoded Error.
type WireError struct {
	Code      uint64
	Message   string
	EventID   uint64
	ClientRef uint64
	Fatal     bool
}

// Fragment returns the markup this patch carries for one region, and whether it
// carried any at all. "Did this patch touch the controls" is the question
// FR-62's independent-regions property is decided by, so it gets a method.
func (p *WirePatch) Fragment(id string) (string, bool) {
	for _, u := range p.Updates {
		if u.FragmentID == id {
			return u.HTML, true
		}
	}
	return "", false
}

// HTMLBytes returns the total rendered markup this frame carries, and the
// per-fragment split. The resync report quotes both: "a snapshot costs N bytes"
// is more useful beside "and here is which region spent them".
func (p *WirePatch) HTMLBytes() (int, map[string]int) {
	total := 0
	per := make(map[string]int, len(p.Updates))
	for _, u := range p.Updates {
		total += len(u.HTML)
		per[u.FragmentID] = len(u.HTML)
	}
	return total, per
}

// eachField walks one protobuf message, handing each field its number and
// either its varint value or its length-delimited bytes. Fixed-width fields are
// skipped: this schema has none.
func eachField(b []byte, fn func(num protowire.Number, v uint64, bs []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]

		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			if err := fn(num, v, nil); err != nil {
				return err
			}
			b = b[n:]
		case protowire.BytesType:
			bs, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			if err := fn(num, 0, bs); err != nil {
				return err
			}
			b = b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
		}
	}
	return nil
}

// DecodeFrame reads one frame off the wire.
func DecodeFrame(b []byte) (*WireFrame, error) {
	f := &WireFrame{Kind: "other", Bytes: len(b)}
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case fieldFrameProtocolVersion:
			if v != 1 {
				return fmt.Errorf("dashboard: a frame arrived with protocol version %d", v)
			}
		case fieldFrameSessionID:
			f.SessionID = slices.Clone(bs)
		case fieldFramePatch, fieldFrameSnapshot:
			p, err := decodePatch(bs, num == fieldFrameSnapshot)
			if err != nil {
				return err
			}
			f.Patch = p
			f.Kind = "patch"
			if num == fieldFrameSnapshot {
				f.Kind = "snapshot"
			}
		case fieldFrameError:
			e, err := decodeError(bs)
			if err != nil {
				return err
			}
			f.Error = e
			f.Kind = "error"
		case fieldFrameHeartbeat:
			f.Kind = "heartbeat"
		case fieldFrameAck:
			f.Kind = "ack"
		}
		return nil
	})
	return f, err
}

func decodePatch(b []byte, snapshot bool) (*WirePatch, error) {
	updates := protowire.Number(6)
	if snapshot {
		updates = 9
	}

	p := &WirePatch{}
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			p.ServerSeq = v
		case 2:
			p.PatchID = v
		case 3:
			p.TransitionID = v
		case 4:
			p.StateVersion = v
		case 5:
			o, err := decodeOrigin(bs)
			if err != nil {
				return err
			}
			p.Origin = o
		case updates:
			u, err := decodeUpdate(bs)
			if err != nil {
				return err
			}
			p.Updates = append(p.Updates, u)
		case 10:
			if snapshot {
				p.SupersededFrom = v
			}
		case 11:
			if snapshot {
				p.SupersededThrough = v
			}
		}
		return nil
	})
	return p, err
}

func decodeOrigin(b []byte) (WireOrigin, error) {
	var o WireOrigin
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			o.Kind = v
		case 2:
			o.EventID = v
		case 3:
			o.ClientRef = v
		case 4:
			o.Source = string(bs)
		case 5:
			// repeated uint64, packed by default in proto3.
			if bs == nil {
				o.Contributing = append(o.Contributing, v)
				return nil
			}
			for len(bs) > 0 {
				id, n := protowire.ConsumeVarint(bs)
				if n < 0 {
					return protowire.ParseError(n)
				}
				o.Contributing = append(o.Contributing, id)
				bs = bs[n:]
			}
		}
		return nil
	})
	return o, err
}

func decodeUpdate(b []byte) (WireUpdate, error) {
	var u WireUpdate
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			u.FragmentID = string(bs)
		case 2:
			u.Op = v
		case 3:
			u.HTML = string(bs)
		}
		return nil
	})
	return u, err
}

func decodeError(b []byte) (*WireError, error) {
	e := &WireError{}
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			e.Code = v
		case 2:
			e.Message = string(bs)
		case 3:
			e.EventID = v
		case 4:
			e.ClientRef = v
		case 5:
			e.Fatal = v != 0
		}
		return nil
	})
	return e, err
}

// EncodeAckFrame builds the acknowledgement the client runtime sends after it
// has applied a patch. Withholding it is how the backpressure spec makes a
// client slow without throttling a socket.
func EncodeAckFrame(sessionID []byte, serverSeq uint64) []byte {
	var ack []byte
	ack = protowire.AppendTag(ack, 1, protowire.VarintType)
	ack = protowire.AppendVarint(ack, serverSeq)
	return envelope(sessionID, fieldFrameAck, ack)
}

// EncodeResyncFrame builds a resync request. lastApplied is the highest
// sequence the client claims to hold; a value already equal to the server's is
// answered with an Ack rather than a Snapshot, which is the no-op short circuit
// the measurement deliberately avoids.
func EncodeResyncFrame(sessionID []byte, lastApplied uint64, reason uint64) []byte {
	var rq []byte
	rq = protowire.AppendTag(rq, 1, protowire.VarintType)
	rq = protowire.AppendVarint(rq, lastApplied)
	rq = protowire.AppendTag(rq, 2, protowire.VarintType)
	rq = protowire.AppendVarint(rq, reason)
	return envelope(sessionID, fieldFrameResyncRequest, rq)
}

func envelope(sessionID []byte, payload protowire.Number, body []byte) []byte {
	var frame []byte
	frame = protowire.AppendTag(frame, fieldFrameProtocolVersion, protowire.VarintType)
	frame = protowire.AppendVarint(frame, 1)
	frame = protowire.AppendTag(frame, fieldFrameSessionID, protowire.BytesType)
	frame = protowire.AppendBytes(frame, sessionID)
	frame = protowire.AppendTag(frame, payload, protowire.BytesType)
	frame = protowire.AppendBytes(frame, body)
	return frame
}
