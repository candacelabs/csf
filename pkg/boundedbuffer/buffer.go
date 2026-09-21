// Package boundedbuffer provides an io.Writer that retains at most a fixed
// number of bytes while reporting the original write lengths to its producer.
package boundedbuffer

import (
	"bytes"
	"fmt"

	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"
)

const truncatedSuffix = " (truncated)"

// Buffer captures bounded command diagnostics without applying backpressure.
type Buffer struct {
	data      bytes.Buffer
	maxBytes  int64
	truncated bool
}

// New constructs a Buffer from a validated retention policy.
func New(retention *boundedbufferv1.Retention) (*Buffer, error) {
	if err := boundedbufferv1.ValidateRetention(retention); err != nil {
		return nil, fmt.Errorf("boundedbuffer retention: %w", err)
	}
	return &Buffer{maxBytes: retention.MaxBytes}, nil
}

// Write retains the prefix that fits and reports the full input length so
// callers such as os/exec can continue draining process output.
func (b *Buffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := b.maxBytes - int64(b.data.Len())
	if remaining < int64(len(data)) {
		b.truncated = true
	}
	if remaining > 0 {
		_, _ = b.data.Write(data[:int(min(int64(len(data)), remaining))])
	}
	return written, nil
}

// Bytes returns the retained prefix. The returned slice aliases the Buffer.
func (b *Buffer) Bytes() []byte { return b.data.Bytes() }

// Truncated reports whether bytes were discarded after the retention bound.
func (b *Buffer) Truncated() bool { return b.truncated }

// String returns the retained prefix and marks it when bytes were discarded.
func (b *Buffer) String() string {
	if !b.truncated {
		return b.data.String()
	}
	return b.data.String() + truncatedSuffix
}
