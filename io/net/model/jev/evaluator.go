// Copyright 2026 Candace Labs

package jev

import (
	"context"
	"errors"
	"fmt"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

// ErrNoDecisionCandidates reports a candidate list that names no declared model.
var ErrNoDecisionCandidates = errors.New("jev: no declared decision model to evaluate")

// ModelEvaluator measures a declared decision model on the committed held-out
// tree questions: the evaluate contract's criteria are the corpus, never the
// model. It unloads every model after its run, so the next candidate loads
// into a free GPU.
type ModelEvaluator struct {
	client    iohttp.IHTTPClient
	options   []OllamaOption
	questions []TreeQuestion
	unloaded  func(model string, err error)
}

// ModelEvaluatorOption configures a [ModelEvaluator].
type ModelEvaluatorOption func(evaluator *ModelEvaluator) error

// WithEvaluatorEndpoint asks the Ollama server at endpoint instead of
// [DefaultOllamaEndpoint].
func WithEvaluatorEndpoint(endpoint string) ModelEvaluatorOption {
	return func(evaluator *ModelEvaluator) error {
		evaluator.options = append(evaluator.options, WithOllamaEndpoint(endpoint))
		return nil
	}
}

// WithUnloadFailures receives a model the server could not unload after a
// measured run; the run's result stands, since the measurement completed.
func WithUnloadFailures(report func(model string, err error)) ModelEvaluatorOption {
	return func(evaluator *ModelEvaluator) error {
		if report == nil {
			return fmt.Errorf("jev: nil unload report")
		}
		evaluator.unloaded = report
		return nil
	}
}

// NewModelEvaluator builds the evaluator over the committed held-out corpus.
func NewModelEvaluator(client iohttp.IHTTPClient, options ...ModelEvaluatorOption) (*ModelEvaluator, error) {
	questions, err := LoadTreeQuestions()
	if err != nil {
		return nil, err
	}
	evaluator := &ModelEvaluator{client: client, questions: questions, unloaded: func(string, error) {}}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("jev: nil evaluator option")
		}
		if err := option(evaluator); err != nil {
			return nil, err
		}
	}
	return evaluator, nil
}

// Evaluate runs the held-out corpus against one declared model.
func (evaluator *ModelEvaluator) Evaluate(ctx context.Context, candidate DeciderModel) (ModelResult, error) {
	decider, err := NewDecider(evaluator.client, candidate, evaluator.options...)
	if err != nil {
		return ModelResult{}, err
	}
	metrics, err := Evaluate(ctx, decider, evaluator.questions)
	if unloadErr := decider.Unload(ctx); unloadErr != nil && err == nil {
		evaluator.unloaded(candidate.Name, unloadErr)
	}
	if err != nil {
		return ModelResult{}, err
	}
	return ModelResult{Model: candidate.Name, Metrics: metrics}, nil
}

// EvaluationCandidates are the models to measure: the declared model name
// names, or every declared candidate when name is empty.
func EvaluationCandidates(name string) ([]DeciderModel, error) {
	if name == "" {
		if len(DecisionCandidates) == 0 {
			return nil, ErrNoDecisionCandidates
		}
		return DecisionCandidates, nil
	}
	model, err := DeclaredModel(name)
	if err != nil {
		return nil, err
	}
	return []DeciderModel{model}, nil
}
