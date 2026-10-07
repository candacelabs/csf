// Copyright 2026 Candace Labs

package sessiongate_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
)

var _ = Describe("the question gate's counts", func() {
	var state string

	runLog := func(run string, lines ...string) {
		directory := filepath.Join(state, run)
		Expect(os.MkdirAll(directory, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(directory, session.EventsFile), []byte(strings.Join(lines, "\n")+"\n"), 0o600)).To(Succeed())
	}

	BeforeEach(func() {
		state = GinkgoT().TempDir()
		runLog("a",
			`{"event_type":"session_gate_decision","gate":"reply","decision":"deny","questions":[{"class":"magnitude","blocked":true},{"class":"meaning","blocked":false}]}`,
			`{"event_type":"session_gate_decision","gate":"reply","decision":"limit","questions":[{"class":"magnitude","blocked":true}]}`,
			`{"event_type":"session_gate_decision","gate":"reply","decision":"allow"}`,
			`{"event_type":"harness_control_action","action":"send","question_wanted":true}`,
			`{"event_type":"harness_control_action","action":"send","question_wanted":false}`)
		runLog("b",
			`{"event_type":"session_gate_decision","gate":"question","decision":"deny","questions":[{"class":"re-litigation","blocked":true}]}`,
			"not a record")
	})

	It("counts every question by gate, class and verdict, and every override, across the runs", func() {
		counts, err := sessiongate.CountQuestions(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(counts).To(Equal(sessiongate.QuestionCounts{
			Questions: map[sessiongate.QuestionKey]int{
				{Gate: "reply", Class: "magnitude", Verdict: sessiongate.VerdictBlocked}:        1,
				{Gate: "reply", Class: "magnitude", Verdict: sessiongate.VerdictLimit}:          1,
				{Gate: "reply", Class: "meaning", Verdict: sessiongate.VerdictPassed}:           1,
				{Gate: "question", Class: "re-litigation", Verdict: sessiongate.VerdictBlocked}: 1,
			},
			Overrides: 1,
		}))
	})

	It("exports them as counters", func() {
		registry := prometheus.NewRegistry()
		registry.MustRegister(sessiongate.NewQuestionCollector(state))
		families, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())
		exported := map[string][]float64{}
		for _, family := range families {
			for _, metric := range family.GetMetric() {
				exported[family.GetName()] = append(exported[family.GetName()], metric.GetCounter().GetValue())
			}
		}
		Expect(exported).To(Equal(map[string][]float64{
			"csf_harness_questions_total":          {1, 1, 1, 1},
			"csf_harness_question_overrides_total": {1},
		}))
	})

	It("is empty for a state directory with no runs", func() {
		counts, err := sessiongate.CountQuestions(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(counts).To(Equal(sessiongate.QuestionCounts{Questions: map[sessiongate.QuestionKey]int{}}))
	})
})

// rulingSeries gathers the rulings series by enforcement.
func rulingSeries(state string) (map[string]float64, error) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(sessiongate.NewRulingCollector(state))
	families, err := registry.Gather()
	series := map[string]float64{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				series[label.GetValue()] = metric.GetGauge().GetValue()
			}
		}
	}
	return series, err
}

var _ = Describe("the rulings series", func() {
	It("exports the rulings in force by whether a gate enforces them, zero of each with no records", func() {
		state := GinkgoT().TempDir()
		series, err := rulingSeries(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(series).To(Equal(map[string]float64{sessiongate.EnforcementEnforced: 0, sessiongate.EnforcementUnenforced: 0}))

		for _, ruling := range []session.Ruling{
			{ID: "gated", Statement: "s", Quote: "q", RuledOn: "2026-10-05", EnforcedBy: "a gate"},
			{ID: "pending", Statement: "s", Quote: "q", RuledOn: "2026-10-05", PendingGate: "a gate to come"},
			{ID: "bare", Statement: "s", Quote: "q", RuledOn: "2026-10-05"},
		} {
			Expect(session.AppendRuling(state, ruling)).To(Succeed())
		}
		series, err = rulingSeries(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(series).To(Equal(map[string]float64{sessiongate.EnforcementEnforced: 1, sessiongate.EnforcementUnenforced: 2}))
	})

	It("reports records it cannot read as an error rather than as zero rulings", func() {
		state := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(state, session.RulingsFile), []byte("not json\n"), 0o600)).To(Succeed())
		_, err := rulingSeries(state)
		Expect(err).To(MatchError(ContainSubstring("decode ruling")))
	})
})
