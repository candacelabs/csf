// Copyright 2026 Candace Labs

package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	stdhttp "net/http"
	"slices"
	"strings"
	"time"

	iohttp "github.com/candacelabs/csf/io/net/http"
)

// DefaultOllamaEndpoint is the local Ollama server's own default address. A
// decision model is served by one Ollama server; the caller names where it
// runs.
const DefaultOllamaEndpoint = "http://127.0.0.1:11434"

// MaxKnockoutOptions bounds a choice the knockout can reduce: options are
// scored in groups of [MaxChoiceOptions], one winner leaves each group, and
// the final round holds at most [MaxChoiceOptions] winners, so at most
// MaxChoiceOptions² options.
const MaxKnockoutOptions = MaxChoiceOptions * MaxChoiceOptions

const (
	generatePath = "/api/generate"

	// optionLetterBase is the first option letter: the model's 16-slot choice
	// head labels its options A..P, and one option letter is the model's whole
	// answer.
	optionLetterBase = 'A'
	// missingLetterMargin is how far below the lowest returned option letter a
	// letter its answer left out of the top logprobs sits, so an option the
	// model never ranked keeps a small share rather than none.
	missingLetterMargin = 2.0

	// topLogprobs is how many alternative tokens the server reports per
	// position: enough for all 16 option letters.
	topLogprobs = 20
	// predictTokens is how many tokens the readout needs: the answer letter is
	// the model's first token, so nothing is generated.
	predictTokens = 1

	// defaultKeepAlive keeps the model loaded briefly between questions, so a
	// batch of decisions reuses one load, then frees the shared GPU.
	defaultKeepAlive = 30 * time.Second
)

var (
	// ErrNoModel reports a decider constructed without a model name.
	ErrNoModel = errors.New("jev: an Ollama model name is required")
	// ErrInvalidOption reports a nil option or a value the decider cannot use.
	ErrInvalidOption = errors.New("jev: invalid option")
	// ErrLetterShape reports an Ollama answer that does not carry the option
	// letters this readout needs.
	ErrLetterShape = errors.New("jev: the Ollama answer does not carry the option letters")
)

// Usage is the tokens one decision run cost, as the server counted them: the
// prompt tokens it read and the tokens it generated.
type Usage struct {
	PromptTokens int64
	EvalTokens   int64
}

// Add sums two usages, token for token.
func (usage Usage) Add(other Usage) Usage {
	return Usage{
		PromptTokens: usage.PromptTokens + other.PromptTokens,
		EvalTokens:   usage.EvalTokens + other.EvalTokens,
	}
}

// Total is every token a run read or generated.
func (usage Usage) Total() int64 { return usage.PromptTokens + usage.EvalTokens }

// OllamaDecider answers typed decisions with a JEV GGUF model served by a
// local Ollama server. One decision is one prefill pass per question: the
// model's first token is the answer letter, and the letters' logprobs are the
// option scores. A choice wider than the model's 16 slots is reduced by a
// knockout. It holds no state between calls, so one value is safe to share.
type OllamaDecider struct {
	client              iohttp.IHTTPClient
	endpoint            string
	model               string
	temperature         float64
	knockoutTemperature float64
	keepAlive           time.Duration
}

// OllamaOption configures an [OllamaDecider].
type OllamaOption func(decider *OllamaDecider) error

// WithOllamaEndpoint reaches the server at endpoint, a URL with scheme and
// host, instead of [DefaultOllamaEndpoint].
func WithOllamaEndpoint(endpoint string) OllamaOption {
	return func(decider *OllamaDecider) error {
		trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if trimmed == "" {
			return fmt.Errorf("%w: empty endpoint", ErrInvalidOption)
		}
		decider.endpoint = trimmed
		return nil
	}
}

