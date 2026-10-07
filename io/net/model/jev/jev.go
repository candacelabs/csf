// Copyright 2026 Candace Labs

// Package jev asks JEV-9B typed decisions. JEV-9B (autotrust/JEV-9B,
// Apache-2.0) answers a typed question about a piece of state text in one
// prefill pass, without generating text, and returns a calibrated probability
// for every allowed answer:
//
//   - [KindNoul]: is this statement true? Answers "false" and "true".
//   - [KindChoice]: which of these 2 to 16 options? One answer per option.
//   - [KindScore]: where on an ordered 0 to 5 scale? Answers "0" to "5".
//
// The model runs outside this process; each transport lives beside the
// shared types here and reaches it through the http capability its
// constructor receives. A distribution is the model's belief, not a
// decision the caller has made: acting on it, and below which confidence to
// stop, belong to the caller.
package jev

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind is the type of one question; the values are the model's own names.
type Kind string

const (
	// KindNoul asks whether a statement is true.
	KindNoul Kind = "noul"
	// KindChoice asks which of 2 to 16 options applies.
	KindChoice Kind = "choice"
	// KindScore asks for a level on an ordered 0 to 5 scale.
	KindScore Kind = "score"
)

const (
	// MinChoiceOptions and MaxChoiceOptions bound a choice question, as the
	// model's 16-slot choice head does.
	MinChoiceOptions = 2
	MaxChoiceOptions = 16
	// MaxScoreLevel is the top of the score scale; the bottom is 0.
	MaxScoreLevel = 5

	answerFalse = "false"
	answerTrue  = "true"
)

var (
	// ErrNoState reports a decision asked about empty state text.
	ErrNoState = errors.New("jev: the state text is empty")
	// ErrNoQuestions reports a decision with no questions.
	ErrNoQuestions = errors.New("jev: at least one question is required")
	// ErrInvalidQuestion reports a question the model cannot answer.
	ErrInvalidQuestion = errors.New("jev: invalid question")
)

// Question is one typed question about the state. Options are read only for
// [KindChoice]; the other kinds have fixed answers.
type Question struct {
	Kind     Kind
	Question string
	Options  []string
}

// Noul asks whether statement is true of the state.
func Noul(statement string) Question {
	return Question{Kind: KindNoul, Question: statement}
}

// Choice asks which of options applies to the state.
func Choice(question string, options ...string) Question {
	return Question{Kind: KindChoice, Question: question, Options: options}
}

// Score asks where the state sits on a 0 to 5 scale that question describes.
func Score(question string) Question {
	return Question{Kind: KindScore, Question: question}
}

// Answers returns the answers the model chooses between, in the order its
// distribution reports them.
func (question Question) Answers() []string {
	switch question.Kind {
	case KindNoul:
		return []string{answerFalse, answerTrue}
	case KindScore:
		levels := make([]string, 0, MaxScoreLevel+1)
		for level := 0; level <= MaxScoreLevel; level++ {
			levels = append(levels, strconv.Itoa(level))
		}
		return levels
	}
	return question.Options
}

// Validate reports whether the model can answer question. A choice option
// must be non-empty, free of surrounding space and unique.
func (question Question) Validate() error {
	return validateQuestion(question, MaxChoiceOptions)
}

// validateQuestion is [Question.Validate] with the choice bound named, so a
// transport that reduces a wider choice itself admits one.
func validateQuestion(question Question, maxOptions int) error {
	if strings.TrimSpace(question.Question) == "" {
		return fmt.Errorf("%w: the question text is empty", ErrInvalidQuestion)
	}
	switch question.Kind {
	case KindNoul, KindScore:
		return nil
	case KindChoice:
		return validateOptions(question.Options, maxOptions)
	}
	return fmt.Errorf("%w: unknown kind %q", ErrInvalidQuestion, question.Kind)
}

func validateOptions(options []string, maxOptions int) error {
	if len(options) < MinChoiceOptions || len(options) > maxOptions {
		return fmt.Errorf("%w: a choice needs %d to %d options, got %d",
			ErrInvalidQuestion, MinChoiceOptions, maxOptions, len(options))
	}
	seen := make(map[string]bool, len(options))
	for _, option := range options {
		if option == "" || option != strings.TrimSpace(option) {
			return fmt.Errorf("%w: option %q is empty or has surrounding space", ErrInvalidQuestion, option)
		}
		if seen[option] {
			return fmt.Errorf("%w: option %q appears twice", ErrInvalidQuestion, option)
		}
		seen[option] = true
	}
	return nil
}

// Distribution is the model's calibrated probability for each answer to one
// question, aligned with [Question.Answers].
type Distribution struct {
	Question      Question
	Answers       []string
	Probabilities []float64
}

// Top returns the most probable answer and its probability; the earlier
// answer wins a tie.
func (distribution *Distribution) Top() (string, float64) {
	best := 0
	for index, probability := range distribution.Probabilities {
		if probability > distribution.Probabilities[best] {
			best = index
		}
	}
	return distribution.Answers[best], distribution.Probabilities[best]
}

// Expected returns the probability-weighted mean level of a score question.
// It is meaningful only for [KindScore].
func (distribution *Distribution) Expected() float64 {
	expected := 0.0
	for level, probability := range distribution.Probabilities {
		expected += float64(level) * probability
	}
	return expected
}
