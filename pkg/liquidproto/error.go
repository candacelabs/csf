package liquidproto

import (
	"fmt"
)

// Error reports a value that failed a protobuf field refinement.
//
// Generated message validators return *Error so callers can inspect the exact
// contract violation with errors.As.
type Error struct {
	// Message is the fully-qualified protobuf message name.
	Message string
	// Field is the protobuf field name.
	Field string
	// Predicate is the source expression from the field option.
	Predicate string
	// Value is the rejected value in its base Go type.
	Value any
}

func (e *Error) Error() string {
	return fmt.Sprintf(
		"refinement violated for %s: value %s does not satisfy %q",
		e.Message+"."+e.Field,
		FormatValue(e.Value),
		e.Predicate,
	)
}

// FormatValue renders a rejected value without placing string or byte content
// in diagnostics. Error.Value retains the raw value for trusted callers using
// errors.As, while Error() remains safe to put in production logs.
func FormatValue(v any) string {
	switch value := v.(type) {
	case nil:
		return "<nil>"
	case string:
		return fmt.Sprintf("<redacted string: %d bytes>", len(value))
	case []byte:
		return fmt.Sprintf("<redacted bytes: %d bytes>", len(value))
	default:
		return fmt.Sprintf("%v", value)
	}
}
