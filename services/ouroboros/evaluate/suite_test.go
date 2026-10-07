// Copyright 2026 Candace Labs

package evaluate_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

var _ = Describe("Selecting the suite", func() {
	// pool is twenty tickets whose original runs read 100 to 290 calls.
	pool := func() []evaluate.Ticket {
		var tickets []evaluate.Ticket
		for index := range 20 {
			tickets = append(tickets, ticket(int64(100+index), int64(100+10*index), int64(1+index%4)))
		}
		return tickets
	}

	It("holds out half the eligible pool when more replays are needed, and records why", func() {
		suite, err := evaluate.Select(pool(), nil, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		Expect(suite.Tickets).To(HaveLen(10))
		Expect(suite.Derivation.Pool).To(Equal(20))
		Expect(suite.Derivation.Needed).To(BeNumerically(">", 10))
		Expect(suite.Derivation.SizeFrom).To(ContainSubstring("held-out half"))
		Expect(suite.Derivation.Resolves).To(BeNumerically(">", 1))
	})

	It("takes the budget from the shortest tenth of the original runs", func() {
		suite, err := evaluate.Select(pool(), nil, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		// The calls are 100, 110, ... 290; the p10 index of 20 is 1.
		Expect(suite.Derivation.Budget).To(Equal(110))
	})

	It("draws the same tickets for a version and fresh ones for the next", func() {
		first, err := evaluate.Select(pool(), nil, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		again, err := evaluate.Select(pool(), nil, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		next, err := evaluate.Select(pool(), nil, 2, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Tickets).To(Equal(first.Tickets))
		Expect(next.Tickets).NotTo(Equal(first.Tickets))
	})

	It("rotates a week after selection, and a missing suite is always due", func() {
		suite, err := evaluate.Select(pool(), nil, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		Expect(suite.RotatesAt).To(Equal(specStart.Add(evaluate.RotationPeriod)))
		Expect(suite.Due(specStart.Add(evaluate.RotationPeriod - 1))).To(BeFalse())
		Expect(suite.Due(specStart.Add(evaluate.RotationPeriod))).To(BeTrue())
		var none *evaluate.Suite
		Expect(none.Due(specStart)).To(BeTrue())
	})

	It("never holds a ticket the loop evolves the harness on, and records the split", func() {
		evolution := []int64{100, 101, 102, 103, 104, 105, 106, 107, 108, 109}
		suite, err := evaluate.Select(pool(), evolution, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		for _, held := range suite.Tickets {
			Expect(evolution).NotTo(ContainElement(held.Number))
		}
		Expect(suite.EvolutionSet).To(Equal(evolution))
		Expect(suite.Derivation.Evolution).To(Equal(10))
		Expect(evaluate.Disjoint(suite, evolution)).To(Succeed())
	})

	It("records the replay model and the replay tool rules, none of which reaches past the replay", func() {
		suite, err := evaluate.Select(pool(), nil, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		Expect(suite.Model).To(Equal("claude-opus-5-5"))
		Expect(suite.Tools).To(Equal(evaluate.ReplayTools))
		for _, rule := range suite.Tools {
			Expect(rule).NotTo(ContainSubstring("mcp__"))
			Expect(rule).NotTo(ContainSubstring("git push"))
			Expect(rule).NotTo(ContainSubstring("gh pr merge"))
			Expect(rule).NotTo(ContainSubstring("csf"))
		}
	})

	It("refuses an overlap with the evolution tickets with the defined error", func() {
		suite, err := evaluate.Select(pool(), nil, 1, "claude-opus-5-5", specStart)
		Expect(err).NotTo(HaveOccurred())
		Expect(evaluate.Disjoint(suite, []int64{suite.Tickets[0].Number})).To(MatchError(evaluate.ErrNotDisjoint))
	})

	It("refuses a pool with no eligible ticket with the defined error", func() {
		_, err := evaluate.Select(nil, nil, 1, "claude-opus-5-5", specStart)
		Expect(err).To(MatchError(evaluate.ErrEmptyPool))
		_, err = evaluate.Select([]evaluate.Ticket{ticket(7, 0, 0)}, nil, 1, "claude-opus-5-5", specStart)
		Expect(err).To(MatchError(evaluate.ErrEmptyPool))
	})
})
