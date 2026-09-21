package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

// Errors returned by the SMTP notifier and message builder.
var (
	// ErrSTARTTLSRequired means the server did not advertise STARTTLS; the
	// notifier fails closed rather than send credentials or mail in cleartext.
	ErrSTARTTLSRequired = errors.New("notify: smtp server does not advertise STARTTLS")
	// ErrAuthUnsupported means credentials were configured but the server did
	// not advertise AUTH after STARTTLS.
	ErrAuthUnsupported = errors.New("notify: smtp server does not advertise AUTH")
	// ErrNoRecipients means no To address was configured.
	ErrNoRecipients = errors.New("notify: no recipients configured")
	// ErrNoSender means no From address was configured.
	ErrNoSender = errors.New("notify: no From address configured")
	// ErrHeaderInjection means a header-contributing field held a CR or LF.
	ErrHeaderInjection = errors.New("notify: header field contains a CR or LF")
)

const (
	smtpDialTimeout = 10 * time.Second
	smtpDeadline    = 30 * time.Second
)

// SMTPConfig configures the SMTP notifier. Username may be empty to skip AUTH
// (e.g. an internal relay that authenticates by network).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
}

// SMTPNotifier sends incident emails over SMTP with STARTTLS. It holds only
// immutable configuration, so concurrent Notify calls (each on its own
// connection) are safe without synchronization.
type SMTPNotifier struct {
	cfg SMTPConfig
}

var _ warden.INotifier = (*SMTPNotifier)(nil)

// NewSMTPNotifier returns an SMTPNotifier for cfg.
func NewSMTPNotifier(cfg SMTPConfig) *SMTPNotifier { return &SMTPNotifier{cfg: cfg} }

// Notify builds and sends the incident email. The message is built first so a
// malformed From/To (e.g. a header-injection attempt) fails before any network
// contact.
func (n *SMTPNotifier) Notify(ctx context.Context, inc warden.Incident) error {
	msg, err := buildMessage(n.cfg.From, n.cfg.To, inc, time.Now())
	if err != nil {
		return fmt.Errorf("building message for incident %s: %w", inc.ID, err)
	}
	return n.send(ctx, msg)
}

// send performs the SMTP conversation: dial, EHLO, STARTTLS (mandatory), AUTH
// (if configured), MAIL/RCPT/DATA, QUIT.
func (n *SMTPNotifier) send(ctx context.Context, msg []byte) error {
	addr := net.JoinHostPort(n.cfg.Host, strconv.Itoa(n.cfg.Port))

	dialer := net.Dialer{Timeout: smtpDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dialing smtp server %s: %w", addr, err)
	}

	deadline := time.Now().Add(smtpDeadline)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	// Interrupt blocking I/O promptly if ctx is cancelled (e.g. shutdown), so
	// this call — and any delivery goroutine waiting on it — never lingers.
	// The watcher is always cleaned up when send returns via close(stop).
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stop:
		}
	}()

	c, err := smtp.NewClient(conn, n.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("creating smtp client for %s: %w", addr, err)
	}
	defer c.Close()

	if err := c.Hello(ehloName()); err != nil {
		return fmt.Errorf("smtp EHLO to %s: %w", addr, err)
	}

	// Fail closed: never transmit credentials or mail over a cleartext link.
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return fmt.Errorf("smtp server %s: %w", addr, ErrSTARTTLSRequired)
	}
	if err := c.StartTLS(&tls.Config{ServerName: n.cfg.Host}); err != nil {
		return fmt.Errorf("smtp STARTTLS to %s: %w", addr, err)
	}

	if n.cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return fmt.Errorf("smtp server %s: %w", addr, ErrAuthUnsupported)
		}
		auth := smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp AUTH to %s: %w", addr, err)
		}
	}

	if err := c.Mail(n.cfg.From); err != nil {
		return fmt.Errorf("smtp MAIL FROM <%s>: %w", n.cfg.From, err)
	}
	for _, rcpt := range n.cfg.To {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp RCPT TO <%s>: %w", rcpt, err)
		}
	}

	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("writing message body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("completing DATA: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp QUIT: %w", err)
	}
	return nil
}

