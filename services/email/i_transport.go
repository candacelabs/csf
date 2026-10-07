package email

import (
	"context"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
)

// ITransport delivers one complete RFC 5322 message. ACCEPTED means only that
// the transport accepted the message; it does not prove inbox delivery.
type ITransport interface {
	Send(ctx context.Context, message []byte) (emailv1.DeliveryOutcome, error)
}
