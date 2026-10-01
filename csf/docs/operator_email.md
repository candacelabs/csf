# Operator email with Spine provenance

`SendEmail` is the generated authenticated MCP operation. The caller supplies
only `message.subject` and `message.text`. Recipients, sender, runtime metadata,
receipt storage and SMTP settings are host-owned. The plain unauthenticated MCP
and HTTP routes cannot send mail. Other Go compositions can mount the same
`services/email.Mailer` through `csf.WithEmail`; no additional process is needed.

The CSF binary accepts `--operator-email-config /private/email.json` alongside
`--agent-mcp-key-file`. Both the configuration and optional SMTP-password file
must be regular owner-only files. Example configuration (not credentials):

```json
{
  "smtpHost": "smtp.example.invalid",
  "smtpPort": 587,
  "smtpUsername": "csf@example.invalid",
  "smtpPasswordFile": "/private/smtp-password",
  "sender": "csf@example.invalid",
  "recipients": ["operator@example.invalid"],
  "receiptDirectory": "/private/email-receipts",
  "reportingNode": {"nodeId": "node-a", "address": "203.0.113.10"},
  "csfVersion": "0.1.0",
  "sourceRevision": "the-exact-built-source-revision",
  "sessionProvider": "copilot",
  "containerObservationsFile": "/private/container-observations.json",
  "links": [
    {"kind": "EVIDENCE_LINK_KIND_WORKBENCH", "label": "Workbench", "url": "https://workbench.example.invalid/ui/"},
    {"kind": "EVIDENCE_LINK_KIND_GRAFANA", "label": "Email dashboard", "url": "https://grafana.example.invalid/d/csf-email"},
    {"kind": "EVIDENCE_LINK_KIND_CSF", "label": "CSF", "url": "https://github.com/candacelabs/csf"}
  ]
}
```

The binary observes its own hostname if omitted. Build version/revision and
provider are deployment-supplied facts, not guessed from a model. Only the
authenticated session making this operation is included; the service does not
scrape other ChatGPT/Copilot sessions. Absent provider information is unknown.

An optional existing authorized observer may atomically publish protobuf JSON
`candace.provenance.v1.ReceiptMetadata` with `schemaVersion: 1`, `recordedAt` and
`containers`. Each container supplies its node/location, name, image, actual
state, `observedAt` and direct log/trace/Grafana links. This feature does not
install that observer or add Docker authority. Only observations at most five
minutes old are included; future, absent, invalid or stale snapshots become an
explicit collection gap. Identity and session claims in the file are ignored.
Links must be host-configured HTTP(S) URLs, without embedded credentials.

## Evidence and instrumentation

Every send automatically renders a compact **Spine provenance** footer in
HTML and plain text. The versioned metadata is also retained with the message
hash and bounded outcome in the private receipt directory. The message body,
recipient list and SMTP password are not retained in receipts. An UNKNOWN
attempt is persisted before transport; the final receipt replaces it. A crash
can leave UNKNOWN and must not trigger a blind resend. SMTP ACCEPTED does not
prove delivery to an inbox or that the operator read it.

The host must retain the receipt directory on its existing persistent private
storage, and back it up or prune it under its retention policy. Nothing deletes
receipts automatically. Disk/full or pre-send persistence failure prevents the
send; a final persistence failure remains an explicit error/uncertainty boundary.

`/metrics` exposes bounded `csf_email_send_total{outcome}` and
`csf_email_send_duration_seconds{outcome}` series. Receipt/session IDs stay out
of labels. The [native Grafana dashboard](../observability/email-dashboard.json)
has UID `csf-email`, panels 1–2. Provision it through the operator's existing
Grafana workflow and select the CSF scrape instance. Missing data is Unknown;
metrics are not an exact delivery ledger. Importing this source does not deploy
the dashboard, SMTP settings, observer or email capability.
