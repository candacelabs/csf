// Copyright 2026 Candace Labs

package jev

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"regexp"
	"strings"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

// DefaultSpaceEndpoint is the model authors' public demo Space, which runs
// JEV-9B on Hugging Face ZeroGPU.
const DefaultSpaceEndpoint = "https://autotrust-jev-9b-decision-demo.hf.space"

const (
	// SpaceQuestionsPerCall is how many questions the Space answers in one call.
	SpaceQuestionsPerCall = 4

	decidePath   = "/gradio_api/call/decide"
	optionSplits = ",\n"

	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	bearerPrefix        = "Bearer "
	contentTypeJSON     = "application/json"

	eventPrefix   = "event:"
	dataPrefix    = "data:"
	eventComplete = "complete"
	eventError    = "error"
	// outputAnswers is the index of the answers JSON among the Space's outputs.
	outputAnswers = 2

	// maxAnswerBytes bounds one response body read; the Space's rendered
	// HTML for four questions is a few kilobytes.
	maxAnswerBytes = 1 << 20
)

// eventIdentifier is the shape of the Space's call identifier, checked before
// it becomes part of a URL path.
var eventIdentifier = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

var (
	// ErrNoClient reports a decider constructed without the http capability.
	ErrNoClient = errors.New("jev: an HTTP client capability is required")
	// ErrNoEndpoint reports a decider constructed without the Space's URL.
	ErrNoEndpoint = errors.New("jev: an endpoint is required")
	// ErrSpaceRefused reports a decision the Space answered with an error.
	ErrSpaceRefused = errors.New("jev: the Space refused the decision")
	// ErrAnswerShape reports a Space answer that does not cover the questions.
	ErrAnswerShape = errors.New("jev: the Space's answer does not match the questions")
)

// SpaceDecider asks typed decisions of JEV-9B through a Gradio Space's
// decide API, by default the authors' public demo. It holds no state
// between calls, so one value is safe to share.
type SpaceDecider struct {
	client   iohttp.IHTTPClient
	endpoint string
	token    string
}

// SpaceOption configures a [SpaceDecider].
type SpaceOption func(decider *SpaceDecider)

// WithSpaceToken sends a Hugging Face token with each call. A public Space
// answers without one, but ZeroGPU then counts the calls against the
// anonymous quota rather than the account's.
func WithSpaceToken(token string) SpaceOption {
	return func(decider *SpaceDecider) { decider.token = strings.TrimSpace(token) }
}

// NewSpaceDecider reaches the Space at endpoint, a URL with scheme and host.
// The caller owns the client's lifetime.
func NewSpaceDecider(client iohttp.IHTTPClient, endpoint string, options ...SpaceOption) (*SpaceDecider, error) {
	if client == nil {
		return nil, ErrNoClient
	}
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if trimmed == "" {
		return nil, ErrNoEndpoint
	}
	decider := &SpaceDecider{client: client, endpoint: trimmed}
	for _, option := range options {
		if option != nil {
			option(decider)
		}
	}
	return decider, nil
}

// Decide answers every question about state, one distribution per question
// in question order. Questions travel to the Space in groups of
// [SpaceQuestionsPerCall].
func (decider *SpaceDecider) Decide(ctx context.Context, state string, questions []Question) ([]*Distribution, error) {
	if strings.TrimSpace(state) == "" {
		return nil, ErrNoState
	}
	if len(questions) == 0 {
		return nil, ErrNoQuestions
	}
	for index, question := range questions {
		if err := validateSpaceQuestion(question); err != nil {
			return nil, fmt.Errorf("question %d: %w", index+1, err)
		}
	}
	distributions := make([]*Distribution, 0, len(questions))
	for start := 0; start < len(questions); start += SpaceQuestionsPerCall {
		group := questions[start:min(start+SpaceQuestionsPerCall, len(questions))]
		answered, err := decider.decideGroup(ctx, state, group)
		if err != nil {
			return nil, err
		}
		distributions = append(distributions, answered...)
	}
	return distributions, nil
}

// validateSpaceQuestion adds the Space's own limit to the model's: it reads
// choice options as one comma- or newline-separated field.
func validateSpaceQuestion(question Question) error {
	if err := question.Validate(); err != nil {
		return err
	}
	if question.Kind != KindChoice {
		return nil
	}
	for _, option := range question.Options {
		if strings.ContainsAny(option, optionSplits) {
			return fmt.Errorf("%w: option %q contains a comma or newline, which the Space reads as a separator",
				ErrInvalidQuestion, option)
		}
	}
	return nil
}

