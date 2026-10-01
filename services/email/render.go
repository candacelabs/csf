package email

import (
	"bytes"
	"fmt"
	"html/template"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"

	emailv1 "github.com/candacelabs/csf/proto/candace/email/v1"
	provenancev1 "github.com/candacelabs/csf/proto/candace/provenance/v1"
)

const unknownValue = "unknown"

var htmlMessageTemplate = template.Must(template.New("email").Parse(`<!doctype html>
<html><body>
<div style="white-space: pre-wrap">{{.Message}}</div>
<div style="border-top: 1px solid #d0d0d0; color: #666; font-size: 11px; line-height: 1.35; margin-top: 16px; padding-top: 8px">
<strong>Spine provenance</strong><br>
Receipt: {{.ReceiptID}}<br>
Recorded: {{.RecordedAt}}<br>
Reporting node: {{.ReportingNode}}<br>
CSF version: {{.CSFVersion}}<br>
Source revision: {{.SourceRevision}}<br>
{{if .Sessions}}Sessions:<ul>{{range .Sessions}}<li>{{.Label}}{{if .URL}} — <a href="{{.URL}}">session</a>{{end}}</li>{{end}}</ul>{{end}}
{{if .Containers}}Containers:<ul>{{range .Containers}}<li>{{.Text}}{{range .Links}} — <a href="{{.URL}}">{{.Label}}</a>{{end}}</li>{{end}}</ul>{{end}}
{{if .Links}}Evidence:<ul>{{range .Links}}<li>{{.Label}}{{if .URL}} — <a href="{{.URL}}">open</a>{{end}}</li>{{end}}</ul>{{end}}
{{if .Unavailable}}Unavailable:<ul>{{range .Unavailable}}<li>{{.}}</li>{{end}}</ul>{{end}}
</div>
</body></html>`))

type address struct {
	header   string
	envelope string
}

type renderedLink struct {
	Label string
	URL   string
}

type renderedContainer struct {
	Text  string
	Links []renderedLink
}

type provenanceView struct {
	Message        string
	ReceiptID      string
	RecordedAt     string
	ReportingNode  string
	CSFVersion     string
	SourceRevision string
	Sessions       []renderedLink
	Containers     []renderedContainer
	Links          []renderedLink
	Unavailable    []string
}

func addressFromMail(value *mail.Address) address {
	return address{header: value.String(), envelope: value.Address}
}

