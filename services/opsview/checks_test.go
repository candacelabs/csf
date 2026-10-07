// Copyright 2026 Candace Labs

package opsview_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf/prod/certificate"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/opsview"
)

// certificateFixture is a certificate as a bootstrap writes it: two checks, one
// reading its threshold from a knee in the reference sample and one too short
// to have a curve, and an overall pass.
func certificateFixture(pass bool) certificate.Certificate {
	GinkgoHelper()
	return certificate.Certificate{
		Repo: "candacelabs/csf_staging", Revision: "a29054e", Created: "2026-10-06T03:00:00Z",
		PerCheck: []certificate.Check{
			{Name: certificate.CheckCoverage, Value: 0.93, Pass: true,
				Derivation: certificate.Derivation{Method: "knee", Sample: "coverage per held-out fold", Samples: 6, Quantile: 0.67, Threshold: 0.41}},
			{Name: certificate.CheckMDL, Value: 0.22, Pass: pass,
				Derivation: certificate.Derivation{Method: "knee", Sample: "description length", Samples: 6, Quantile: 0.67, Threshold: 0.58}},
		},
		Pass: pass,
	}
}

func writeCertificate(directory string, cert certificate.Certificate) {
	GinkgoHelper()
	Expect(certificate.Write(filepath.Join(directory, certificate.FileName), cert)).To(Succeed())
}

func showsCheck(name string) func(html string) bool {
	return func(html string) bool { return strings.Contains(html, `data-opsview-check="`+name+`"`) }
}

var _ = Describe("The choice certificate panel", func() {
	var (
		directory string
		watcher   *specWatcher
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		watcher = newSpecWatcher(gomock.NewController(GinkgoT()))
	})

	It("shows every check with its data-derived threshold, and patches only itself when replaced", func() {
		writeCertificate(directory, certificateFixture(true))
		client := connect(mountView(directory, watcher.mock))
		client.Send(opsview.EventSection, opsview.CertificateRegion, map[string]string{opsview.FieldSection: "certificate"})
		panel := client.WaitFor(opsview.CertificateRegion, opened(showsCheck(certificate.CheckCoverage)))
		client.Ack(panel.Patch.ServerSeq)
		html, _ := panel.Patch.Fragment(opsview.CertificateRegion)
		Expect(html).To(ContainSubstring("candacelabs/csf_staging @ a29054e, built 2026-10-06T03:00:00Z"))
		Expect(html).To(ContainSubstring(`data-opsview-check="coverage" class="pass"`))
		Expect(html).To(ContainSubstring("<td>0.930</td><td>0.410 at quantile 0.67 of 6</td>"))
		Expect(html).To(ContainSubstring("Every required check passed."))

		writeCertificate(directory, certificateFixture(false))
		watcher.changes <- iofs.Change{Name: certificate.FileName, Op: iofs.ChangeCreated}
		panel = client.WaitFor(opsview.CertificateRegion, showsCheck(certificate.CheckMDL))
		Expect(panel.Patch.FragmentIDs()).To(Equal([]string{opsview.CertificateRegion}), "the board did not re-render")
		html, _ = panel.Patch.Fragment(opsview.CertificateRegion)
		Expect(html).To(ContainSubstring(`data-opsview-check="mdl" class="fail"`))
		Expect(html).To(ContainSubstring("at least one required check is below its threshold"))
	})

	It("says so when no certificate is written, and ignores one that does not decode", func() {
		Expect(os.WriteFile(filepath.Join(directory, certificate.FileName), []byte("not a certificate"), 0o600)).To(Succeed())
		client := connect(mountView(directory, watcher.mock))
		html := unfold(client, opsview.CertificateRegion, "certificate")
		Expect(html).To(ContainSubstring("No choice certificate yet"))
	})
})
