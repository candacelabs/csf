// Copyright 2026 Candace Labs

package jev_test

import (
	"context"

	"github.com/candacelabs/csf/io/net/model/jev"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Held-out eval", func() {
	Describe("the committed corpus", func() {
		It("loads every question with the answer the tree declares", func() {
			questions, err := jev.LoadTreeQuestions()
			Expect(err).NotTo(HaveOccurred())
			Expect(questions).NotTo(BeEmpty())
			for _, question := range questions {
				Expect(question.Answer).NotTo(BeEmpty())
				Expect(question.Typed().Validate()).To(Succeed())
			}
		})
	})

	Describe("the committed table", func() {
		It("chooses the declared default decision model", func() {
			table, err := jev.LoadEvalTable()
			Expect(err).NotTo(HaveOccurred())
			chosen, err := jev.ChosenModel(table)
			Expect(err).NotTo(HaveOccurred())
			Expect(chosen).To(Equal(jev.DefaultDeciderModel))
		})

		It("carries each measured model's readout", func() {
			table, err := jev.LoadEvalTable()
			Expect(err).NotTo(HaveOccurred())
			Expect(table).NotTo(BeEmpty())
			for _, row := range table {
				_, err := jev.DeclaredModel(row.Model)
				Expect(err).NotTo(HaveOccurred(), "row %s is not a declared model", row.Model)
				Expect(row.Metrics.Accuracy).To(BeNumerically(">=", 0))
				Expect(row.Metrics.Accuracy).To(BeNumerically("<=", 1))
				Expect(row.Metrics.ECE).To(BeNumerically(">=", 0))
				Expect(row.Metrics.P50Latency).To(BeNumerically(">", 0))
				Expect(row.Metrics.DecisionsPerSecond).To(BeNumerically(">", 0))
			}
		})
	})

	Describe("ChosenModel", func() {
		It("takes the best accuracy within the measured latency knee", func() {
			table := jev.EvalTable{
				{Model: "jevk5_9b_q4", Metrics: jev.EvalMetrics{Accuracy: 0.80, P50Latency: 0.10}},
				{Model: "jev_omni_12b_q4", Metrics: jev.EvalMetrics{Accuracy: 0.95, P50Latency: 0.15}},
			}
			chosen, err := jev.ChosenModel(table)
			Expect(err).NotTo(HaveOccurred())
			Expect(chosen.Name).To(Equal("jev_omni_12b_q4"))
		})

		It("refuses a candidate whose latency is past the knee", func() {
			table := jev.EvalTable{
				{Model: "jevk5_9b_q4", Metrics: jev.EvalMetrics{Accuracy: 0.80, P50Latency: 0.10}},
				{Model: "jev_omni_12b_q4", Metrics: jev.EvalMetrics{Accuracy: 0.95, P50Latency: 0.25}},
			}
			chosen, err := jev.ChosenModel(table)
			Expect(err).NotTo(HaveOccurred())
			Expect(chosen.Name).To(Equal("jevk5_9b_q4"))
		})

		It("keeps the earlier candidate when accuracies tie", func() {
			table := jev.EvalTable{
				{Model: "jevk5_9b_q4", Metrics: jev.EvalMetrics{Accuracy: 0.90, P50Latency: 0.10}},
				{Model: "jev_omni_12b_q4", Metrics: jev.EvalMetrics{Accuracy: 0.90, P50Latency: 0.12}},
			}
			chosen, err := jev.ChosenModel(table)
			Expect(err).NotTo(HaveOccurred())
			Expect(chosen.Name).To(Equal("jevk5_9b_q4"))
		})

		It("reports a table with no measured latency", func() {
			_, err := jev.ChosenModel(jev.EvalTable{{Model: "jevk5_9b_q4"}})
			Expect(err).To(MatchError(jev.ErrNoMeasuredModel))
		})
	})

	Describe("Evaluate", func() {
		It("measures accuracy, calibration and throughput over a corpus", func() {
			controller := gomock.NewController(GinkgoT())
			defer controller.Finish()
			distribution := &jev.Distribution{
				Question:      jev.Noul("the sky is blue"),
				Answers:       []string{"false", "true"},
				Probabilities: []float64{0.1, 0.9},
			}
			decider := NewMockIDecider(controller)
			decider.EXPECT().Decide(gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]*jev.Distribution{distribution}, nil).Times(2)
			questions := []jev.TreeQuestion{
				{State: "the sky is blue", Kind: jev.KindNoul, Question: "is the sky blue?", Answer: "true"},
				{State: "the sky is blue", Kind: jev.KindNoul, Question: "is the sky green?", Answer: "true"},
			}

			metrics, err := jev.Evaluate(context.Background(), decider, questions)

			Expect(err).NotTo(HaveOccurred())
			Expect(metrics.Accuracy).To(Equal(1.0))
			Expect(metrics.ECE).To(BeNumerically("~", 0.1, 1e-9))
			Expect(metrics.P50Latency).To(BeNumerically(">=", 0))
			Expect(metrics.DecisionsPerSecond).To(BeNumerically(">=", 0))
		})

		It("refuses an empty corpus", func() {
			controller := gomock.NewController(GinkgoT())
			defer controller.Finish()
			_, err := jev.Evaluate(context.Background(), NewMockIDecider(controller), nil)
			Expect(err).To(MatchError(jev.ErrNoEvalQuestions))
		})
	})
})
