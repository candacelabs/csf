// Package notify implements the warden.INotifier delivery backends used by the
// watchdog:
//
//   - SMTPNotifier sends incident emails over STARTTLS (production).
//   - LogNotifier writes a structured log line via core.Logger (used when SMTP
//     is not configured).
//   - FileNotifier appends one JSON-encoded incident per line to a file (used
//     by the e2e harness).
//
// (A test that wants to assert on calls rather than deliver anywhere uses the
// generated warden.INotifier mock instead; see services/warden/internal/mocks.)
//
// All notifiers are safe for concurrent use, which is the contract the watchdog
// relies on when it delivers from short-lived goroutines. The SMTP notifier
// fails closed: it refuses to transmit credentials or mail if the server does
// not advertise STARTTLS, so secrets never traverse a cleartext link. That
// refusal is deliberate behaviour, reported as ErrSTARTTLSRequired — a caller
// must not "fall back" to plaintext around it.
package notify
