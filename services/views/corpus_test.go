// Copyright 2026 Candace Labs

package views_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/views"
)

var _ = Describe("FoldRun", func() {
	It("sums per-turn usage, adds each cost rise and a restarted process's whole cost, and counts gates, refusals, checks and operator hours", func() {
		run := views.FoldRun("run-1", session.RunState{AgentID: "csf-metrics", Model: "claude-opus-5-5"}, []byte(eventLog))

		Expect(run.Agent).To(Equal("csf-metrics"))
		Expect(run.Model).To(Equal("claude-opus-5-5"))
		Expect(run.Executor).To(Equal("claudecode"))
		Expect(run.Total.Turns).To(Equal(int64(3)))
		Expect(run.Total.CostUSD).To(BeNumerically("~", 1.75, 1e-9))
		Expect(run.Total.Tokens).To(Equal(map[views.TokenKind]int64{
			views.TokensUncached: 3, views.TokensCacheWrite: 2, views.TokensCacheRead: 13, views.TokensOutput: 5,
		}))
		Expect(run.Total.Gates).To(Equal(map[views.GateKey]int64{{Gate: "reply", Decision: "deny"}: 1, {Gate: "commit", Decision: "allow"}: 1}))
		Expect(run.Total.Refusals).To(Equal(map[string]int64{"commitment": 1}))
		Expect(run.Total.Checks).To(Equal(map[views.CheckKey]int64{
			{Action: "merge", Outcome: views.OutcomeFailed}: 1, {Action: "ready", Outcome: views.OutcomeOK}: 1,
		}))
		Expect(run.Hours).To(HaveLen(2))
		Expect(run.Hours[at(10, 0)].CostUSD).To(BeNumerically("~", 1.5, 1e-9))
		Expect(run.Hours[at(11, 0)].CostUSD).To(BeNumerically("~", 0.25, 1e-9))
		Expect(run.OperatorHours).To(Equal(map[time.Time]bool{at(11, 0): true}))
	})

	It("folds an empty log to nothing", func() {
		run := views.FoldRun("run-0", session.RunState{}, nil)
		Expect(run.Total.Turns).To(BeZero())
		Expect(run.Hours).To(BeEmpty())
	})
})
