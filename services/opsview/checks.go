// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/csf/prod/certificate"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The certificate panel: the choice-verification certificate a bootstrap
// archive carries (csf/prod/certificate), read from certificate.json under the
// state directory. It shows why the bootstrap's choices are right — the options
// it induced and the picks it made — one row per check, every threshold read
// from the data rather than typed in.
const (
	// CertificateRegion is the panel's region.
	CertificateRegion = "opsview.certificate"
	// EventCertificate carries the certificate, as its JSON, in the
	// certificate field. It is internal: the follow effect emits it, and a
	// browser may not.
	EventCertificate = "opsview.certificate"
	// FieldCertificate is the field EventCertificate carries the JSON in.
	FieldCertificate = "certificate"

	certificateTemplate = "certificate"
)

// CertificateRow is one certificate check, every number already rendered.
type CertificateRow struct {
	// Name is the check.
	Name string
	// Value is the measured value.
	Value string
	// Threshold is how the threshold was read: the value, the quantile it
	// sits at, and the size of the sample it was read from.
	Threshold string
	// Method is how the threshold was derived: "knee", or "none" when the
	// reference sample was too small to have a curve.
	Method string
	// Pass is the verdict.
	Pass bool
}

// CertificatePanel is what the view shows of the certificate: a pure
// projection of it, every number already rendered.
type CertificatePanel struct {
	Recorded bool
	Repo     string
	Revision string
	Created  string
	Pass     bool
	Checks   []CertificateRow
}

// equal compares two panels field by field.
func (panel CertificatePanel) equal(other CertificatePanel) bool {
	return panel.Recorded == other.Recorded && panel.Repo == other.Repo && panel.Revision == other.Revision &&
		panel.Created == other.Created && panel.Pass == other.Pass && slices.Equal(panel.Checks, other.Checks)
}

// certificatePanelOf renders one certificate.
func certificatePanelOf(cert certificate.Certificate) CertificatePanel {
	panel := CertificatePanel{Recorded: true, Repo: cert.Repo, Revision: cert.Revision, Created: cert.Created, Pass: cert.Pass}
	for _, check := range cert.PerCheck {
		panel.Checks = append(panel.Checks, CertificateRow{
			Name:      check.Name,
			Value:     fmt.Sprintf("%.3f", check.Value),
			Threshold: fmt.Sprintf("%.3f at quantile %.2f of %d", check.Derivation.Threshold, check.Derivation.Quantile, check.Derivation.Samples),
			Method:    check.Derivation.Method,
			Pass:      check.Pass,
		})
	}
	return panel
}

// decodeCertificate reads a certificate off the wire. One that does not decode
// leaves the panel as it was.
func decodeCertificate(text string) (CertificatePanel, bool) {
	var cert certificate.Certificate
	if json.Unmarshal([]byte(text), &cert) != nil {
		return CertificatePanel{}, false
	}
	return certificatePanelOf(cert), true
}

// CertificateEvent is the event the follow effect emits when the certificate
// changed, addressed to the panel's own region.
func CertificateEvent(content []byte) live.Event {
	return live.Event{Name: EventCertificate, FragmentID: CertificateRegion, Fields: live.NewFields(map[string]string{FieldCertificate: string(content)})}
}

// certificateView is the panel region's data.
type certificateView struct {
	CertificatePanel
	Region string
}

// renderCertificate draws the panel from its projection.
func renderCertificate(state viewState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, certificateTemplate, certificateView{CertificatePanel: state.certificate, Region: CertificateRegion})
	})
}

// certificateChanged reports whether the panel's rendered values moved.
func certificateChanged(previous viewState, next viewState) bool {
	return !previous.certificate.equal(next.certificate)
}
