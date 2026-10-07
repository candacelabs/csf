// Copyright 2026 Candace Labs

package jev_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model/jev"
)

var _ = Describe("The decision record", func() {
	It("appends a run, stamps it and round-trips through its file", func() {
		at := time.Date(2026, 10, 6, 5, 0, 0, 0, time.UTC)
		ledger := jev.DecideLedger{}.Record(jev.DecisionRun{At: at, Model: "jevk5_9b_q4", Decisions: 3, PromptTokens: 700, EvalTokens: 115})
		Expect(ledger.Runs).To(HaveLen(1))
		Expect(ledger.UpdatedAt.Equal(at)).To(BeTrue())

		ledger = ledger.Record(jev.DecisionRun{At: at.Add(time.Hour), Model: "jevk5_9b_q4", Decisions: 2, PromptTokens: 12, EvalTokens: 0})
		Expect(ledger.Runs).To(HaveLen(2))
		Expect(ledger.UpdatedAt.Equal(at.Add(time.Hour))).To(BeTrue(), "the stamp is the newest run's")

		content, err := ledger.Encode()
		Expect(err).NotTo(HaveOccurred())
		read := jev.ReadDecideLedger(content)
		Expect(read.Runs).To(HaveLen(2))
		Expect(read.Runs[0].Model).To(Equal("jevk5_9b_q4"))
		Expect(read.Runs[0].Decisions).To(Equal(int64(3)))
		Expect(read.Runs[0].Tokens()).To(Equal(int64(815)))
		Expect(read.Runs[1].Decisions).To(Equal(int64(2)))
		Expect(read.UpdatedAt.Equal(at.Add(time.Hour))).To(BeTrue())
	})

	It("counts only the runs of the day asked for", func() {
		ledger := jev.DecideLedger{}.
			Record(jev.DecisionRun{At: time.Date(2026, 10, 5, 23, 0, 0, 0, time.UTC), Decisions: 4, PromptTokens: 100, EvalTokens: 10}).
			Record(jev.DecisionRun{At: time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC), Decisions: 3, PromptTokens: 700, EvalTokens: 115})
		day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
		Expect(ledger.TokensOn(day)).To(Equal(int64(815)), "the earlier day's 110 tokens are not counted")
		Expect(ledger.DecisionsOn(day)).To(Equal(int64(3)))
		Expect(ledger.TokensOn(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))).To(Equal(int64(110)))
	})

	It("reads an empty or unreadable record as empty", func() {
		Expect(jev.ReadDecideLedger(nil).Runs).To(BeEmpty())
		Expect(jev.ReadDecideLedger([]byte(nil))).To(Equal(jev.DecideLedger{Runs: []jev.DecisionRun{}}))
		Expect(jev.ReadDecideLedger([]byte("not a record")).Runs).To(BeEmpty())
	})
})
