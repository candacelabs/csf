package copilotadapter

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
		_, err := parseAdapterConfig([]byte(`{"eventStreamPollMillis": 250, "eventStreamPageSize": 200, "defaultPageLimit": 100000}`))

		Expect(err).To(MatchError(ContainSubstring("default_page_limit")))
	})

	It("reads the stream's poll interval and page size from the declaration, not from source", func() {
		adapter := &CopilotAdapter{config: &copilotv1.AdapterConfig{
			EventStreamPollMillis: 500,
			EventStreamPageSize:   17,
			DefaultPageLimit:      3,
		}}

		Expect(adapter.eventStreamPoll().Milliseconds()).To(Equal(int64(500)))
		Expect(adapter.eventStreamPage()).To(Equal(int32(17)))
		Expect(adapter.defaultPageLimit()).To(Equal(int32(3)))
	})
})