// WithOllamaTemperature sets the temperature the option letters' logprobs are
// re-tempered at: p ∝ exp(logprob / temperature). One is the model's own
// distribution; above one flattens it, below one sharpens it.
func WithOllamaTemperature(temperature float64) OllamaOption {
	return func(decider *OllamaDecider) error {
		if temperature <= 0 {
			return fmt.Errorf("%w: temperature must be positive, got %g", ErrInvalidOption, temperature)
		}
		decider.temperature = temperature
		return nil
	}
}

// WithOllamaKnockoutTemperature sets the temperature of the knockout's final
// round, which decides among the group winners.
func WithOllamaKnockoutTemperature(temperature float64) OllamaOption {
	return func(decider *OllamaDecider) error {
		if temperature <= 0 {
			return fmt.Errorf("%w: knockout temperature must be positive, got %g", ErrInvalidOption, temperature)
		}
		decider.knockoutTemperature = temperature
		return nil
	}
}

// WithOllamaKeepAlive sets how long the server keeps the model loaded after
// each question; zero leaves the server's own default in force.
func WithOllamaKeepAlive(stay time.Duration) OllamaOption {
	return func(decider *OllamaDecider) error {
		if stay < 0 {
			return fmt.Errorf("%w: keep-alive must not be negative, got %s", ErrInvalidOption, stay)
		}
		decider.keepAlive = stay
		return nil
	}
}

// NewOllamaDecider builds the decider for modelName on the local Ollama
// server client reaches. The client is the http capability, dialed by the
// binary that owns the process; the caller owns its lifetime.
func NewOllamaDecider(client iohttp.IHTTPClient, modelName string, options ...OllamaOption) (*OllamaDecider, error) {
	if client == nil {
		return nil, ErrNoClient
	}
	if strings.TrimSpace(modelName) == "" {
		return nil, ErrNoModel
	}
	decider := &OllamaDecider{
		client:              client,
		endpoint:            DefaultOllamaEndpoint,
		model:               modelName,
		temperature:         1,
		knockoutTemperature: 1,
		keepAlive:           defaultKeepAlive,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(decider); err != nil {
			return nil, err
		}
	}
	return decider, nil
}

// Model is the model name every decision is asked of.
func (decider *OllamaDecider) Model() string { return decider.model }

// Decide answers every question about state, one distribution per question in
// question order. Each question is one call; a choice wider than the model's
// 16 slots is answered by the knockout.
func (decider *OllamaDecider) Decide(ctx context.Context, state string, questions []Question) ([]*Distribution, error) {
	distributions, _, err := decider.DecideWithUsage(ctx, state, questions)
	return distributions, err
}

// DecideWithUsage answers every question about state as [OllamaDecider.Decide]
// does, and reports the tokens the server counted: the prompt tokens it read
// and the tokens it generated. A decision reads one token and generates
// nothing, so the eval count is what the server spent on the readout.
func (decider *OllamaDecider) DecideWithUsage(ctx context.Context, state string, questions []Question) ([]*Distribution, Usage, error) {
	var usage Usage
	if strings.TrimSpace(state) == "" {
		return nil, usage, ErrNoState
	}
	if len(questions) == 0 {
		return nil, usage, ErrNoQuestions
	}
	for index, question := range questions {
		if err := validateOllamaQuestion(question); err != nil {
			return nil, usage, fmt.Errorf("question %d: %w", index+1, err)
		}
	}
	distributions := make([]*Distribution, 0, len(questions))
	for _, question := range questions {
		probabilities, spent, err := decider.decideQuestion(ctx, state, question)
		if err != nil {
			return nil, usage, err
		}
		usage = usage.Add(spent)
		distributions = append(distributions, &Distribution{
			Question:      question,
			Answers:       question.Answers(),
			Probabilities: probabilities,
		})
	}
	return distributions, usage, nil
}

// validateOllamaQuestion is the shared validation with the knockout's wider
// choice bound named.
func validateOllamaQuestion(question Question) error {
	return validateQuestion(question, MaxKnockoutOptions)
}

