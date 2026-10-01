# Shared email capability

`email.NewMailer(options...)` is a composable library. The host supplies the
SMTP transport, sender and operator recipients, runtime provenance, receipt
retention, clock, and optional Prometheus registry. Callers supply only an
`email.v1.EmailMessage`; the Mailer adds a small text and HTML footer labelled
`Spine provenance`.

## Delivery and receipt evidence

`Send` writes an `UNKNOWN` pre-attempt receipt before network I/O and replaces
it with the final outcome. A sink failure before transport is `FAILED` and safe
to retry. A crash after remote acceptance but before final replacement leaves
`UNKNOWN`; blindly retrying can duplicate mail. `ACCEPTED` means SMTP accepted
DATA, not inbox delivery or reading.

`FileReceiptSink` stores one owner-only (`0600`) deterministic protobuf file at
`<private-directory>/<receipt_id>.pb`, atomically replacing the same path. The
schema is `proto/candace/email/v1/email.proto`; files contain the bounded
`EmailReceipt` only, never message bodies, SMTP passwords, senders, or
recipients. Credential-bearing and userinfo URLs are removed before retention.
The sink does not prune records: the host owns retention and must keep the
directory private (`0700`).

## Instrumentation contract

The optional metrics are:

- `csf_email_send_total{outcome}`
- `csf_email_send_duration_seconds{outcome}`

The only outcome label values are `accepted`, `failed`, and `unknown`. Raw SMTP
errors, message text, addresses, and session identifiers never appear in metric
labels. The root composition owns the dashboard/panel contract in
`csf/observability/email-dashboard.json`; host configuration is documented in
`csf/docs/operator_email.md`. This library exports no server or panel.
OpenTelemetry span `email.send` is emitted through the installed global tracer
provider, or is a no-op when none is installed.
