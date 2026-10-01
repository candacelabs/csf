package protocol

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// ValidateOutbound re-checks a constructed frame against every predicate in
// the schema, plus the cross-field invariants, through the generated Liquid
// Proto validation boundary. The framer calls it immediately before
// marshalling, on the single write path, and it is not optional.
//
// Inbound frames are protected because they are parsed. Outbound frames are
// constructed, and Go cannot forbid the zero value of an opaque type, so
// without this step a struct literal assembled anywhere in the emit path would
// produce an orphan patch that nothing catches — the client codec does not
// enforce the pattern predicates, so neither does an independent decode of the
// capture. Re-checking is what turns "no patch without an origin" from a
// discipline into a property.
func ValidateOutbound(f *pb.Frame) error {
	if f == nil {
		return fmt.Errorf("gotth-live: outbound frame is nil: construct a frame before sending it")
	}
	if err := pb.ValidateFrame(f); err != nil {
		return err
	}
	if _, err := checkFieldInvariants(f.ProtoReflect()); err != nil {
		return err
	}

	switch p := f.GetPayload().(type) {
	case *pb.Frame_Patch:
		if err := pb.ValidatePatch(p.Patch); err != nil {
			return err
		}
		if err := validateOriginAndUpdates(p.Patch.GetOrigin(), p.Patch.GetUpdates()); err != nil {
			return err
		}

	case *pb.Frame_Snapshot:
		if err := pb.ValidateSnapshot(p.Snapshot); err != nil {
			return err
		}
		if err := validateOriginAndUpdates(p.Snapshot.GetOrigin(), p.Snapshot.GetUpdates()); err != nil {
			return err
		}
		if err := validateSnapshot(p.Snapshot); err != nil {
			return err
		}

	case *pb.Frame_Error:
		if err := pb.ValidateError(p.Error); err != nil {
			return err
		}
		if err := validateError(p.Error); err != nil {
			return err
		}

	case *pb.Frame_Heartbeat:
		if err := pb.ValidateHeartbeat(p.Heartbeat); err != nil {
			return err
		}

	case *pb.Frame_Ack:
		// The server sends an Ack in exactly one situation: answering a resync
		// request that describes no gap, where a snapshot would be waste.
		if err := pb.ValidateAck(p.Ack); err != nil {
			return err
		}

	case nil:
		return fmt.Errorf("gotth-live: outbound frame carries no payload: set exactly one member of the payload oneof")

	default:
		return fmt.Errorf("gotth-live: outbound frame carries payload kind %q, which the server never sends: "+
			"this frame was built by the server, so it is a library bug — report it with the kind above; "+
			"a client cannot cause this", KindOf(f))
	}
	return nil
}

func validateOriginAndUpdates(o *pb.Origin, updates []*pb.FragmentUpdate) error {
	if err := pb.ValidateOrigin(o); err != nil {
		return err
	}
	if err := validateOrigin(o); err != nil {
		return err
	}
	for _, u := range updates {
		if err := pb.ValidateFragmentUpdate(u); err != nil {
			return err
		}
	}
	return nil
}

// WriteFunc puts one already-encoded frame on the wire. It is a function value
// rather than an interface because there is one implementation and a
// one-implementation interface buys nothing; it is what keeps the core
// packages from naming the transport at all.
type WriteFunc func(ctx context.Context, b []byte) error

// Framer is the single write path. Nothing reaches the socket except through
// it, which is what makes the emitted-frame counter checkable against a wire
// capture: any drift means a second write path exists.
//
// It serializes writes with a mutex. That is not the actor model leaking: the
// socket is shared transport infrastructure rather than session state, and it
// has two legitimate writers. The actor writes every patch, snapshot and
// heartbeat; the read pump writes the error frame for an inbound frame it
// refuses before that frame ever reaches the mailbox — which it must be able
// to do precisely when the mailbox is full, since that is what the rejection
// is about.
type Framer struct {
	mu    sync.Mutex
	write WriteFunc

	// OnSent is called after a frame reaches the transport, with the encoded
	// size. It is the only incrementer of the frames-sent counter.
	OnSent func(kind Kind, bytes int)
	// OnInvalid is called when a constructed frame fails ValidateOutbound.
	// The frame was built on this side, so it is never a client problem, and
	// any occurrence is actionable. It is not necessarily a coding bug: some
	// of what a frame carries comes from the application, and until D-18 an
	// unbounded Event.Contributing arrived here as one.
	OnInvalid func(kind Kind, err error)
}

// NewFramer returns a framer writing through w.
func NewFramer(w WriteFunc) *Framer {
	return &Framer{write: w}
}