func renderMessage(
	from address,
	to []address,
	message *emailv1.EmailMessage,
	metadata *provenancev1.ReceiptMetadata,
) ([]byte, error) {
	view := buildProvenanceView(message.GetText(), metadata)
	plain := renderPlain(view)
	var html bytes.Buffer
	if err := htmlMessageTemplate.Execute(&html, view); err != nil {
		return nil, fmt.Errorf("rendering HTML email: %w", err)
	}

	boundary := "csf-" + strings.ReplaceAll(metadata.GetReceiptId(), "-", "")
	headers := []string{
		"From: " + from.header,
		"To: " + joinedHeaders(to),
		"Subject: " + mime.QEncoding.Encode("utf-8", sanitizeHeader(message.GetSubject())),
		"Date: " + metadata.GetRecordedAt().AsTime().UTC().Format(time.RFC1123Z),
		"Message-ID: <" + metadata.GetReceiptId() + "@" + addressHost(from.envelope) + ">",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="` + boundary + `"`,
	}

	var raw bytes.Buffer
	raw.WriteString(strings.Join(headers, "\r\n"))
	raw.WriteString("\r\n\r\n")
	writer := multipart.NewWriter(&raw)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, fmt.Errorf("setting MIME boundary: %w", err)
	}
	if err := writeMIMEPart(writer, "text/plain", []byte(plain)); err != nil {
		return nil, err
	}
	if err := writeMIMEPart(writer, "text/html", html.Bytes()); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("closing MIME message: %w", err)
	}
	return raw.Bytes(), nil
}

func writeMIMEPart(writer *multipart.Writer, mediaType string, content []byte) error {
	headers := make(textproto.MIMEHeader)
	headers.Set("Content-Type", mediaType+`; charset="utf-8"`)
	headers.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := writer.CreatePart(headers)
	if err != nil {
		return fmt.Errorf("creating %s MIME part: %w", mediaType, err)
	}
	encoded := quotedprintable.NewWriter(part)
	if _, err := encoded.Write([]byte(normalizeCRLF(string(content)))); err != nil {
		_ = encoded.Close()
		return fmt.Errorf("writing %s MIME part: %w", mediaType, err)
	}
	if err := encoded.Close(); err != nil {
		return fmt.Errorf("closing %s MIME part: %w", mediaType, err)
	}
	return nil
}

func normalizeCRLF(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func joinedHeaders(addresses []address) string {
	values := make([]string, 0, len(addresses))
	for _, value := range addresses {
		values = append(values, value.header)
	}
	return strings.Join(values, ",\r\n\t")
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func addressHost(value string) string {
	parts := strings.Split(value, "@")
	return parts[len(parts)-1]
}

func buildProvenanceView(message string, metadata *provenancev1.ReceiptMetadata) provenanceView {
	view := provenanceView{
		Message:        message,
		ReceiptID:      valueOrUnknown(metadata.GetReceiptId()),
		RecordedAt:     unknownValue,
		ReportingNode:  nodeText(metadata.GetReportingNode()),
		CSFVersion:     valueOrUnknown(metadata.GetCsfVersion()),
		SourceRevision: valueOrUnknown(metadata.GetSourceRevision()),
		Unavailable:    append([]string(nil), metadata.GetUnavailable()...),
	}
	if metadata.GetRecordedAt() != nil {
		view.RecordedAt = metadata.GetRecordedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	for _, session := range metadata.GetSessions() {
		text := session.GetProvider() + "/" + session.GetSessionId()
		if session.GetAgentId() != "" {
			text += " (agent " + session.GetAgentId() + ")"
		}
		view.Sessions = append(view.Sessions, renderedLink{Label: text, URL: safeURL(session.GetUrl())})
	}
	for _, container := range metadata.GetContainers() {
		rendered := renderedContainer{Text: containerText(container)}
		for _, link := range container.GetLinks() {
			rendered.Links = append(rendered.Links, renderLink(link))
		}
		view.Containers = append(view.Containers, rendered)
	}
	for _, link := range metadata.GetLinks() {
		view.Links = append(view.Links, renderLink(link))
	}
	sort.Strings(view.Unavailable)
	return view
}

func renderPlain(view provenanceView) string {
	var body strings.Builder
	body.WriteString(strings.ReplaceAll(view.Message, "\r\n", "\n"))
	body.WriteString("\n\n---\nSpine provenance\n")
	fmt.Fprintf(&body, "Receipt: %s\n", view.ReceiptID)
	fmt.Fprintf(&body, "Recorded: %s\n", view.RecordedAt)
	fmt.Fprintf(&body, "Reporting node: %s\n", view.ReportingNode)
	fmt.Fprintf(&body, "CSF version: %s\n", view.CSFVersion)
	fmt.Fprintf(&body, "Source revision: %s\n", view.SourceRevision)
	writePlainLinks(&body, "Sessions", view.Sessions)
	if len(view.Containers) > 0 {
		body.WriteString("Containers:\n")
		for _, container := range view.Containers {
			fmt.Fprintf(&body, "- %s\n", container.Text)
			writePlainLinks(&body, "  Evidence", container.Links)
		}
	}
	writePlainLinks(&body, "Evidence", view.Links)
	if len(view.Unavailable) > 0 {
		body.WriteString("Unavailable:\n")
		for _, unavailable := range view.Unavailable {
			fmt.Fprintf(&body, "- %s\n", unavailable)
		}
	}
	return body.String()
}

func writePlainLinks(builder *strings.Builder, title string, links []renderedLink) {
	if len(links) == 0 {
		return
	}
	builder.WriteString(title)
	builder.WriteString(":\n")
	for _, link := range links {
		fmt.Fprintf(builder, "- %s", link.Label)
		if link.URL != "" {
			fmt.Fprintf(builder, " — %s", link.URL)
		} else {
			builder.WriteString(" — URL omitted")
		}
		builder.WriteByte('\n')
	}
}

func renderLink(link *provenancev1.EvidenceLink) renderedLink {
	return renderedLink{Label: link.GetLabel(), URL: safeURL(link.GetUrl())}
}

func containerText(container *provenancev1.ContainerObservation) string {
	text := container.GetName()
	if container.GetImage() != "" {
		text += " image=" + container.GetImage()
	}
	text += " state=" + container.GetState().String()
	if node := nodeText(container.GetNode()); node != unknownValue {
		text += " node=" + node
	}
	if container.GetObservedAt() != nil {
		text += " observed=" + container.GetObservedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	return text
}

func nodeText(node *provenancev1.NodeIdentity) string {
	if node == nil {
		return unknownValue
	}
	parts := make([]string, 0, 3)
	for _, value := range []string{node.GetNodeId(), node.GetHostname(), node.GetAddress()} {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return unknownValue
	}
	return strings.Join(parts, " / ")
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return unknownValue
	}
	return value
}

func safeURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	fragment := parsed.Fragment
	if _, query, found := strings.Cut(fragment, "?"); found {
		fragment = query
	}
	for _, rawQuery := range []string{parsed.RawQuery, fragment} {
		query, err := url.ParseQuery(rawQuery)
		if err != nil {
			return ""
		}
		for key := range query {
			if credentialQueryKey(key) {
				return ""
			}
		}
	}
	return parsed.String()
}

func credentialQueryKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
	switch normalized {
	case "token", "accesstoken", "idtoken", "refreshtoken", "apikey", "key", "password", "passwd",
		"secret", "clientsecret", "auth", "authorization", "credential", "credentials", "signature", "sig",
		"code", "xamzcredential", "xamzsignature", "xamzsecuritytoken":
		return true
	default:
		return false
	}
}
