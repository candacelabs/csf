// Copyright 2026 Candace Labs

package views_test

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/views"
)

// sample is one series a tally reports through Series.
type sample struct {
	Name   string
	Value  float64
	Labels []string
}

// samplesOf collects what a tally emits for one run key, in emission order.
func samplesOf(tally *views.Tally, key views.RunKey) []sample {
	var collected []sample
	tally.Series(key, func(name string, value float64, labels ...string) {
		collected = append(collected, sample{Name: name, Value: value, Labels: slices.Clone(labels)})
	})
	return collected
}

var _ = Describe("Tally.Series", func() {
	key := views.RunKey{Agent: "csf-metrics", Executor: "claudecode", Model: "claude-opus-5-5", Slice: "S1"}
	run := func(leading ...string) []string {
		return append(slices.Clone(leading), "csf-metrics", "claudecode", "claude-opus-5-5", "S1")
	}

	It("reports turns, cost, every token kind, then each gate, refusal and check, all labelled by the run", func() {
		tally := &views.Tally{
			Turns:    3,
			CostUSD:  1.5,
			Tokens:   map[views.TokenKind]int64{views.TokensUncached: 4, views.TokensCacheWrite: 1},
			Gates:    map[views.GateKey]int64{{Gate: "reply", Decision: "deny"}: 2},
			Refusals: map[string]int64{"commitment": 1},
			Checks:   map[views.CheckKey]int64{{Action: "merge", Outcome: views.OutcomeFailed}: 1},
		}

		Expect(samplesOf(tally, key)).To(Equal([]sample{
			{Name: views.MetricTurns, Value: 3, Labels: run()},
			{Name: views.MetricCost, Value: 1.5, Labels: run()},
			{Name: views.MetricTokens, Value: 4, Labels: run(string(views.TokensUncached))},
			{Name: views.MetricTokens, Value: 1, Labels: run(string(views.TokensCacheWrite))},
			{Name: views.MetricTokens, Value: 0, Labels: run(string(views.TokensCacheRead))},
			{Name: views.MetricTokens, Value: 0, Labels: run(string(views.TokensOutput))},
			{Name: views.MetricGateDecisions, Value: 2, Labels: run("reply", "deny")},
			{Name: views.MetricReplyRefusals, Value: 1, Labels: run("commitment")},
			{Name: views.MetricMergeChecks, Value: 1, Labels: run("merge", views.OutcomeFailed)},
		}))
	})

	It("still reports the zero counters for a run that added nothing", func() {
		tally := &views.Tally{}

		Expect(samplesOf(tally, key)).To(Equal([]sample{
			{Name: views.MetricTurns, Value: 0, Labels: run()},
			{Name: views.MetricCost, Value: 0, Labels: run()},
			{Name: views.MetricTokens, Value: 0, Labels: run(string(views.TokensUncached))},
			{Name: views.MetricTokens, Value: 0, Labels: run(string(views.TokensCacheWrite))},
			{Name: views.MetricTokens, Value: 0, Labels: run(string(views.TokensCacheRead))},
			{Name: views.MetricTokens, Value: 0, Labels: run(string(views.TokensOutput))},
		}))
	})
})
