// Copyright 2026 Candace Labs

package model_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model"
)

var _ = Describe("A proposal", func() {
	It("yields its single action", func() {
		proposal := &model.Proposal[string]{Provider: "test", Actions: []string{"open the gripper"}}
		action, err := proposal.Only()
		Expect(err).NotTo(HaveOccurred())
		Expect(action).To(Equal("open the gripper"))
	})

	It("reports an empty or missing proposal as no proposal", func() {
		_, err := (&model.Proposal[string]{Provider: "test"}).Only()
		Expect(err).To(MatchError(model.ErrNoProposal))
		var missing *model.Proposal[string]
		_, err = missing.Only()
		Expect(err).To(MatchError(model.ErrNoProposal))
	})

	It("refuses to pick one of several actions on the caller's behalf", func() {
		_, err := (&model.Proposal[int]{Provider: "test", Actions: []int{1, 2}}).Only()
		var ambiguous *model.AmbiguousProposalError
		Expect(err).To(BeAssignableToTypeOf(ambiguous))
		Expect(err).To(MatchError(`brain "test" proposed 2 actions where one was expected`))
	})
})
