package email

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

var (
	// ErrDeliveryFailed means the transport rejected the message before acceptance.
	ErrDeliveryFailed = errors.New("email: delivery failed")
	// ErrDeliveryUnknown means interruption left remote acceptance ambiguous.
	ErrDeliveryUnknown = errors.New("email: delivery outcome is unknown")
	// ErrInvalidTransportOutcome means a transport returned an unusable outcome.
	ErrInvalidTransportOutcome = errors.New("email: transport returned an invalid outcome")
)

const (
	receiptSchemaVersion  = 1
	errorCodeAttempt      = "attempt_pending"
	errorCodeValidation   = "validation_failed"
	errorCodeSink         = "receipt_sink_failed"
	errorCodeFailed       = "transport_failed"
	errorCodeUnknown      = "transport_unknown"
	errorCodeInvalid      = "transport_invalid_outcome"
	errorCodeAcceptedErr  = "transport_accepted_with_error"
	provenanceUnavailable = "provenance source unavailable"
	unsafeURLUnavailable  = "unsafe provenance URL omitted"
	invalidUnavailable    = "provenance source returned invalid metadata"
)

// Mailer renders, records, and delivers bounded email messages. Its fields are
// immutable after construction, and Send starts no background goroutines.
type Mailer struct {
	transport    ITransport
	provenance   IProvenanceSource
	receipts     IReceiptSink
	from         address
	to           []address
	now          func() time.Time
	newReceiptID func() string
	metrics      *mailerMetrics
}

// NewMailer validates all required host dependencies before constructing a Mailer.
func NewMailer(options ...Option) (*Mailer, error) {
	config := mailerConfig{now: time.Now}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("email: nil option")
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	if config.transport == nil {
		return nil, ErrNoTransport
	}
	if config.provenance == nil {
		return nil, ErrNoProvenance
	}
	if config.receipts == nil {
		return nil, ErrNoReceiptSink
	}
	if config.from == nil {
		return nil, ErrNoSender
	}
	if len(config.to) == 0 {
		return nil, ErrNoRecipients
	}
	metrics, err := newMailerMetrics(config.registerer)
	if err != nil {
		return nil, err
	}
	recipients := make([]address, 0, len(config.to))
	for _, recipient := range config.to {
		recipients = append(recipients, addressFromMail(recipient))
	}
	return &Mailer{
		transport:    config.transport,
		provenance:   config.provenance,
		receipts:     config.receipts,
		from:         addressFromMail(config.from),
		to:           recipients,
		now:          config.now,
		newReceiptID: uuid.NewString,
		metrics:      metrics,
	}, nil
}

