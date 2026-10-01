package copilotadapter

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	adapterconfig "github.com/candacelabs/csf/services/copilot-adapter/config"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
)

var _ = Describe("the declared configuration", func() {
	It("parses the checked-in defaults and holds them to the contract", func() {
		config := DefaultAdapterConfig()

		Expect(copilotv1.ValidateAdapterConfig(config)).To(Succeed())
		Expect(config.GetEventStreamPollMillis()).To(BeNumerically(">", 0))
		Expect(config.GetEventStreamPageSize()).To(BeNumerically(">", 0))
		Expect(config.GetDefaultPageLimit()).To(BeNumerically(">", 0))
	})

	It("refuses a configuration the contract's refinements reject", func() {
		resolved := configuration{}

		err := WithConfig(&copilotv1.AdapterConfig{
			EventStreamPollMillis: 1,
			EventStreamPageSize:   200,
			DefaultPageLimit:      50,
		})(&resolved)

		Expect(err).To(MatchError(ContainSubstring("event_stream_poll_millis")))
		Expect(resolved.config).To(BeNil())
	})

	It("refuses a document that does not satisfy its own contract", func() {
		_, err := adapterconfig.ParseAdapterConfig([]byte(`{"eventStreamPollMillis": 250, "eventStreamPageSize": 200, "defaultPageLimit": 100000}`))

		Expect(err).To(MatchError(ContainSubstring("default_page_limit")))
	})

	It("projects typed configuration values without redeclaring their defaults", func() {
		configuration := &copilotv1.AdapterConfig{
			EventStreamPollMillis:          500,
			EventStreamPageSize:            17,
			DefaultPageLimit:               3,
			DurableTransitionTimeoutMillis: 2500,
			AssistantDeltaMaxBytes:         2048,
			AssistantDeltaFlushMillis:      75,
			AssistantDeltaMaxEvents:        9,
			AssistantDeltaSourceMaxBytes:   4096,
		}

		Expect(adapterconfig.EventStreamPollInterval(configuration).Milliseconds()).To(Equal(int64(500)))
		Expect(adapterconfig.EventStreamPageSize(configuration)).To(Equal(int32(17)))
		Expect(adapterconfig.DefaultPageLimit(configuration)).To(Equal(int32(3)))
		Expect(adapterconfig.DurableTransitionTimeout(configuration).Milliseconds()).To(Equal(int64(2500)))
		Expect(adapterconfig.AssistantDeltaMaxBytes(configuration)).To(Equal(2048))
		Expect(adapterconfig.AssistantDeltaFlushInterval(configuration).Milliseconds()).To(Equal(int64(75)))
		Expect(adapterconfig.AssistantDeltaMaxEvents(configuration)).To(Equal(9))
		Expect(adapterconfig.AssistantDeltaSourceMaxBytes(configuration)).To(Equal(4096))
	})
})
