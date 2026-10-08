// Copyright 2026 Candace Labs

package evaluate_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/evaluate"
)

// lengthEvaluator scores a candidate by its length and cannot measure the
// empty one; it records every candidate it was asked about.
type lengthEvaluator struct {
	asked []string
}

var errEmptyCandidate = errors.New("empty candidate")

func (evaluator *lengthEvaluator) Evaluate(_ context.Context, candidate string) (int, error) {
	evaluator.asked = append(evaluator.asked, candidate)
	if candidate == "" {
		return 0, errEmptyCandidate
	}
	return len(candidate), nil
}

var _ evaluate.IEvaluator[string, int] = (*lengthEvaluator)(nil)

var _ = Describe("Each", func() {
	It("measures every candidate in order, one row each", func() {
		evaluator := &lengthEvaluator{}
		rows := evaluate.Each(context.Background(), evaluator, []string{"a", "abc"})
		Expect(rows).To(Equal([]evaluate.Measured[string, int]{
			{Candidate: "a", Result: 1},
			{Candidate: "abc", Result: 3},
		}))
	})

	It("fails only the row of a candidate the evaluator cannot measure", func() {
		evaluator := &lengthEvaluator{}
		rows := evaluate.Each(context.Background(), evaluator, []string{"", "ab"})
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].Err).To(MatchError(errEmptyCandidate))
		Expect(rows[1]).To(Equal(evaluate.Measured[string, int]{Candidate: "ab", Result: 2}))
	})

	It("asks nothing once the context is cancelled and fails every row with its cause", func() {
		evaluator := &lengthEvaluator{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rows := evaluate.Each(ctx, evaluator, []string{"a", "b"})
		Expect(evaluator.asked).To(BeEmpty())
		Expect(rows).To(HaveLen(2))
		for _, row := range rows {
			Expect(row.Err).To(MatchError(context.Canceled))
		}
	})

	It("returns no rows for no candidates", func() {
		Expect(evaluate.Each(context.Background(), &lengthEvaluator{}, nil)).To(BeEmpty())
	})
})
