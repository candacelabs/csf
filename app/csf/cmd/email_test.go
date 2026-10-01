package main

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

var _ = Describe("operator email startup", func() {
	It("constructs the shared mail capability without contacting SMTP", func() {
		directory := GinkgoT().TempDir()
		path := filepath.Join(directory, "email.json")
		configuration := `{"smtpHost":"smtp.example.invalid","smtpPort":587,"sender":"csf@example.invalid","recipients":["operator@example.invalid"],"receiptDirectory":"` + filepath.Join(directory, "receipts") + `"}`
		Expect(os.WriteFile(path, []byte(configuration), 0600)).To(Succeed())
		mailer, err := configuredOperatorEmail(path, prometheus.NewRegistry())
		Expect(err).NotTo(HaveOccurred())
		Expect(mailer).NotTo(BeNil())
	})

	It("rejects public, oversized and unknown-field configuration before SMTP", func() {
		path := filepath.Join(GinkgoT().TempDir(), "email.json")
		Expect(os.WriteFile(path, []byte(`{"unknown":"secret"}`), 0600)).To(Succeed())
		_, err := configuredOperatorEmail(path, prometheus.NewRegistry())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("secret"))
		Expect(os.WriteFile(path, []byte(strings.Repeat("x", maxEmailConfigurationBytes+1)), 0600)).To(Succeed())
		_, err = configuredOperatorEmail(path, prometheus.NewRegistry())
		Expect(err).To(HaveOccurred())
		Expect(os.Chmod(path, 0644)).To(Succeed())
		_, err = configuredOperatorEmail(path, prometheus.NewRegistry())
		Expect(err).To(HaveOccurred())
	})
})
