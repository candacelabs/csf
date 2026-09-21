package widget

import "strconv"

// The three readers that turn one wire value into one state field.
//
// They live here rather than in each generated widget because they are the
// SDK's own semantics rather than any widget's: what a flag, a counter and a
// count mean is fixed by the dialect's four state types, and a widget that
// decided for itself what an unreadable value meant would be a widget whose
// behaviour under a hostile or buggy sender differed from its neighbour's.
//
// All three share one rule: a value that cannot be read leaves the field where
// it was. A reducer is pure and has nowhere to report a parse failure — it may
// not log, and inventing an event for it would put a wire fault into a state
// machine the author wrote for their own domain — so the honest outcome is the
// state the widget already had, which is the last value that was actually true.
// The field's writer will send another.

// ParseFlag reads a flag, keeping fallback when the value is not a boolean.
//
// The spellings are strconv.ParseBool's, which is what an HTML form and every
// serializer this library speaks produce: "true"/"false", "1"/"0", "t"/"f",
// and their capitalised forms.
func ParseFlag(raw string, fallback bool) bool {
	parsed, parseError := strconv.ParseBool(raw)
	if parseError != nil {
		return fallback
	}
	return parsed
}

// ParseCounter reads a counter, keeping fallback when the value is not a
// non-negative integer OR when it is lower than the value already held.
//
// The second half is the type's own meaning rather than defensive coding: a
// counter is monotonic, so a delivery carrying a lower value is a delivery that
// arrived late, and applying it would walk the counter backwards and re-arm
// every animation gated on it. Out-of-order delivery repairs itself this way;
// a counter that accepted the older number would show the wrong total until the
// next message and would have looked right the whole time.
func ParseCounter(raw string, fallback uint64) uint64 {
	parsed, parseError := strconv.ParseUint(raw, 10, 64)
	if parseError != nil || parsed < fallback {
		return fallback
	}
	return parsed
}

// ParseCount reads a count, keeping fallback when the value is not an integer.
//
// A count is an ordinary number and is not monotonic: it goes down as readily
// as up, so unlike a counter it takes whatever the wire said.
func ParseCount(raw string, fallback int64) int64 {
	parsed, parseError := strconv.ParseInt(raw, 10, 64)
	if parseError != nil {
		return fallback
	}
	return parsed
}
