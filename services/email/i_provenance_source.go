package email

import (
	"context"

	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

// IProvenanceSource observes the runtime facts attached to one send attempt.
// Mailer clones the returned message before assigning receipt-owned fields.
type IProvenanceSource interface {
	Snapshot(ctx context.Context) (*provenancev1.ReceiptMetadata, error)
}