// InvalidFrameError reports a frame this library constructed and could not
// validate. It is separated from a transport failure because the two demand
// different responses: a transport failure ends the connection, while an
// invalid frame is dropped and replaced by an Error carrying the same causal
// chain, leaving the sequence contiguous.
type InvalidFrameError struct {
	// Kind is the payload the refused frame carried, which is what a log line
	// needs to say which emit path built it.
	Kind Kind

	// Err is the validation failure, typically a refinement violation naming
	// the offending field.
	Err error
}

// Error says explicitly that the server built this frame, because the first
// question asked of a validation failure is whose input caused it and here the
// answer is never the client's.
func (e *InvalidFrameError) Error() string {
	return fmt.Sprintf("gotth-live: refusing to send an invalid %s frame: %v: this frame was built by the server, so it is not a client problem", e.Kind, e.Err)
}

// Unwrap exposes the validation failure to errors.Is and errors.As, so a
// caller can reach the *liquidproto.Error and name the field.
func (e *InvalidFrameError) Unwrap() error { return e.Err }

// Encoded is one frame that has passed ValidateOutbound and been marshalled:
// the only thing Write accepts, and the only thing this package will put on a
// socket.
//
// Its fields are unexported and it has no constructor, so Framer.Encode is the
// only way to obtain a non-zero one and Write's argument cannot be assembled
// by hand. That is U-5's point. B-9 — every socket write in the module goes
// through Encode, which calls ValidateOutbound and refuses on failure — was
// true by grep and stayed true by nobody wanting to break it; Write took
// pre-encoded bytes and a Kind, so a future caller could marshal a frame itself
// and hand them over, and the coupling between "validated" and "written" was a
// convention rather than an invariant. A token only Encode can mint makes the
// bypass unconstructable instead of merely unobserved.
//
// The split it preserves is not decorative: instrumentation §2.3 defines
// gotthlive_send_duration_seconds as "time in Conn.Write, the write-stall
// signal", and while validate, marshal and write were one call that histogram
// and gotthlive_encode_duration_seconds were equal by construction and neither
// could isolate a stalling client. Collapsing Write back into Send to unexport
// it would re-create exactly that (L9 ruling on U-5).
type Encoded struct {
	kind Kind
	b    []byte
}

// Kind reports which payload the encoded frame carries.
func (e Encoded) Kind() Kind { return e.kind }

// Len is the encoded size in bytes, which is what the frame-size attributes and
// the byte counters record.
func (e Encoded) Len() int { return len(e.b) }

// Send validates, encodes and writes one frame, returning the number of bytes
// written. A frame that fails validation is never written and is reported as
// *InvalidFrameError.
func (f *Framer) Send(ctx context.Context, frame *pb.Frame) (int, error) {
	enc, err := f.Encode(frame)
	if err != nil {
		return 0, err
	}
	return f.Write(ctx, enc)
}

// Encode validates and marshals one frame without touching the socket.
//
// It is separated from Write because the two are different work with different
// failure modes, and until FR-36's gotthlive.send span was implemented nothing
// in this library could tell them apart. The consequence was not only a
// missing span: instrumentation §2.3 defines
// gotthlive_send_duration_seconds as "time in Conn.Write, the write-stall
// signal", and with one combined call site the actor recorded the same
// interval — validate plus marshal plus write — into both that histogram and
// gotthlive_encode_duration_seconds. Two series that are equal by construction
// cannot detect the stall one of them is named for.
func (f *Framer) Encode(frame *pb.Frame) (Encoded, error) {
	kind := KindOf(frame)
	if err := ValidateOutbound(frame); err != nil {
		if f.OnInvalid != nil {
			f.OnInvalid(kind, err)
		}
		return Encoded{}, &InvalidFrameError{Kind: kind, Err: err}
	}
	b, err := proto.Marshal(frame)
	if err != nil {
		if f.OnInvalid != nil {
			f.OnInvalid(kind, err)
		}
		return Encoded{}, &InvalidFrameError{Kind: kind, Err: err}
	}
	return Encoded{kind: kind, b: b}, nil
}

// Write puts one encoded frame on the wire under the framer's mutex.
//
// It stays a method on the framer rather than becoming a second write path:
// the serialization and the sent-frame counter are here, which is what makes
// the counter checkable against a wire capture at all (protocol.md P8).
//
// It takes an Encoded rather than bytes and a Kind, so the only thing reachable
// here is something Encode validated. The zero value is the one Encoded a
// caller outside this package can name, and it is refused: an empty payload is
// not a frame, and accepting it would leave exactly the hole the type closes.
func (f *Framer) Write(ctx context.Context, e Encoded) (int, error) {
	if len(e.b) == 0 {
		return 0, &InvalidFrameError{
			Kind: e.kind,
			Err:  fmt.Errorf("gotth-live: an encoded frame with no bytes: obtain one from Framer.Encode rather than constructing it"),
		}
	}
	f.mu.Lock()
	err := f.write(ctx, e.b)
	f.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if f.OnSent != nil {
		f.OnSent(e.kind, len(e.b))
	}
	return len(e.b), nil
}
