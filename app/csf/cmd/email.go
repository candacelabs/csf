package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/privatefile"
	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
	"github.com/candacelabs/csf/services/email"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	operatorEmailConfigFlag    = "operator-email-config"
	maxEmailConfigurationBytes = 1 << 20
	maxSMTPPasswordBytes       = 4096
)

func configuredOperatorEmail(path string, registry *prometheus.Registry) (*email.Mailer, error) {
	content, err := privatefile.Read(path, maxEmailConfigurationBytes)
	if err != nil {
		return nil, fmt.Errorf("read private operator email configuration: %w", err)
	}
	configuration := &emailv1.EmailHostConfiguration{}
	if err := protojson.Unmarshal(content, configuration); err != nil {
		return nil, fmt.Errorf("operator email configuration is invalid protobuf JSON")
	}
	if err := emailv1.ValidateEmailHostConfiguration(configuration); err != nil {
		return nil, fmt.Errorf("operator email configuration requires a host, valid port and receipt directory")
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
	sink, err := email.NewFileReceiptSink(configuration.ReceiptDirectory)
	if err != nil {
		return nil, err
	}
	return email.NewMailer(
		email.WithAddresses(configuration.Sender, configuration.Recipients),
		email.WithTransport(email.NewSMTPTransport(email.SMTPConfig{
			Host: configuration.SmtpHost, Port: int(configuration.SmtpPort),
			Username: configuration.SmtpUsername, Password: password,
		})),
		email.WithProvenance(csf.NewEmailProvenance(configuration, nil)),
		email.WithReceiptSink(sink), email.WithMetrics(registry),
	)
}
