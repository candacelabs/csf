// Copyright 2026 Candace Labs

// Package patience provides bounded channel receives with cancellation and error messages.
package patience

import (
	"context"
	"fmt"
	"time"
)

// Await receives from ch with a timeout and a descriptive error message if it times out.
// T is the type of the channel values.
func Await[T any](ctx context.Context, ch <-chan T, message string) (T, error) {
	select {
	case v := <-ch:
		return v, nil
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("await: %s: %w", message, context.Cause(ctx))
	}
}

// AwaitWithTimeout is like Await but creates the timeout context internally.
func AwaitWithTimeout[T any](ctx context.Context, ch <-chan T, timeout time.Duration, message string) (T, error) {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return Await(bounded, ch, message)
}