// buildMessage renders a complete, well-formed RFC 5322 message for inc. It is
// a pure function: given the same inputs (including date) it produces identical
// bytes, which makes it exhaustively unit-testable. It rejects From/To
// addresses containing CR or LF and strips CR/LF from every header value, so a
// malicious peer identifier cannot inject extra headers.
func buildMessage(from string, to []string, inc warden.Incident, date time.Time) ([]byte, error) {
	if strings.TrimSpace(from) == "" {
		return nil, ErrNoSender
	}
	if err := ensureNoCRLF(from); err != nil {
		return nil, fmt.Errorf("From address: %w", err)
	}
	if len(to) == 0 {
		return nil, ErrNoRecipients
	}
	for _, addr := range to {
		if strings.TrimSpace(addr) == "" {
			return nil, ErrNoRecipients
		}
		if err := ensureNoCRLF(addr); err != nil {
			return nil, fmt.Errorf("To address %q: %w", addr, err)
		}
	}

	var b strings.Builder
	writeHeader(&b, "From", from)
	writeHeader(&b, "To", strings.Join(to, ", "))
	writeHeader(&b, "Subject", buildSubject(inc))
	writeHeader(&b, "Date", date.Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", messageID(inc, date, from))
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", `text/plain; charset="utf-8"`)
	b.WriteString("\r\n")
	b.WriteString(buildBody(inc))
	return []byte(b.String()), nil
}

// ensureNoCRLF rejects header-critical fields (addresses) that contain a CR or
// LF, the classic email header-injection vector.
func ensureNoCRLF(s string) error {
	if strings.ContainsAny(s, "\r\n") {
		return ErrHeaderInjection
	}
	return nil
}

// stripCRLF removes CR and LF from a string so it can never break out of its
// header line.
func stripCRLF(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
}

// writeHeader appends "Name: value\r\n", defensively stripping CR/LF from the
// value.
func writeHeader(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(stripCRLF(value))
	b.WriteString("\r\n")
}

func buildSubject(inc warden.Incident) string {
	switch inc.Type {
	case warden.IncidentPeerDead:
		return fmt.Sprintf("[warden] peer %s DEAD (term %d)", inc.Peer.ID, inc.Term)
	case warden.IncidentPeerRecovered:
		return fmt.Sprintf("[warden] peer %s recovered", inc.Peer.ID)
	default:
		return fmt.Sprintf("[warden] peer %s incident %s (term %d)", inc.Peer.ID, inc.Type, inc.Term)
	}
}

func buildBody(inc warden.Incident) string {
	var b strings.Builder
	b.WriteString("A warden incident was detected on the candacenet fleet.\r\n\r\n")
	fmt.Fprintf(&b, "Incident:     %s\r\n", inc.Type)
	fmt.Fprintf(&b, "Incident ID:  %s\r\n", inc.ID)
	fmt.Fprintf(&b, "Peer:         %s (%s)\r\n", inc.Peer.ID, inc.Peer.Addr)
	fmt.Fprintf(&b, "Reported by:  %s (leader)\r\n", inc.ReportedBy)
	fmt.Fprintf(&b, "Term:         %d\r\n", inc.Term)
	fmt.Fprintf(&b, "Detected at:  %s\r\n", core.FormatTimeOrNever(inc.DetectedAt))
	fmt.Fprintf(&b, "Last seen:    %s\r\n", core.FormatTimeOrNever(inc.LastSeen))
	b.WriteString("\r\n")
	b.WriteString(inc.Message)
	b.WriteString("\r\n")
	return b.String()
}

// messageID builds a deterministic, well-formed Message-ID from the incident
// ID and date. The local part is sanitized to safe atext characters and the
// domain is taken from the From address.
func messageID(inc warden.Incident, date time.Time, from string) string {
	local := sanitizeMsgIDPart(inc.ID)
	if local == "" {
		local = "incident"
	}
	return fmt.Sprintf("<%s.%d@%s>", local, date.UnixNano(), hostFromAddress(from))
}

func sanitizeMsgIDPart(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.' || r == '-' || r == '_' || r == '/':
			return r
		default:
			return '_'
		}
	}, s)
}

func hostFromAddress(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 && i < len(addr)-1 {
		if host := strings.TrimSpace(stripCRLF(addr[i+1:])); host != "" {
			return host
		}
	}
	return "warden.local"
}

func ehloName() string {
	if h, err := os.Hostname(); err == nil {
		if h = strings.TrimSpace(stripCRLF(h)); h != "" {
			return h
		}
	}
	return "localhost"
}
