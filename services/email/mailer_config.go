package email

import (
	"net/mail"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	maxRecipients    = 64
	maxAddressHeader = 512
)

type mailerConfig struct {
	transport  ITransport
	provenance IProvenanceSource
	receipts   IReceiptSink
	from       *mail.Address
	to         []*mail.Address
	now        func() time.Time
	registerer prometheus.Registerer
}
