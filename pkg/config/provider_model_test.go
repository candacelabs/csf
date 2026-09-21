package config_test

import (
	"github.com/candacelabs/csf/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("provider model references", func() {
	It("preserves model IDs containing path separators", func() {
		reference, err := config.ParseProviderModel("openrouter/openai/gpt-5.4-nano")
		Expect(err).NotTo(HaveOccurred())
		Expect(reference).To(Equal(config.ProviderModel{
			ProviderID: "openrouter",
			ModelID:    "openai/gpt-5.4-nano",
		}))
	})

	DescribeTable("rejects incomplete references",
		func(value string) {
			_, err := config.ParseProviderModel(value)
			Expect(err).To(MatchError("must be providerID/modelID"))
		},
		Entry("no separator", "model"),
		Entry("empty provider", "/model"),
		Entry("empty model", "provider/"),
	)
})
