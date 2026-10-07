// Copyright 2026 Candace Labs

package stub_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/stub"
)

type observation struct{ distance int }

var _ = Describe("The canned brain", func() {
	It("proposes the same actions for every decision, with no model", func() {
		var brain model.IBrain[observation, string] = stub.NewCannedBrain[observation]("stop", "report")
		near, err := brain.Propose(context.Background(), observation{distance: 1})
		Expect(err).NotTo(HaveOccurred())
		far, err := brain.Propose(context.Background(), observation{distance: 100})
		Expect(err).NotTo(HaveOccurred())
		Expect(near).To(Equal(far))
		Expect(near.Provider).To(Equal(stub.ProviderName))
		Expect(near.Actions).To(Equal([]string{"stop", "report"}))
	})

	It("hands out copies a caller cannot use to rewrite later proposals", func() {
		brain := stub.NewCannedBrain[observation]("stop")
		first, err := brain.Propose(context.Background(), observation{})
		Expect(err).NotTo(HaveOccurred())
		first.Actions[0] = "accelerate"
		second, err := brain.Propose(context.Background(), observation{})
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Actions).To(Equal([]string{"stop"}))
	})

	It("answers no proposal when it has nothing canned", func() {
		_, err := stub.NewCannedBrain[observation, string]().Propose(context.Background(), observation{})
		Expect(err).To(MatchError(model.ErrNoProposal))
	})

	It("honours a cancelled context", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := stub.NewCannedBrain[observation]("stop").Propose(ctx, observation{})
		Expect(err).To(MatchError(context.Canceled))
	})
})
