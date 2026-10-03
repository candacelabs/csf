package opencode

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OpenCode configuration", func() {
	It("splits the explicit model only at its first provider separator", func() {
		model, err := parsePromptModel("openrouter/openai/gpt-5.4-nano")
		Expect(err).NotTo(HaveOccurred())
		Expect(model).To(Equal(promptModel{ProviderID: "openrouter", ModelID: "openai/gpt-5.4-nano"}))
	})

	It("rejects a model that names no provider", func() {
		_, err := parsePromptModel("gpt-5.4-nano")
		Expect(err).To(MatchError(ErrModel))
	})
})
