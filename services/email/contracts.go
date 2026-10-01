package email

import (
	"context"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

// ITransport delivers one complete RFC 5322 message. ACCEPTED means only that
// the transport accepted the message; it does not prove inbox delivery.
type ITransport interface {
	Send(ctx context.Context, message []byte) (emailv1.DeliveryOutcome, error)
}

// IProvenanceSource observes the runtime facts attached to one send attempt.
// Mailer clones the returned message before assigning receipt-owned fields.
type IProvenanceSource interface {
	Snapshot(ctx context.Context) (*provenancev1.ReceiptMetadata, error)
}

// IReceiptSink records bounded receipt evidence. Implementations must treat a
// later record with the same receipt ID as the final replacement for the
// pre-send UNKNOWN attempt.
type IReceiptSink interface {
	Record(ctx context.Context, receipt *emailv1.EmailReceipt) error
}