// Send records an UNKNOWN attempt before network I/O, then replaces it with the
// final bounded outcome. A process crash between those records deliberately
// leaves UNKNOWN because blind retry could duplicate an accepted message.
func (mailer *Mailer) Send(ctx context.Context, message *emailv1.EmailMessage) (*emailv1.EmailReceipt, error) {
	started := mailer.now().UTC()
	ctx, span := otel.Tracer("github.com/candacelabs/csf/services/email").Start(ctx, "email.send")
	defer span.End()

	clonedMessage := cloneMessage(message)
	metadata := mailer.snapshot(ctx, started)
	if validationErr := emailv1.ValidateEmailMessage(clonedMessage); validationErr != nil {
		receipt := newAttemptReceipt(metadata, nil)
		return mailer.finishWithoutTransport(
			ctx,
			receipt,
			started,
			errorCodeValidation,
			fmt.Errorf("validating email message: %w", validationErr),
			span,
		)
	}
	raw, renderErr := renderMessage(mailer.from, mailer.to, clonedMessage, metadata)
	receipt := newAttemptReceipt(metadata, raw)

	if validationErr := validateAttempt(receipt, renderErr); validationErr != nil {
		return mailer.finishWithoutTransport(ctx, receipt, started, errorCodeValidation, validationErr, span)
	}
	if err := mailer.record(ctx, receipt); err != nil {
		receipt.Outcome = emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED
		receipt.ErrorCode = errorCodeSink
		receipt.CompletedAt = timestamppb.New(mailer.now().UTC())
		mailer.observe(receipt, started, span, err)
		return receipt, fmt.Errorf("recording pre-send receipt: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return mailer.finishWithoutTransport(ctx, receipt, started, errorCodeFailed, err, span)
	}

	outcome, transportErr := mailer.transport.Send(ctx, raw)
	return mailer.finishTransport(ctx, receipt, started, outcome, transportErr, span)
}

func cloneMessage(message *emailv1.EmailMessage) *emailv1.EmailMessage {
	if message == nil {
		return &emailv1.EmailMessage{}
	}
	return proto.Clone(message).(*emailv1.EmailMessage)
}

func (mailer *Mailer) snapshot(ctx context.Context, recordedAt time.Time) *provenancev1.ReceiptMetadata {
	observed, err := mailer.provenance.Snapshot(ctx)
	receiptID := mailer.newReceiptID()
	if err != nil || observed == nil {
		return unavailableMetadata(receiptID, recordedAt, provenanceUnavailable)
	}
	if err := validateMetadataShape(observed); err != nil {
		return unavailableMetadata(receiptID, recordedAt, invalidUnavailable)
	}
	metadata := proto.Clone(observed).(*provenancev1.ReceiptMetadata)
	metadata.SchemaVersion = receiptSchemaVersion
	metadata.ReceiptId = receiptID
	metadata.RecordedAt = timestamppb.New(recordedAt)
	sanitizeMetadataURLs(metadata)
	if validationErr := validateMetadata(metadata); validationErr != nil {
		return unavailableMetadata(receiptID, recordedAt, invalidUnavailable)
	}
	return metadata
}

func unavailableMetadata(receiptID string, recordedAt time.Time, reason string) *provenancev1.ReceiptMetadata {
	return &provenancev1.ReceiptMetadata{
		SchemaVersion: receiptSchemaVersion,
		ReceiptId:     receiptID,
		RecordedAt:    timestamppb.New(recordedAt),
		Unavailable:   []string{reason},
	}
}

func newAttemptReceipt(metadata *provenancev1.ReceiptMetadata, raw []byte) *emailv1.EmailReceipt {
	digest := sha256.Sum256(raw)
	return &emailv1.EmailReceipt{
		Provenance:    metadata,
		MessageSha256: fmt.Sprintf("%x", digest),
		Outcome:       emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN,
		ErrorCode:     errorCodeAttempt,
	}
}

func validateAttempt(receipt *emailv1.EmailReceipt, renderErr error) error {
	if renderErr != nil {
		return renderErr
	}
	if err := validateReceipt(receipt); err != nil {
		return fmt.Errorf("validating email receipt: %w", err)
	}
	return nil
}

func (mailer *Mailer) finishWithoutTransport(
	ctx context.Context,
	receipt *emailv1.EmailReceipt,
	started time.Time,
	errorCode string,
	cause error,
	span trace.Span,
) (*emailv1.EmailReceipt, error) {
	receipt.Outcome = emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED
	receipt.ErrorCode = errorCode
	receipt.CompletedAt = timestamppb.New(mailer.now().UTC())
	recordErr := mailer.record(ctx, receipt)
	if recordErr != nil {
		receipt.ErrorCode = errorCodeSink
		cause = errors.Join(cause, fmt.Errorf("recording final receipt: %w", recordErr))
	}
	mailer.observe(receipt, started, span, cause)
	return receipt, cause
}

func (mailer *Mailer) finishTransport(
	ctx context.Context,
	receipt *emailv1.EmailReceipt,
	started time.Time,
	outcome emailv1.DeliveryOutcome,
	transportErr error,
	span trace.Span,
) (*emailv1.EmailReceipt, error) {
	receipt.Outcome, receipt.ErrorCode, transportErr = normalizeOutcome(outcome, transportErr)
	receipt.CompletedAt = timestamppb.New(mailer.now().UTC())
	recordErr := mailer.record(ctx, receipt)
	if recordErr != nil {
		receipt.ErrorCode = errorCodeSink
		transportErr = errors.Join(transportErr, fmt.Errorf("recording final receipt: %w", recordErr))
	}
	mailer.observe(receipt, started, span, transportErr)
	return receipt, transportErr
}

func normalizeOutcome(outcome emailv1.DeliveryOutcome, transportErr error) (emailv1.DeliveryOutcome, string, error) {
	switch outcome {
	case emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED:
		if transportErr != nil {
			return outcome, errorCodeAcceptedErr, transportErr
		}
		return outcome, "", nil
	case emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED:
		if transportErr == nil {
			transportErr = ErrDeliveryFailed
		}
		return outcome, errorCodeFailed, transportErr
	case emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN:
		if transportErr == nil {
			transportErr = ErrDeliveryUnknown
		} else {
			transportErr = errors.Join(ErrDeliveryUnknown, transportErr)
		}
		return outcome, errorCodeUnknown, transportErr
	default:
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN, errorCodeInvalid, errors.Join(ErrInvalidTransportOutcome, transportErr)
	}
}

func (mailer *Mailer) record(ctx context.Context, receipt *emailv1.EmailReceipt) error {
	copyForSink := proto.Clone(receipt).(*emailv1.EmailReceipt)
	return mailer.receipts.Record(ctx, copyForSink)
}

func (mailer *Mailer) observe(
	receipt *emailv1.EmailReceipt,
	started time.Time,
	span trace.Span,
	err error,
) {
	outcome := metricOutcome(receipt.GetOutcome())
	mailer.metrics.observe(outcome, mailer.now().UTC().Sub(started).Seconds())
	span.SetAttributes(
		attribute.String("email.delivery.outcome", outcome),
		attribute.String("email.error_code", receipt.GetErrorCode()),
		attribute.String("email.receipt_id", receipt.GetProvenance().GetReceiptId()),
	)
	if err != nil {
		span.SetStatus(codes.Error, receipt.GetErrorCode())
	}
}

func metricOutcome(outcome emailv1.DeliveryOutcome) string {
	switch outcome {
	case emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED:
		return metricOutcomeAccepted
	case emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED:
		return metricOutcomeFailed
	default:
		return metricOutcomeUnknown
	}
}
