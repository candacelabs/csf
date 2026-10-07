// Copyright 2026 Candace Labs

package sessiongate

import (
	"github.com/prometheus/client_golang/prometheus"
)

// The question gate's series: every question an agent put to the operator,
// by the gate that judged it, its class and its verdict, and every send by
// which the operator overrode a refusal. The blocked-question rate is the
// blocked share of all questions; overrides over blocked questions bound the
// gate's false-positive rate from below, since only a refusal the operator
// hears of can be overridden.
const (
	questionsMetric = "csf_harness_questions_total"
	overridesMetric = "csf_harness_question_overrides_total"

	labelGate    = "gate"
	labelClass   = "class"
	labelVerdict = "verdict"
)

// QuestionCollector exports the question gate's measurement, read afresh
// from the state directory at each scrape.
type QuestionCollector struct {
	state     string
	questions *prometheus.Desc
	overrides *prometheus.Desc
}

// NewQuestionCollector builds the collector over the runs under state.
func NewQuestionCollector(state string) *QuestionCollector {
	return &QuestionCollector{
		state:     state,
		questions: prometheus.NewDesc(questionsMetric, "Questions agents put to the operator, by the gate that judged them, their class and verdict.", []string{labelGate, labelClass, labelVerdict}, nil),
		overrides: prometheus.NewDesc(overridesMetric, "Sends by which the operator said they wanted a question the question gate refused.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (collector *QuestionCollector) Describe(output chan<- *prometheus.Desc) {
	output <- collector.questions
	output <- collector.overrides
}

// Collect implements prometheus.Collector.
func (collector *QuestionCollector) Collect(output chan<- prometheus.Metric) {
	counts, err := CountQuestions(collector.state)
	if err != nil {
		output <- prometheus.NewInvalidMetric(collector.questions, err)
		return
	}
	for key, count := range counts.Questions {
		output <- prometheus.MustNewConstMetric(collector.questions, prometheus.CounterValue, float64(count), key.Gate, key.Class, key.Verdict)
	}
	output <- prometheus.MustNewConstMetric(collector.overrides, prometheus.CounterValue, float64(counts.Overrides))
}
