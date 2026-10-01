package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
)

var (
	// ErrSTARTTLSRequired means the server offered no encrypted upgrade.
	ErrSTARTTLSRequired = errors.New("email: smtp server does not advertise STARTTLS")
	// ErrAuthUnsupported means credentials were configured but encrypted SMTP
	// offered no AUTH extension.
	ErrAuthUnsupported = errors.New("email: smtp server does not advertise AUTH")
)

const (
	smtpDialTimeout = 10 * time.Second
	smtpDeadline    = 30 * time.Second
)

// SMTPConfig configures a real SMTP transport. The Mailer, not the transport,
// owns sender and recipients; the transport reads their validated MIME headers.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
}

type iSMTPClient interface {
	Hello(localName string) error
	Extension(extension string) (bool, string)
	StartTLS(config *tls.Config) error
	Auth(auth smtp.Auth) error
	Mail(from string) error
	Rcpt(to string) error
	Data() (io.WriteCloser, error)
	Quit() error
	Close() error
}

var _ iSMTPClient = (*smtp.Client)(nil)

// SMTPTransport performs one mandatory-STARTTLS SMTP conversation per Send.
type SMTPTransport struct {
	config SMTPConfig
}

var _ ITransport = (*SMTPTransport)(nil)

// NewSMTPTransport returns a real SMTP transport. Configuration is validated
// before the first network operation in Send.
func NewSMTPTransport(config SMTPConfig) *SMTPTransport {
	return &SMTPTransport{config: config}
}

// Send delivers message with STARTTLS mandatory. Failure while closing DATA is
// UNKNOWN because the server may have accepted the message. Once DATA closes
// successfully, a later QUIT failure does not revoke ACCEPTED.
func (transport *SMTPTransport) Send(ctx context.Context, message []byte) (emailv1.DeliveryOutcome, error) {
	from, recipients, err := parseEnvelope(message)
	if err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, err
	}
	if err := validateSMTPConfig(transport.config); err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, err
	}
	if err := ctx.Err(); err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, err
	}
	address := net.JoinHostPort(transport.config.Host, strconv.Itoa(transport.config.Port))
	dialer := net.Dialer{Timeout: smtpDialTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("dialing smtp server: %w", err)
	}
	setSMTPDeadline(ctx, connection)
	stopCancellation := interruptOnCancellation(ctx, connection)
	defer close(stopCancellation)

	client, err := smtp.NewClient(connection, transport.config.Host)
	if err != nil {
		_ = connection.Close()
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("creating smtp client: %w", err)
	}
	defer client.Close()
	return transport.converse(client, from, recipients, message)
}

func (transport *SMTPTransport) converse(
	client iSMTPClient,
	from string,
	recipients []string,
	message []byte,
) (emailv1.DeliveryOutcome, error) {
	if err := client.Hello(ehloName()); err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("smtp EHLO: %w", err)
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, ErrSTARTTLSRequired
	}
	if err := client.StartTLS(&tls.Config{ServerName: transport.config.Host, MinVersion: tls.VersionTLS12}); err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("smtp STARTTLS: %w", err)
	}
	if transport.config.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, ErrAuthUnsupported
		}
		auth := smtp.PlainAuth("", transport.config.Username, transport.config.Password, transport.config.Host)
		if err := client.Auth(auth); err != nil {
			return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("smtp AUTH: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("smtp RCPT TO: %w", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_FAILED, fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN, fmt.Errorf("writing smtp DATA: %w", err)
	}
	if err := writer.Close(); err != nil {
		return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_UNKNOWN, fmt.Errorf("completing smtp DATA: %w", err)
	}
	_ = client.Quit()
	return emailv1.DeliveryOutcome_DELIVERY_OUTCOME_ACCEPTED, nil
}

func validateSMTPConfig(config SMTPConfig) error {
	if strings.TrimSpace(config.Host) == "" {
		return errors.New("email: smtp host is required")
	}
	if config.Port < 1 || config.Port > 65535 {
		return errors.New("email: smtp port is invalid")
	}
	return nil
}

func parseEnvelope(message []byte) (string, []string, error) {
	parsed, err := mail.ReadMessage(bytesReader(message))
	if err != nil {
		return "", nil, fmt.Errorf("parsing smtp envelope headers: %w", err)
	}
	from, err := mail.ParseAddress(parsed.Header.Get("From"))
	if err != nil {
		return "", nil, fmt.Errorf("parsing smtp From header: %w", err)
	}
	to, err := parsed.Header.AddressList("To")
	if err != nil || len(to) == 0 {
		return "", nil, ErrNoRecipients
	}
	recipients := make([]string, 0, len(to))
	for _, recipient := range to {
		recipients = append(recipients, recipient.Address)
	}
	return from.Address, recipients, nil
}

func bytesReader(message []byte) *strings.Reader {
	return strings.NewReader(string(message))
}

func setSMTPDeadline(ctx context.Context, connection net.Conn) {
	deadline := time.Now().Add(smtpDeadline)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
}

func interruptOnCancellation(ctx context.Context, connection net.Conn) chan struct{} {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.SetDeadline(time.Now())
		case <-stop:
		}
	}()
	return stop
}

func ehloName() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "localhost"
	}
	return sanitizeHeader(strings.TrimSpace(hostname))
}
