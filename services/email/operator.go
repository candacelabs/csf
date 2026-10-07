// Copyright 2026 Candace Labs

package email

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/privatefile"
	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

const (
	// MaxOperatorConfigurationBytes bounds the operator email configuration.
	MaxOperatorConfigurationBytes = 1 << 20
	maxSMTPPasswordBytes          = 4096
)

// NewOperatorMailer builds the operator's mailer from the private operator
// email configuration at path (protobuf JSON, owner-only), the one channel
// that reaches the operator's phone. Every binary that tells the operator
// something builds it this way. A nil registry exports no send metrics.
func NewOperatorMailer(path string, registry prometheus.Registerer) (*Mailer, error) {
	content, err := privatefile.Read(path, MaxOperatorConfigurationBytes)
	if err != nil {
		return nil, fmt.Errorf("read private operator email configuration: %w", err)
	}
	configuration := &emailv1.EmailHostConfiguration{}
	if err := protojson.Unmarshal(content, configuration); err != nil {
		return nil, errors.New("operator email configuration is invalid protobuf JSON")
	}
	if err := emailv1.ValidateEmailHostConfiguration(configuration); err != nil {
		return nil, errors.New("operator email configuration requires a host, valid port and receipt directory")
	}
	var password string
	if configuration.SmtpPasswordFile != "" {
		secret, err := privatefile.Read(configuration.SmtpPasswordFile, maxSMTPPasswordBytes)
		if err != nil {
			return nil, fmt.Errorf("read private SMTP password: %w", err)
		}
		password = strings.TrimRight(string(secret), "\r\n")
	}
	if configuration.ReportingNode == nil {
		configuration.ReportingNode = &provenancev1.NodeIdentity{}
	}
	if configuration.ReportingNode.Hostname == "" {
		configuration.ReportingNode.Hostname, _ = os.Hostname()
	}
	sink, err := NewFileReceiptSink(configuration.ReceiptDirectory)
	if err != nil {
		return nil, err
	}
	options := []Option{
		WithAddresses(configuration.Sender, configuration.Recipients),
		WithTransport(NewSMTPTransport(SMTPConfig{
			Host: configuration.SmtpHost, Port: int(configuration.SmtpPort),
			Username: configuration.SmtpUsername, Password: password,
		})),
		WithProvenance(csf.NewEmailProvenance(configuration, nil)),
		WithReceiptSink(sink),
	}
	if registry != nil {
		options = append(options, WithMetrics(registry))
	}
	return NewMailer(options...)
}