// decideQuestion answers one question, by knockout when it is a choice wider
// than the model's 16 slots.
func (decider *OllamaDecider) decideQuestion(ctx context.Context, state string, question Question) ([]float64, Usage, error) {
	if question.Kind == KindChoice && len(question.Options) > MaxChoiceOptions {
		return decider.decideKnockout(ctx, state, question)
	}
	return decider.decideOptions(ctx, state, question, question.Answers(), decider.temperature)
}

// decideKnockout reduces a choice wider than the model's 16 slots: options are
// scored in groups of [MaxChoiceOptions] at the decider's temperature, one
// winner leaves each group, and the winners meet in one final round at the
// knockout temperature. A group's losers keep nothing, so every eliminated
// option's probability is zero and the final round's decide among the winners.
func (decider *OllamaDecider) decideKnockout(ctx context.Context, state string, question Question) ([]float64, Usage, error) {
	var usage Usage
	options := question.Options
	winners := make([]string, 0, len(options)/MaxChoiceOptions+1)
	for start := 0; start < len(options); start += MaxChoiceOptions {
		group := options[start:min(start+MaxChoiceOptions, len(options))]
		probabilities, spent, err := decider.decideOptions(ctx, state, question, group, decider.temperature)
		if err != nil {
			return nil, usage, err
		}
		usage = usage.Add(spent)
		winners = append(winners, group[argmax(probabilities)])
	}
	final, spent, err := decider.decideOptions(ctx, state, question, winners, decider.knockoutTemperature)
	if err != nil {
		return nil, usage, err
	}
	usage = usage.Add(spent)
	probabilities := make([]float64, len(options))
	for index, winner := range winners {
		probabilities[slices.Index(options, winner)] = final[index]
	}
	return probabilities, usage, nil
}

// decideOptions asks one call about labels and returns a probability per
// label, in label order, with the tokens the server spent on the call.
func (decider *OllamaDecider) decideOptions(ctx context.Context, state string, question Question, labels []string, temperature float64) ([]float64, Usage, error) {
	var usage Usage
	body, err := json.Marshal(decider.generateRequest(state, question, labels))
	if err != nil {
		return nil, usage, fmt.Errorf("jev: encode generate request: %w", err)
	}
	var answer generateResponse
	if err := decider.call(ctx, body, &answer); err != nil {
		return nil, usage, err
	}
	usage = Usage{PromptTokens: answer.PromptEvalCount, EvalTokens: answer.EvalCount}
	logprobs, err := optionLetterLogprobs(answer, len(labels))
	if err != nil {
		return nil, usage, err
	}
	probabilities, err := reTemper(logprobs, temperature)
	if err != nil {
		return nil, usage, err
	}
	return probabilities, usage, nil
}

// generateRequest is one /api/generate request: the state and question with
// the labelled options, asking for the answer letter. The readout reads one
// token's logprobs, so the model generates nothing.
func (decider *OllamaDecider) generateRequest(state string, question Question, labels []string) generateRequest {
	var prompt strings.Builder
	prompt.WriteString(strings.TrimSpace(state))
	prompt.WriteString("\n\n")
	prompt.WriteString(question.Question)
	prompt.WriteString("\n")
	for index, label := range labels {
		prompt.WriteString(optionLetter(index))
		prompt.WriteString(". ")
		prompt.WriteString(label)
		prompt.WriteString("\n")
	}
	prompt.WriteString("Answer:\n")
	stay := ""
	if decider.keepAlive > 0 {
		stay = decider.keepAlive.String()
	}
	return generateRequest{
		Model:       decider.model,
		Prompt:      prompt.String(),
		Raw:         true,
		Stream:      false,
		Logprobs:    true,
		TopLogprobs: topLogprobs,
		KeepAlive:   stay,
		Options:     generateOptions{NumPredict: predictTokens},
	}
}

// call sends one generate request and decodes its JSON answer into target.
func (decider *OllamaDecider) call(ctx context.Context, body []byte, target any) error {
	request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, decider.endpoint+generatePath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("jev: build request: %w", err)
	}
	request.Header.Set(headerContentType, contentTypeJSON)
	content, err := exchange(request, decider.client)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("jev: decode answer: %w", err)
	}
	return nil
}

