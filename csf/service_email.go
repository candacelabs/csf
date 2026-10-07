package csf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	emailObservationBytes         = 1 << 20
	emailObservationMaxAge        = 5 * time.Minute
	emailObservationMaxContainers = 32
	emailUnknownProvider          = "unknown"
)

var ErrEmailUnavailable = errors.New("operator email is not configured")

// IEmailSender is the shared capability, not an SMTP implementation.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=email.go -destination=email_mock_test.go -package=csf
type IEmailSender interface {
	Send(ctx context.Context, message *emailv1.EmailMessage) (*emailv1.EmailReceipt, error)
}

func WithEmail(sender IEmailSender) Option {
	return func(service *Service) { service.email = sender }
}

// SendEmail accepts content only, and never accepts a caller-selected recipient
// or claimed provenance. Legacy unauthenticated HTTP/MCP routes fail closed.
func (service *Service) SendEmail(ctx context.Context, request *emailv1.SendEmailRequest) (*emailv1.SendEmailResponse, error) {
	if _, err := agentIDFromContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if service.email == nil {
		return nil, ErrEmailUnavailable
	}
	if err := emailv1.ValidateEmailMessage(request.GetMessage()); err != nil {
		return nil, fmt.Errorf("%w: message is required and must meet the email content bounds", ErrInvalidRequest)
	}
	receipt, err := service.email.Send(ctx, request.Message)
	if receipt != nil {
		// Preserve failed/uncertain delivery evidence at the API boundary. Returning
		// only an HTTP error would hide whether a blind retry could duplicate mail.
		return &emailv1.SendEmailResponse{Receipt: receipt}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("email attempt failed before a receipt was available")
	}
	return nil, fmt.Errorf("email capability returned no receipt")
}

// EmailProvenance observes host-owned metadata at send time. It reads only the
// explicitly configured observer file, never a Docker socket or other sessions.
type EmailProvenance struct {
	configuration *emailv1.EmailHostConfiguration
	now           func() time.Time
}

func NewEmailProvenance(configuration *emailv1.EmailHostConfiguration, now func() time.Time) *EmailProvenance {
	if configuration == nil {
		configuration = &emailv1.EmailHostConfiguration{}
	}
	if now == nil {
		now = time.Now
	}
	return &EmailProvenance{configuration: proto.CloneOf(configuration), now: now}
}

func (source *EmailProvenance) Snapshot(ctx context.Context) (*provenancev1.ReceiptMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := source.configuration
	metadata := &provenancev1.ReceiptMetadata{
		ReportingNode: proto.CloneOf(cfg.ReportingNode), CsfVersion: cfg.CsfVersion,
		SourceRevision: cfg.SourceRevision,
	}
	for _, link := range cfg.Links {
		metadata.Links = append(metadata.Links, proto.CloneOf(link))
	}
	identity, ok := ctx.Value(agentIdentityContextKey{}).(agentIdentity)
	if ok && identity.id != "" && identity.sessionID != "" {
		provider := cfg.SessionProvider
		if provider == "" {
			provider = emailUnknownProvider
		}
		metadata.Sessions = []*provenancev1.SessionReference{{Provider: provider, AgentId: identity.id, SessionId: identity.sessionID}}
	}
	containers, err := source.observeContainers()
	if err != nil {
		metadata.Unavailable = append(metadata.Unavailable, "container observations unavailable or stale")
	} else {
		metadata.Containers = containers
	}
	return metadata, nil
}

func (source *EmailProvenance) observeContainers() ([]*provenancev1.ContainerObservation, error) {
	file, err := os.Open(source.configuration.ContainerObservationsFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, emailObservationBytes+1))
	if err != nil || len(content) > emailObservationBytes {
		return nil, fmt.Errorf("container observation read failed or exceeded bounds")
	}
	observations := &provenancev1.ReceiptMetadata{}
	if err := protojson.Unmarshal(content, observations); err != nil {
		return nil, err
	}
	if observations.SchemaVersion != 1 || observations.RecordedAt == nil || observations.RecordedAt.CheckValid() != nil || len(observations.Containers) > emailObservationMaxContainers {
		return nil, fmt.Errorf("invalid container observation snapshot")
	}
	now := source.now()
	if age := now.Sub(observations.RecordedAt.AsTime()); age < 0 || age > emailObservationMaxAge {
		return nil, fmt.Errorf("stale container observation snapshot")
	}
	for _, container := range observations.Containers {
		if container == nil || container.ObservedAt == nil || container.ObservedAt.CheckValid() != nil {
			return nil, fmt.Errorf("container observation timestamp required")
		}
		if age := now.Sub(container.ObservedAt.AsTime()); age < 0 || age > emailObservationMaxAge {
			return nil, fmt.Errorf("stale container observation")
		}
	}
	return observations.Containers, nil
}
