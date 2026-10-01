package redact_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/redact"
)

var _ = Describe("Redactor", func() {
	It("removes raw and URL-userinfo-escaped spellings", func() {
		redactor := redact.NewRedactor("credential@example:with space")

		result := redactor.String(
			"raw=credential@example:with space escaped=credential%40example%3Awith%20space",
		)

		Expect(result).To(Equal("raw=[REDACTED] escaped=[REDACTED]"))
	})

	It("removes overlapping values without exposing a longer suffix", func() {
		redactor := redact.NewRedactor("short-value", "short-value-with-suffix")

		Expect(redactor.String("short-value-with-suffix short-value")).
			To(Equal("[REDACTED] [REDACTED]"))
	})

	It("owns an immutable snapshot of the declared values", func() {
		secrets := []string{"first-value"}
		redactor := redact.NewRedactor(secrets...)
		secrets[0] = "later-value"

		Expect(redactor.String("first-value later-value")).
			To(Equal("[REDACTED] later-value"))
	})

	It("leaves text unchanged when no effective values were declared", func() {
		const diagnostic = "connection refused"

		Expect(redact.Redactor{}.String(diagnostic)).To(Equal(diagnostic))
		Expect(redact.NewRedactor("", "").String(diagnostic)).To(Equal(diagnostic))
	})
})
