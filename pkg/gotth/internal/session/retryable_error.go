package session

import "errors"

// RetryableError marks an effect's failure as transient, so the failure event
// says so and a reducer can decide to schedule another attempt.
//
// It carries no message of its own. The classification travels as its own
// field on the event, and prefixing the error text would put the same fact in
// two places — one of which a reducer would then be tempted to parse.
type RetryableError struct {
	// Err is the failure being classified. It is never nil: Retryable(nil) is
	// nil rather than a marked nil.
	Err error
}

// Error is the wrapped error's message, unchanged. The classification is not
// prefixed onto it deliberately — it travels as its own event field, and a
// message that also carried it would invite a reducer to parse the string.
func (e *RetryableError) Error() string { return e.Err.Error() }

// Unwrap exposes the underlying failure, so the mark survives errors.Is and
// errors.As in both directions.
func (e *RetryableError) Unwrap() error { return e.Err }

// IsRetryable reports whether a failure was explicitly classified as transient.
//
// Unclassified is terminal, and that direction is the whole point. An effect
// may have committed externally before it failed, so re-running one nobody
// classified risks duplicating a side effect that already happened; retrying
// is a claim about idempotence, and only the code that performed the effect
// can make it. A failure never retried costs a change that does not happen. A
// failure retried blindly costs one that happens twice.
//
// errors.As rather than a type assertion, so the mark survives being wrapped
// by helpers between the effect that set it and the actor that reads it. It is
// exported out of this package because live.IsRetryable is this function and
// not a second copy of it: a predicate an application can call and a predicate
// the library decides on must not be two implementations that can disagree.
func IsRetryable(err error) bool {
	var marked *RetryableError
	return errors.As(err, &marked)
}
