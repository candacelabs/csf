// Copyright 2026 Candace Labs

package views_test

import (
	"bytes"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/views"
)

const fixtureLabels = `agent="csf-metrics",executor="claudecode",model="claude-opus-5-5",slice="CSF-METRICS"`

func fixtureHistory() views.History {
	run := views.FoldRun("run-1", session.RunState{AgentID: "csf-metrics", Model: "claude-opus-5-5"}, []byte(eventLog))
	return views.History{
		Measurement: &views.Measurement{Runs: []views.Run{run}, OperatorHours: []time.Time{at(11, 0)}},
		Slices:      map[string]string{"run-1": "CSF-METRICS"},
	}
}

func backfill(history views.History, from time.Time, through time.Time) (string, int) {
	var document bytes.Buffer
	samples, err := views.WriteBackfill(&document, history, from, through)
	Expect(err).NotTo(HaveOccurred())
	return document.String(), samples
}

var _ = Describe("WriteBackfill", func() {
	It("samples each counter as it stood at the end of every hour, and at the instant it ends, as OpenMetrics", func() {
		document, samples := backfill(fixtureHistory(), at(0, 0), at(12, 30))

		Expect(samples).To(Equal(41))
		Expect(document).To(ContainSubstring("# TYPE csf_cost_usd counter\n" +
			"csf_cost_usd_total{" + fixtureLabels + "} 1.5 1.7911116e+09\n" +
			"csf_cost_usd_total{" + fixtureLabels + "} 1.75 1.7911152e+09\n" +
			"csf_cost_usd_total{" + fixtureLabels + "} 1.75 1.791117e+09\n"))
		Expect(document).To(ContainSubstring(`csf_reply_gate_refusals_total{rule="commitment",` + fixtureLabels + "} 1.0 1.7911152e+09\n"))
		Expect(document).To(ContainSubstring(`csf_operator_attention_hours_total{source="runs"} 1.0 1.7911152e+09`))
		Expect(document).To(HaveSuffix("# EOF\n"))
	})

	It("writes nothing at or before from, so a later backfill continues where the last one stopped", func() {
		document, _ := backfill(fixtureHistory(), at(12, 0), at(12, 30))
		Expect(document).NotTo(ContainSubstring(" 1.7911152e+09"))
		Expect(document).To(ContainSubstring("csf_session_turns_total{" + fixtureLabels + "} 3.0 1.791117e+09"))
		Expect(document).NotTo(ContainSubstring(`source="hand_labeled"`))
	})

	It("samples the hand-labeled window as nothing at its start and every correction at its end", func() {
		document, _ := backfill(views.History{}, at(0, 0), at(30, 0))
		Expect(document).To(ContainSubstring(`csf_operator_corrections_total{class="#145",source="hand_labeled"} 0.0 1.7910828e+09`))
		Expect(document).To(ContainSubstring(`csf_operator_corrections_total{class="#145",source="hand_labeled"} 4.0 1.7911659e+09`))
		Expect(document).To(ContainSubstring(`csf_operator_attention_hours_total{source="hand_labeled"} 7.0 1.7911659e+09`))
	})
})
