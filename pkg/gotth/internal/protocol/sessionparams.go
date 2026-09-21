package protocol

// SessionParamRange is the closed interval one Snapshot session parameter's
// refinement admits.
//
// The predicates are declared on gotthlive.v1.Snapshot in
// proto/gotthlive/v1/frame.proto (protocol.md §3.3) and compiled to native Go
// by protoc-gen-liquidproto, which emits a message validator and no constant
// for either endpoint. So a caller that wants to reject an out-of-range value
// at construction, rather than discover it when the frame it built is refused
// on the write path, has a validator to ask and no number to quote back to the
// operator. These are that number, in the one place this side of the wire
// names it.
//
// They are not a second opinion about the range. The generated validator stays
// the only thing that decides a frame, and sessionparams_test.go holds every
// range below against the production outbound boundary that invokes it:
// accepted at Min and at Max, refused one past either end, with Field and
// Predicate compared verbatim against what the generator compiled. A predicate
// that moves in the .proto and is regenerated turns those specs red rather than
// leaving this file quietly wrong.
type SessionParamRange struct {
	// Field is the Snapshot field the predicate is declared on.
	Field string
	// Predicate is the predicate's source text, verbatim from the .proto. It
	// is carried so that a rejection can say where the range comes from
	// instead of asserting one on its own authority.
	Predicate string
	// Min and Max are the interval's inclusive endpoints.
	Min, Max uint32
}

// Contains reports whether v is within the range.
//
// It takes an int64 rather than a uint32 because every caller is narrowing
// from something wider — a time.Duration counted in milliseconds, an int — and
// narrowing first is how a value far outside the range becomes one inside it:
// 4294987296 ms is 49 days, and is 20 seconds once truncated to uint32.
func (r SessionParamRange) Contains(v int64) bool {
	return v >= int64(r.Min) && v <= int64(r.Max)
}

// The three session parameters a Snapshot carries, which is what makes them
// the fields an operator can set and a session can then fail to encode: they
// are the only configuration that leaves this process as refined wire values
// (D-23).
var (
	// HeartbeatIntervalMSRange refines Snapshot.heartbeat_interval_ms.
	HeartbeatIntervalMSRange = SessionParamRange{
		Field:     "heartbeat_interval_ms",
		Predicate: "this >= 1000 && this <= 300000",
		Min:       1000,
		Max:       300000,
	}
	// MaxInboundFrameBytesRange refines Snapshot.max_inbound_frame_bytes.
	MaxInboundFrameBytesRange = SessionParamRange{
		Field:     "max_inbound_frame_bytes",
		Predicate: "this >= 1024 && this <= 1048576",
		Min:       1024,
		Max:       1048576,
	}
	// AckWindowRange refines Snapshot.ack_window.
	AckWindowRange = SessionParamRange{
		Field:     "ack_window",
		Predicate: "this >= 1 && this <= 256",
		Min:       1,
		Max:       256,
	}
)