// Unload asks the server to drop the model after the request in flight
// completes, so an eval ends by freeing the shared GPU at once rather than
// leaving the model loaded through its keep-alive window.
func (decider *OllamaDecider) Unload(ctx context.Context) error {
	request := decider.generateRequest("", Question{}, nil)
	request.KeepAlive = "0"
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("jev: encode unload request: %w", err)
	}
	var answer generateResponse
	return decider.call(ctx, body, &answer)
}

// optionLetter is the letter labelling the option at index.
func optionLetter(index int) string {
	return string(rune(optionLetterBase + index))
}

// optionLetterLogprobs reads the option letters' logprobs out of the answer's
// first generated token. A letter the model left out of its top logprobs sits
// [missingLetterMargin] below the lowest letter it did rank, so it keeps a
// small share rather than none.
func optionLetterLogprobs(answer generateResponse, count int) ([]float64, error) {
	if len(answer.Logprobs) == 0 {
		return nil, fmt.Errorf("%w: the answer carries no logprobs", ErrLetterShape)
	}
	ranked := make(map[string]float64, len(answer.Logprobs[0].TopLogprobs))
	for _, entry := range answer.Logprobs[0].TopLogprobs {
		ranked[entry.Token] = entry.Logprob
	}
	logprobs := make([]float64, count)
	missing := make([]bool, count)
	lowest := 0.0
	found := 0
	for index := 0; index < count; index++ {
		logprob, present := ranked[optionLetter(index)]
		if !present {
			missing[index] = true
			continue
		}
		if found == 0 || logprob < lowest {
			lowest = logprob
		}
		found++
		logprobs[index] = logprob
	}
	if found == 0 {
		return nil, fmt.Errorf("%w: none of the %d option letters %s..%s were ranked",
			ErrLetterShape, count, optionLetter(0), optionLetter(count-1))
	}
	for index := range logprobs {
		if missing[index] {
			logprobs[index] = lowest - missingLetterMargin
		}
	}
	return logprobs, nil
}

// reTemper turns the option letters' logprobs into a probability distribution
// at temperature: p ∝ exp(logprob / temperature), renormalized. The server
// reports logprobs at its own temperature, so this is where the readout's
// temperature applies.
func reTemper(logprobs []float64, temperature float64) ([]float64, error) {
	if temperature <= 0 {
		return nil, fmt.Errorf("%w: temperature must be positive, got %g", ErrInvalidOption, temperature)
	}
	probabilities := make([]float64, len(logprobs))
	total := 0.0
	for index, logprob := range logprobs {
		weight := math.Exp(logprob / temperature)
		probabilities[index] = weight
		total += weight
	}
	if total <= 0 || math.IsInf(total, 1) {
		return nil, fmt.Errorf("%w: the option letters do not normalize", ErrLetterShape)
	}
	for index := range probabilities {
		probabilities[index] /= total
	}
	return probabilities, nil
}

// argmax is the index of the largest value; the earlier index wins a tie.
func argmax(values []float64) int {
	best := 0
	for index, value := range values {
		if value > values[best] {
			best = index
		}
	}
	return best
}

// The generate request and answer as the server reads and writes them.
type generateRequest struct {
	Model       string          `json:"model"`
	Prompt      string          `json:"prompt"`
	Raw         bool            `json:"raw"`
	Stream      bool            `json:"stream"`
	Logprobs    bool            `json:"logprobs"`
	TopLogprobs int             `json:"top_logprobs"`
	KeepAlive   string          `json:"keep_alive,omitempty"`
	Options     generateOptions `json:"options"`
}

type generateOptions struct {
	NumPredict int `json:"num_predict"`
}

type generateResponse struct {
	Logprobs        []generateLogprob `json:"logprobs"`
	PromptEvalCount int64             `json:"prompt_eval_count"`
	EvalCount       int64             `json:"eval_count"`
}

type generateLogprob struct {
	TopLogprobs []tokenLogprob `json:"top_logprobs"`
}

type tokenLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}
