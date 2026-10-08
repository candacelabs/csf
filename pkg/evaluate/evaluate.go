// Copyright 2026 Candace Labs

// Package evaluate is the ontology's evaluate as a contract: compare a
// candidate's result with criteria chosen independently of the candidate. The
// criteria are a held-out set the evaluator owns; a candidate never supplies
// them. A csf build replayed on the rotated ticket suite and a decision model
// asked the committed tree questions are two evaluators of one shape, and csf
// eval score runs either through [Each].
package evaluate

import "context"

// IEvaluator measures one candidate against the held-out set it owns.
type IEvaluator[Candidate any, Result any] interface {
	Evaluate(ctx context.Context, candidate Candidate) (Result, error)
}

// Measured is one candidate's row: its result, or the error that left it
// unmeasured.
type Measured[Candidate any, Result any] struct {
	Candidate Candidate
	Result    Result
	Err       error
}

// Each evaluates every candidate in order. A candidate the evaluator cannot
// measure fails its own row and never hides the others; a cancelled context
// fails every row not yet measured without asking the evaluator.
func Each[Candidate any, Result any](ctx context.Context, evaluator IEvaluator[Candidate, Result], candidates []Candidate) []Measured[Candidate, Result] {
	rows := make([]Measured[Candidate, Result], 0, len(candidates))
	for _, candidate := range candidates {
		row := Measured[Candidate, Result]{Candidate: candidate}
		if err := ctx.Err(); err != nil {
			row.Err = err
		} else {
			row.Result, row.Err = evaluator.Evaluate(ctx, candidate)
		}
		rows = append(rows, row)
	}
	return rows
}
