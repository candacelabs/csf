package routers

import (
	"fmt"
	"slices"

	"google.golang.org/protobuf/encoding/protowire"
)

// ---------------------------------------------------------------------------
// A frame reader, written from the schema rather than from the generated code
// ---------------------------------------------------------------------------
//
// The third assertion this suite makes per router is that a live SESSION runs
// through the prefix, not merely that a static file is reachable there. That
// means opening the WebSocket the rendered tag names and reading the frames
// that come back, and something has to decode them.
//
// The generated protobuf types live under gotth-live/internal/. This module's
// import path is lexically under gotth-live/, so Go's internal rule would let
// it import them. It does not, and that refusal is the point rather than an
// inconvenience: FR-33's subject is what an application OUTSIDE this library
// can do with it, and a consumer's module is not under that prefix. A suite
// that proved "the session works at /ui/gotth" using the library's own
// private codec would be proving a property of mounting with a tool no reader
// of it can pick up.
//
// So this is a reader over google.golang.org/protobuf/encoding/protowire — a
// public package — against the field numbers in
// gotth-live/proto/gotthlive/v1/frame.proto, which is the public artifact an
// operator holding a capture would read. It is a trimmed copy of
// examples/chat/wire_test.go's reader: same schema, same approach, and only
// the four messages this suite reads. Copying it rather than sharing it is
// what FRICTION.md F-1 costs until live/livetest grows the Client it
// documents; sharing would mean a fourth module existing only to be imported
// by two test modules, which is worse for the same reason.

// The Frame field numbers, from proto/gotthlive/v1/frame.proto.
const (
	fieldFrameProtocolVersion protowire.Number = 1
	fieldFrameSessionID       protowire.Number = 2
	fieldFrameEvent           protowire.Number = 3
	fieldFrameHeartbeat       protowire.Number = 5
	fieldFramePatch           protowire.Number = 8
	fieldFrameSnapshot        protowire.Number = 9
	fieldFrameError           protowire.Number = 10
)

// originClientEvent is the OriginKind a patch caused by a browser event
// carries, from the same file. Only the one this suite can produce is named;
// an unnamed value arriving fails with a number in it, which is more useful
// than a missing constant.
const originClientEvent = 1

type wireFrame struct {
	Kind      string // "patch", "snapshot", "error", "heartbeat", "other"
	SessionID []byte
	Patch     *wirePatch
	Error     *wireError
}

type wirePatch struct {
	ServerSeq uint64
	Origin    wireOrigin
	Updates   []wireUpdate
}

type wireOrigin struct {
	Kind   uint64
	Source string
}

type wireUpdate struct {
	FragmentID string
	HTML       string
}

type wireError struct {
	Code    uint64
	Message string
}

// fragment returns the markup this patch carries for one region, and whether
// it carried any at all.
func (p *wirePatch) fragment(id string) (string, bool) {
	for _, u := range p.Updates {
		if u.FragmentID == id {
			return u.HTML, true
		}
	}
	return "", false
}

// describe renders a frame for a failure message. A spec that times out
// waiting for a snapshot should say what did arrive.
func (f *wireFrame) describe() string {
	switch {
	case f == nil:
		return "no frame"
	case f.Error != nil:
		return fmt.Sprintf("error frame, code %d: %s", f.Error.Code, f.Error.Message)
	case f.Patch != nil:
		return fmt.Sprintf("%s frame, origin %q, fragments %v",
			f.Kind, f.Patch.Origin.Source, fragmentIDs(f.Patch))
	default:
		return f.Kind + " frame"
	}
}

func fragmentIDs(p *wirePatch) []string {
	out := make([]string, 0, len(p.Updates))
	for _, u := range p.Updates {
		out = append(out, u.FragmentID)
	}
	return out
}

// eachField walks one protobuf message, handing each field its number and
// either its varint value or its length-delimited bytes. Fixed-width fields
// are skipped: this schema has none.
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

func decodeFrame(b []byte) (*wireFrame, error) {
	f := &wireFrame{Kind: "other"}
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case fieldFrameProtocolVersion:
			if v != 1 {
				return fmt.Errorf("routers: a frame arrived with protocol version %d", v)
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
		}
		return nil
	})
	return f, err
}

// decodePatch reads a Patch or a Snapshot. Their first five fields are
// identical and their update list differs only in number — 6 on a Patch, 9 on
// a Snapshot — which is the whole reason one function reads both.
func decodePatch(b []byte, snapshot bool) (*wirePatch, error) {
	updates := protowire.Number(6)
	if snapshot {
		updates = 9
	}

	p := &wirePatch{}
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			p.ServerSeq = v
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
		}
		return nil
	})
	return p, err
}

func decodeOrigin(b []byte) (wireOrigin, error) {
	var o wireOrigin
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			o.Kind = v
		case 4:
			o.Source = string(bs)
		}
		return nil
	})
	return o, err
}

func decodeUpdate(b []byte) (wireUpdate, error) {
	var u wireUpdate
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			u.FragmentID = string(bs)
		case 3:
			u.HTML = string(bs)
		}
		return nil
	})
	return u, err
}

func decodeError(b []byte) (*wireError, error) {
	e := &wireError{}
	err := eachField(b, func(num protowire.Number, v uint64, bs []byte) error {
		switch num {
		case 1:
			e.Code = v
		case 2:
			e.Message = string(bs)
		}
		return nil
	})
	return e, err
}

// encodeEventFrame builds the frame the client runtime would send, from the
// same schema. The suite sends one so that "the session works through the
// prefix" is a round trip rather than a successful upgrade.
func encodeEventFrame(sessionID []byte, clientRef uint64, name, fragmentID string, seenSeq uint64) []byte {
	var ev []byte
	ev = protowire.AppendTag(ev, 1, protowire.VarintType)
	ev = protowire.AppendVarint(ev, clientRef)
	ev = protowire.AppendTag(ev, 2, protowire.BytesType)
	ev = protowire.AppendString(ev, name)
	ev = protowire.AppendTag(ev, 3, protowire.BytesType)
	ev = protowire.AppendString(ev, fragmentID)
	ev = protowire.AppendTag(ev, 4, protowire.VarintType)
	ev = protowire.AppendVarint(ev, seenSeq)

	var frame []byte
	frame = protowire.AppendTag(frame, fieldFrameProtocolVersion, protowire.VarintType)
	frame = protowire.AppendVarint(frame, 1)
	frame = protowire.AppendTag(frame, fieldFrameSessionID, protowire.BytesType)
	frame = protowire.AppendBytes(frame, sessionID)
	frame = protowire.AppendTag(frame, fieldFrameEvent, protowire.BytesType)
	frame = protowire.AppendBytes(frame, ev)
	return frame
}
