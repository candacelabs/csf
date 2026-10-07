package email

import (
	"context"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
)

// IReceiptSink records bounded receipt evidence. Implementations must treat a
// later record with the same receipt ID as the final replacement for the
// pre-send UNKNOWN attempt.
type IReceiptSink interface {
	Record(ctx context.Context, receipt *emailv1.EmailReceipt) error
}
