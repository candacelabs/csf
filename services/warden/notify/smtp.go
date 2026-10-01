package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/core"
	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
	sharedemail "github.com/candacelabs/csf/services/email"
	"github.com/candacelabs/csf/services/warden"
)

var (
	// Compatibility aliases preserve errors.Is behavior for existing callers.
	ErrSTARTTLSRequired = sharedemail.ErrSTARTTLSRequired
	ErrAuthUnsupported  = sharedemail.ErrAuthUnsupported
	ErrNoRecipients     = sharedemail.ErrNoRecipients
	ErrNoSender         = sharedemail.ErrNoSender
	ErrHeaderInjection  = sharedemail.ErrHeaderInjection
)

const (
	defaultVersionUnavailable    = "csf version unknown"
	defaultContainersUnavailable = "container observations unavailable"
	defaultSessionsUnavailable   = "associated sessions unavailable"
	hostProvenanceUnavailable    = "host provenance unavailable"
	// EmailMessage.subject is bounded in proto/candace/email/v1/email.proto.
	maxSubjectBytes = 256
	subjectEllipsis = "..."
)

// SMTPConfig preserves the Warden configuration surface while delegating
// transport and message policy to services/email.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
}

type smtpNotifierConfig struct {
	provenance sharedemail.IProvenanceSource
	receipts   sharedemail.IReceiptSink
	registerer prometheus.Registerer
	transport  sharedemail.ITransport
	now        func() time.Time
}

// SMTPOption configures the Warden adapter without changing existing callers.
type SMTPOption func(config *smtpNotifierConfig) error

// WithProvenance configures host-observed provenance. When absent or failing,
// the notifier still identifies the incident's ReportedBy node.
func WithProvenance(source sharedemail.IProvenanceSource) SMTPOption {
	return func(config *smtpNotifierConfig) error {
		if source == nil {
			return sharedemail.ErrNoProvenance
		}
		config.provenance = source
		return nil
	}
}

// WithReceiptSink configures durable receipt evidence retention.
func WithReceiptSink(sink sharedemail.IReceiptSink) SMTPOption {
	return func(config *smtpNotifierConfig) error {
		if sink == nil {
			return sharedemail.ErrNoReceiptSink
		}
		config.receipts = sink
		return nil
	}
}

// WithMetrics registers the shared bounded email metrics.
func WithMetrics(registerer prometheus.Registerer) SMTPOption {
	return func(config *smtpNotifierConfig) error {
		if registerer == nil {
			return errors.New("notify: metrics registerer is nil")
		}
		config.registerer = registerer
		return nil
	}
}

func withTransport(transport sharedemail.ITransport) SMTPOption {
	return func(config *smtpNotifierConfig) error {
		config.transport = transport
		return nil
	}
}

func withClock(now func() time.Time) SMTPOption {
	return func(config *smtpNotifierConfig) error {
		config.now = now
		return nil
	}
}

// TerminalDeliveryError marks an outcome that the watchdog must not blindly
// retry because the remote server may already have accepted the message.
type TerminalDeliveryError struct {
	Outcome   emailv1.DeliveryOutcome
	ErrorCode string
	ReceiptID string
	cause     error
}

// Error returns a bounded description without SMTP details or addresses.
func (deliveryError *TerminalDeliveryError) Error() string {
	return fmt.Sprintf(
		"warden email terminal outcome %s (receipt %s, code %s)",
		deliveryError.Outcome.String(),
		deliveryError.ReceiptID,
		deliveryError.ErrorCode,
	)
}

// Unwrap exposes the underlying operational error to errors.Is/errors.As.
func (deliveryError *TerminalDeliveryError) Unwrap() error { return deliveryError.cause }

// Retryable prevents blind retransmission of a possibly accepted message.
func (deliveryError *TerminalDeliveryError) Retryable() bool { return false }

// SMTPNotifier adapts Warden incidents to the shared Mailer.
type SMTPNotifier struct {
	mailer  *sharedemail.Mailer
	initErr error
}

var _ warden.INotifier = (*SMTPNotifier)(nil)

// NewSMTPNotifier preserves the historical one-argument call while accepting
// host provenance, durable receipt, and metrics options.
func NewSMTPNotifier(config SMTPConfig, options ...SMTPOption) *SMTPNotifier {
	notifierConfig := smtpNotifierConfig{
		receipts: logReceiptSink{},
		transport: sharedemail.NewSMTPTransport(sharedemail.SMTPConfig{
			Host: config.Host, Port: config.Port, Username: config.Username, Password: config.Password,
		}),
	}
	for _, option := range options {
		if option == nil {
			return &SMTPNotifier{initErr: errors.New("notify: nil SMTP option")}
		}
		if err := option(&notifierConfig); err != nil {
			return &SMTPNotifier{initErr: err}
		}
	}
	mailerOptions := []sharedemail.Option{
		sharedemail.WithTransport(notifierConfig.transport),
		sharedemail.WithProvenance(incidentProvenanceSource{source: notifierConfig.provenance}),
		sharedemail.WithReceiptSink(notifierConfig.receipts),
		sharedemail.WithAddresses(config.From, config.To),
	}
	if notifierConfig.now != nil {
		mailerOptions = append(mailerOptions, sharedemail.WithClock(notifierConfig.now))
	}
	if notifierConfig.registerer != nil {
		mailerOptions = append(mailerOptions, sharedemail.WithMetrics(notifierConfig.registerer))
	}
	mailer, err := sharedemail.NewMailer(mailerOptions...)
	return &SMTPNotifier{mailer: mailer, initErr: err}
}