func (decider *SpaceDecider) decideGroup(ctx context.Context, state string, questions []Question) ([]*Distribution, error) {
	eventID, err := decider.submit(ctx, state, questions)
	if err != nil {
		return nil, err
	}
	payload, err := decider.await(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return alignAnswers(questions, payload)
}

// submit queues one call and returns the Space's event identifier. The
// Space's decide inputs are the state, then a (kind, question, options) row
// for each of its four question slots; an empty row is skipped.
func (decider *SpaceDecider) submit(ctx context.Context, state string, questions []Question) (string, error) {
	inputs := make([]string, 0, 1+3*SpaceQuestionsPerCall)
	inputs = append(inputs, state)
	for slot := 0; slot < SpaceQuestionsPerCall; slot++ {
		if slot >= len(questions) {
			inputs = append(inputs, "", "", "")
			continue
		}
		question := questions[slot]
		inputs = append(inputs, string(question.Kind), question.Question, strings.Join(question.Options, ", "))
	}
	body, err := json.Marshal(callRequest{Data: inputs})
	if err != nil {
		return "", fmt.Errorf("jev: encode request: %w", err)
	}
	content, err := decider.send(ctx, stdhttp.MethodPost, decider.endpoint+decidePath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	var queued callQueued
	if err := json.Unmarshal(content, &queued); err != nil || !eventIdentifier.MatchString(queued.EventID) {
		return "", fmt.Errorf("jev: the Space did not queue the call: %s", strings.TrimSpace(string(content)))
	}
	return queued.EventID, nil
}

// await reads the call's event stream to its end and returns the complete
// event's outputs, or the Space's error.
func (decider *SpaceDecider) await(ctx context.Context, eventID string) (json.RawMessage, error) {
	content, err := decider.send(ctx, stdhttp.MethodGet, decider.endpoint+decidePath+"/"+eventID, nil)
	if err != nil {
		return nil, err
	}
	event := ""
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64<<10), maxAnswerBytes)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, eventPrefix):
			event = strings.TrimSpace(strings.TrimPrefix(line, eventPrefix))
		case strings.HasPrefix(line, dataPrefix) && event == eventComplete:
			return json.RawMessage(strings.TrimSpace(strings.TrimPrefix(line, dataPrefix))), nil
		case strings.HasPrefix(line, dataPrefix) && event == eventError:
			return nil, spaceRefusal(strings.TrimSpace(strings.TrimPrefix(line, dataPrefix)))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("jev: read event stream: %w", err)
	}
	return nil, fmt.Errorf("jev: the event stream ended without a result")
}

func spaceRefusal(data string) error {
	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &refusal); err != nil || refusal.Error == "" {
		return fmt.Errorf("%w: %s", ErrSpaceRefused, data)
	}
	return fmt.Errorf("%w: %s", ErrSpaceRefused, refusal.Error)
}

func (decider *SpaceDecider) send(ctx context.Context, method string, address string, body io.Reader) ([]byte, error) {
	request, err := stdhttp.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, fmt.Errorf("jev: build request: %w", err)
	}
	if body != nil {
		request.Header.Set(headerContentType, contentTypeJSON)
	}
	if decider.token != "" {
		request.Header.Set(headerAuthorization, bearerPrefix+decider.token)
	}
	return exchange(request, decider.client)
}

// alignAnswers reads the answers JSON among the Space's outputs and orders
// each answer's probabilities by the question's answers.
func alignAnswers(questions []Question, outputs json.RawMessage) ([]*Distribution, error) {
	var rendered []json.RawMessage
	if err := json.Unmarshal(outputs, &rendered); err != nil || len(rendered) <= outputAnswers {
		return nil, fmt.Errorf("%w: unexpected outputs", ErrAnswerShape)
	}
	var encoded string
	if err := json.Unmarshal(rendered[outputAnswers], &encoded); err != nil {
		return nil, fmt.Errorf("%w: the answers are not a JSON string", ErrAnswerShape)
	}
	var answered spaceAnswers
	if err := json.Unmarshal([]byte(encoded), &answered); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnswerShape, err)
	}
	if len(answered.Answers) != len(questions) {
		return nil, fmt.Errorf("%w: %d answers for %d questions", ErrAnswerShape, len(answered.Answers), len(questions))
	}
	distributions := make([]*Distribution, 0, len(questions))
	for index, question := range questions {
		answers := question.Answers()
		probabilities := make([]float64, 0, len(answers))
		for _, answer := range answers {
			probability, ok := answered.Answers[index][answer]
			if !ok {
				return nil, fmt.Errorf("%w: question %d has no probability for %q", ErrAnswerShape, index+1, answer)
			}
			probabilities = append(probabilities, probability)
		}
		if len(answered.Answers[index]) != len(answers) {
			return nil, fmt.Errorf("%w: question %d has %d probabilities for %d answers",
				ErrAnswerShape, index+1, len(answered.Answers[index]), len(answers))
		}
		distributions = append(distributions, &Distribution{Question: question, Answers: answers, Probabilities: probabilities})
	}
	return distributions, nil
}

// The decide call as the Space's Gradio API reads and writes it.
type callRequest struct {
	Data []string `json:"data"`
}

type callQueued struct {
	EventID string `json:"event_id"`
}

type spaceAnswers struct {
	Answers []map[string]float64 `json:"answers"`
}
