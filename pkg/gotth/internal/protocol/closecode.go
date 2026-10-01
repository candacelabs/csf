package protocol

// CloseCode is a WebSocket close code from the library's private-range
// enumeration. Every close names one of these: a connection closed for an
// unenumerated reason is a defect, not a diagnostic gap, and an architecture
// test walks every close call site to hold that true.
type CloseCode int

// The enumeration. The numeric value is the wire code; Label is the metric
// label value, and dashboards, the audit, and this table therefore cannot
// drift apart.
const (
	// CloseNone is the zero value and is not a close code. It means "this
	// error does not close the connection".
	CloseNone CloseCode = 0

	CloseNormal             CloseCode = 4000 // client or server closed cleanly
	CloseGoingAway          CloseCode = 4001 // server shutting down or draining
	CloseProtocolViolation  CloseCode = 4002 // text frame, non-Frame bytes, H-3, H-7
	CloseUnsupportedVersion CloseCode = 4003 // H-2
	CloseUnauthenticated    CloseCode = 4004 // identity hook failed post-upgrade
	CloseForbiddenOrigin    CloseCode = 4005 // origin allowlist
	CloseUnauthorized       CloseCode = 4006 // authorization hook returned a fatal denial
	CloseFrameTooLarge      CloseCode = 4007 // H-5
	CloseRateLimited        CloseCode = 4008 // inbound limits, including the H-14 resync bucket
	CloseSlowClient         CloseCode = 4009 // outbound window exhausted
	CloseHeartbeatTimeout   CloseCode = 4010 // peer-dead detection
	CloseSessionEvicted     CloseCode = 4011 // idle timeout
	CloseInternalError      CloseCode = 4012 // contained panic that could not be recovered into the session
	CloseResyncFailed       CloseCode = 4013 // resync could not produce a consistent snapshot
)

// closeLabels is the one source for the code label of
// gotthlive_connections_closed_total. Never the numeric code and never the
// upper-case constant name.
var closeLabels = map[CloseCode]string{
	CloseNormal:             "normal",
	CloseGoingAway:          "going_away",
	CloseProtocolViolation:  "protocol_violation",
	CloseUnsupportedVersion: "unsupported_version",
	CloseUnauthenticated:    "unauthenticated",
	CloseForbiddenOrigin:    "forbidden_origin",
	CloseUnauthorized:       "unauthorized",
	CloseFrameTooLarge:      "frame_too_large",
	CloseRateLimited:        "rate_limited",
	CloseSlowClient:         "slow_client",
	CloseHeartbeatTimeout:   "heartbeat_timeout",
	CloseSessionEvicted:     "session_evicted",
	CloseInternalError:      "internal_error",
	CloseResyncFailed:       "resync_failed",
}

// Label returns the lower-case metric label value for c. An unenumerated code
// returns "unenumerated", which is a value no correct run can produce and is
// therefore an alarm rather than a silent hole.
func (c CloseCode) Label() string {
	if s, ok := closeLabels[c]; ok {
		return s
	}
	return "unenumerated"
}

// Valid reports whether c is a member of the enumeration.
func (c CloseCode) Valid() bool {
	_, ok := closeLabels[c]
	return ok
}

// CloseCodes returns the enumeration in ascending order. It exists so tests
// and the wire audit iterate the real table rather than a copy of it.
func CloseCodes() []CloseCode {
	return []CloseCode{
		CloseNormal, CloseGoingAway, CloseProtocolViolation, CloseUnsupportedVersion,
		CloseUnauthenticated, CloseForbiddenOrigin, CloseUnauthorized, CloseFrameTooLarge,
		CloseRateLimited, CloseSlowClient, CloseHeartbeatTimeout, CloseSessionEvicted,
		CloseInternalError, CloseResyncFailed,
	}
}
