package protocol

import (
	"fmt"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// Rejection reasons. These are the label values of
// gotthlive_frames_rejected_total{reason}; the set is closed, and one
// enforcement site owns each value.
const (
	ReasonRefineFailed    = "refine_failed"
	ReasonOversize        = "oversize"
	ReasonUnknownKind     = "unknown_kind"
	ReasonBadVersion      = "bad_version"
	ReasonSessionMismatch = "session_mismatch"
	ReasonTextFrame       = "text_frame"
	ReasonEnumDomain      = "enum_domain"
	ReasonListBound       = "list_bound"
	ReasonAckChannelFull  = "ack_channel_full"
)

// RejectError reports a frame this build refuses to accept, and carries
// everything the caller needs to answer it without re-deriving anything: the
// metric label, the ErrorCode for the reply frame, and the close code when the
// violation is fatal to the connection.
//
// The message follows the library's error template — what failed, why, and
// what to do — because a rejection an operator cannot act on is a defect
// (FR-58).
type RejectError struct {
	// Reason is the gotthlive_frames_rejected_total label value.
	Reason string
	// Code is the ErrorCode carried back to the client.
	Code pb.ErrorCode
	// Close names the close code when this rejection ends the connection, and
	// is CloseNone when the connection survives.
	Close CloseCode
	// Detail is the operator-facing explanation. It never carries a token, a
	// cookie, an authorization input, application state, or a raw frame body.
	Detail string
	// Err is the underlying cause, typically a refinement violation.
	Err error
}

// Error renders the reason label, the operator-facing detail and the
// underlying cause in that order: the label is what a metric was incremented
// with, so a log line and a counter can be lined up without a lookup table.
func (e *RejectError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("gotth-live: rejected inbound frame (%s): %s: %v", e.Reason, e.Detail, e.Err)
	}
	return fmt.Sprintf("gotth-live: rejected inbound frame (%s): %s", e.Reason, e.Detail)
}

// Unwrap exposes the underlying refinement violation to errors.Is and
// errors.As, so a caller can inspect *refine.Error for the offending field.
func (e *RejectError) Unwrap() error { return e.Err }

// Fatal reports whether this rejection closes the connection.
func (e *RejectError) Fatal() bool { return e.Close != CloseNone }

func reject(reason string, code pb.ErrorCode, close CloseCode, err error, format string, args ...any) *RejectError {
	return &RejectError{
		Reason: reason,
		Code:   code,
		Close:  close,
		Detail: fmt.Sprintf(format, args...),
		Err:    err,
	}
}
