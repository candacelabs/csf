package copilotadapter

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

var _ = Describe("session event variants", func() {
	It("loads and builds only the variant selected by the stored event kind", func() {
		firstCalls, otherCalls := 0, 0
		expected := errors.New("selected variant failed")
		err := versionedSessionEvent(true, "missing", api.SessionEventKindTurnStarted, api.SessionEventKindTurnStarted, func() (string, error) {
			return "payload", nil
		}, func(payload string) error {
			Expect(payload).To(Equal("payload"))
			firstCalls++
			return expected
		}, func(payload string) error {
			otherCalls++
			return nil
		})
		Expect(err).To(MatchError(expected))
		Expect(firstCalls).To(Equal(1))
		Expect(otherCalls).To(BeZero())

		err = versionedSessionEvent(true, "missing", api.SessionEventKindTurnCompleted, api.SessionEventKindTurnStarted, func() (string, error) {
			return "payload", nil
		}, func(payload string) error {
			firstCalls++
			return nil
		}, func(payload string) error {
			Expect(payload).To(Equal("payload"))
			otherCalls++
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(firstCalls).To(Equal(1))
		Expect(otherCalls).To(Equal(1))
	})

	It("rejects a missing reference without loading a version", func() {
		loaded := false
		err := versionedSessionEvent(false, "missing reference", api.SessionEventKindTurnStarted, api.SessionEventKindTurnStarted, func() (string, error) {
			loaded = true
			return "", nil
		}, func(payload string) error {
			return nil
		}, func(payload string) error {
			return nil
		})
		Expect(err).To(MatchError("missing reference"))
		Expect(loaded).To(BeFalse())
	})
})
