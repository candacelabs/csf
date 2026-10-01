package email

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	// ErrNoTransport means no delivery transport was configured.
	ErrNoTransport = errors.New("email: transport is required")
	// ErrNoProvenance means no runtime provenance source was configured.
	ErrNoProvenance = errors.New("email: provenance source is required")
	// ErrNoReceiptSink means no receipt evidence sink was configured.
	ErrNoReceiptSink = errors.New("email: receipt sink is required")
	// ErrNoSender means the host did not configure one valid sender.
	ErrNoSender = errors.New("email: sender is required")
	// ErrNoRecipients means the host did not configure any valid recipients.
	ErrNoRecipients = errors.New("email: at least one recipient is required")
	// ErrHeaderInjection means an address contains a line break.
	ErrHeaderInjection = errors.New("email: address contains a CR or LF")
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

// Option configures a Mailer before construction.
type Option func(config *mailerConfig) error

// WithTransport configures the delivery boundary.
func WithTransport(transport ITransport) Option {
	return func(config *mailerConfig) error {
		if transport == nil {
			return ErrNoTransport
		}
		config.transport = transport
		return nil
	}
}

// WithProvenance configures the trusted runtime observation boundary.
func WithProvenance(source IProvenanceSource) Option {
	return func(config *mailerConfig) error {
		if source == nil {
			return ErrNoProvenance
		}
		config.provenance = source
		return nil
	}
}

// WithReceiptSink configures receipt evidence retention.
func WithReceiptSink(sink IReceiptSink) Option {
	return func(config *mailerConfig) error {
		if sink == nil {
			return ErrNoReceiptSink
		}
		config.receipts = sink
		return nil
	}
}

// WithAddresses configures the host-owned envelope and visible address headers.
func WithAddresses(from string, to []string) Option {
	return func(config *mailerConfig) error {
		sender, recipients, err := parseAddresses(from, to)
		if err != nil {
			return err
		}
		config.from = sender
		config.to = recipients
		return nil
	}
}

// WithClock replaces the real clock, primarily for deterministic verification.
func WithClock(now func() time.Time) Option {
	return func(config *mailerConfig) error {
		if now == nil {
			return errors.New("email: clock is nil")
		}
		config.now = now
		return nil
	}
}

// WithMetrics registers bounded send metrics with registerer.
func WithMetrics(registerer prometheus.Registerer) Option {
	return func(config *mailerConfig) error {
		if registerer == nil {
			return errors.New("email: metrics registerer is nil")
		}
		config.registerer = registerer
		return nil
	}
}

func parseAddresses(from string, to []string) (*mail.Address, []*mail.Address, error) {
	if strings.TrimSpace(from) == "" {
		return nil, nil, ErrNoSender
	}
	if strings.ContainsAny(from, "\r\n") {
		return nil, nil, ErrHeaderInjection
	}
	sender, err := mail.ParseAddress(from)
	if err != nil || !strings.Contains(sender.Address, "@") {
		return nil, nil, fmt.Errorf("%w: invalid From address", ErrNoSender)
	}
	if len(sender.String()) > maxAddressHeader {
		return nil, nil, fmt.Errorf("%w: From address is too long", ErrNoSender)
	}
	if len(to) == 0 || len(to) > maxRecipients {
		return nil, nil, ErrNoRecipients
	}
	recipients := make([]*mail.Address, 0, len(to))
	for _, raw := range to {
		if strings.TrimSpace(raw) == "" {
			return nil, nil, ErrNoRecipients
		}
		if strings.ContainsAny(raw, "\r\n") {
			return nil, nil, ErrHeaderInjection
		}
		recipient, parseErr := mail.ParseAddress(raw)
		if parseErr != nil || !strings.Contains(recipient.Address, "@") {
			return nil, nil, fmt.Errorf("%w: invalid To address", ErrNoRecipients)
		}
		if len(recipient.String()) > maxAddressHeader {
			return nil, nil, fmt.Errorf("%w: To address is too long", ErrNoRecipients)
		}
		recipients = append(recipients, recipient)
	}
	return sender, recipients, nil
}