// Notify renders and sends one incident through the shared Mailer.
func (notifier *SMTPNotifier) Notify(ctx context.Context, incident warden.Incident) error {
	if notifier.initErr != nil {
		return notifier.initErr
	}
	ctx = context.WithValue(ctx, incidentReporterKey{}, incident.ReportedBy)
	receipt, err := notifier.mailer.Send(ctx, &emailv1.EmailMessage{
		Subject: buildSubject(incident),
		Text:    buildBody(incident),
	})
	if err == nil {
		return nil
	}
	if receipt != nil && terminalOutcome(receipt, err) {
		return &TerminalDeliveryError{
			Outcome:   receipt.GetOutcome(),
			ErrorCode: receipt.GetErrorCode(),
			ReceiptID: receipt.GetProvenance().GetReceiptId(),
			cause:     err,
		}
	}
	return err
}

func terminalOutcome(receipt *emailv1.EmailReceipt, err error) bool {
	return receipt.GetOutcome() == emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED ||
		receipt.GetOutcome() == emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN ||
		errors.Is(err, sharedemail.ErrInvalidTransportOutcome)
}

type incidentReporterKey struct{}

type incidentProvenanceSource struct {
	source sharedemail.IProvenanceSource
}

func (source incidentProvenanceSource) Snapshot(ctx context.Context) (*provenancev1.ReceiptMetadata, error) {
	reporter, _ := ctx.Value(incidentReporterKey{}).(warden.NodeID)
	if source.source == nil {
		return defaultIncidentMetadata(reporter, ""), nil
	}
	metadata, err := source.source.Snapshot(ctx)
	if err != nil || metadata == nil {
		return defaultIncidentMetadata(reporter, hostProvenanceUnavailable), nil
	}
	metadata = proto.Clone(metadata).(*provenancev1.ReceiptMetadata)
	if metadata.ReportingNode == nil {
		metadata.ReportingNode = &provenancev1.NodeIdentity{NodeId: string(reporter)}
	}
	return metadata, nil
}

func defaultIncidentMetadata(reporter warden.NodeID, extraUnavailable string) *provenancev1.ReceiptMetadata {
	unavailable := []string{defaultVersionUnavailable, defaultContainersUnavailable, defaultSessionsUnavailable}
	if extraUnavailable != "" {
		unavailable = append(unavailable, extraUnavailable)
	}
	return &provenancev1.ReceiptMetadata{
		ReportingNode: &provenancev1.NodeIdentity{NodeId: string(reporter)},
		Unavailable:   unavailable,
	}
}

type logReceiptSink struct{}

func (logReceiptSink) Record(_ context.Context, receipt *emailv1.EmailReceipt) error {
	core.Logger.Info().
		Str("receipt_id", receipt.GetProvenance().GetReceiptId()).
		Str("outcome", receipt.GetOutcome().String()).
		Str("error_code", receipt.GetErrorCode()).
		Msg("warden email receipt (non-durable default sink)")
	return nil
}

func buildSubject(incident warden.Incident) string {
	var subject string
	switch incident.Type {
	case warden.IncidentPeerDead:
		subject = fmt.Sprintf("[warden] peer %s DEAD (term %d)", incident.Peer.ID, incident.Term)
	case warden.IncidentPeerRecovered:
		subject = fmt.Sprintf("[warden] peer %s recovered", incident.Peer.ID)
	default:
		subject = fmt.Sprintf("[warden] peer %s incident %s (term %d)", incident.Peer.ID, incident.Type, incident.Term)
	}
	if len(subject) > maxSubjectBytes {
		// Drop an incomplete final rune; the body retains the full node identity.
		subject = strings.ToValidUTF8(subject[:maxSubjectBytes-len(subjectEllipsis)], "") + subjectEllipsis
	}
	return subject
}

func buildBody(incident warden.Incident) string {
	return fmt.Sprintf(
		"A warden incident was detected on the candacenet fleet.\n\n"+
			"Incident:     %s\n"+
			"Incident ID:  %s\n"+
			"Peer:         %s (%s)\n"+
			"Reported by:  %s\n"+
			"Term:         %d\n"+
			"Detected at:  %s\n"+
			"Last seen:    %s\n\n%s\n",
		incident.Type,
		incident.ID,
		incident.Peer.ID,
		incident.Peer.Addr,
		incident.ReportedBy,
		incident.Term,
		core.FormatTimeOrNever(incident.DetectedAt),
		core.FormatTimeOrNever(incident.LastSeen),
		incident.Message,
	)
}
