package live

import "github.com/candacelabs/csf/pkg/gotth/internal/protocol"

// snapshotParam is one Limits field that leaves this process as a refined
// session parameter in the mount Snapshot.
type snapshotParam struct {
	// field is the Limits field an operator sets, for the error.
	field string
	// set is false when the field is zero and takes its default, which is
	// always in range and must stay acceptable.
	set bool
	// got is the value as the operator wrote it — a Duration reads as "500ms"
	// and not as the 0 whole milliseconds it narrows to.
	got string
	// wire is the value the Snapshot would carry, in the wire field's own
	// units, widened so that the narrowing cannot hide the violation.
	wire int64
	// rng is the interval the schema's refinement admits.
	rng protocol.SessionParamRange
	// unit renders a wire value in the field's units, so the range an operator
	// is told about is in the units they set the field in.
	unit func(wire int64) string
	// def is the documented default's wire value.
	def int64
}
