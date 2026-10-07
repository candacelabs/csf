// Copyright 2026 Candace Labs

// Package ollama is the local model provider behind CSF's brain contract: an
// Ollama server, on this host or a neighbour, reached over its HTTP API
// through the http capability the constructor receives. It is the provider
// that costs nothing per token, so it is the one the ouroboros labeler
// (services/ouroboros/labeler) asks to label candidate spans.
//
// A decision is one [Prompt]: a system message, a user message and, for a
// typed answer, the JSON schema the server constrains the model to. The
// proposal is one [Answer]: the message and the server's own measurements of
// the request (load, prompt evaluation, generation), which a caller derives
// its batch sizes and keep-alives from. The keep-alive travels with every
// request, because the server's default on this fleet is to keep a model
// loaded forever: whoever shares the GPU says how long it may stay.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/net/model"
	iohttp "github.com/candacelabs/csf/io/net/http"
)

// ProviderName is the Provider every Ollama proposal carries.
const ProviderName = "ollama"

// DefaultEndpoint is Ollama's own default address.
const DefaultEndpoint = "http://127.0.0.1:11434"

// The server's API paths and the request vocabulary.
const (
	chatPath   = "/api/chat"
	embedPath  = "/api/embed"
	loadedPath = "/api/ps"

	roleSystem = "system"
	roleUser   = "user"

	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"

	// maxAnswerBytes bounds one response body read.
	maxAnswerBytes = 8 << 20
)

var (
	// ErrNoClient reports a brain constructed without the http capability.
	ErrNoClient = errors.New("ollama brain: an HTTP client capability is required")
	// ErrNoModel reports a brain constructed without a model name.
	ErrNoModel = errors.New("ollama brain: a model name is required")
	// ErrInvalidOption reports a nil option or a value the brain cannot use.
	ErrInvalidOption = errors.New("ollama brain: invalid option")
	// ErrNoPrompt reports a nil prompt or one with no user message.
	ErrNoPrompt = errors.New("ollama brain: a prompt with a user message is required")
	// ErrIncompleteAnswer reports a server that answered before the model was
	// done, which a non-streaming request never should.
	ErrIncompleteAnswer = errors.New("ollama brain: the server answered before the model was done")
	// ErrEmbeddingCount reports a server that returned a different number of
	// vectors than it was given inputs.
	ErrEmbeddingCount = errors.New("ollama brain: the server returned a vector count unlike its inputs")
)

// Prompt is the context the Ollama brain decides on: one chat turn.
type Prompt struct {
	// System is the system message; empty sends none.
	System string
	// User is the user message. Required.
	User string
	// Schema is a JSON schema the server constrains the answer to, so Content
	// is valid JSON of that shape. Nil asks for free text.
	Schema json.RawMessage
	// KeepAlive is how long the server keeps the model loaded after this
	// answer. Zero leaves the server's own default in force.
	KeepAlive time.Duration
}

// Answer is the action the Ollama brain proposes: the model's message and
// the server's measurements of producing it.
type Answer struct {
	// Content is the assistant message: JSON text when the prompt carried a
	// schema, prose otherwise.
	Content string
	// Thinking is the model's reasoning when the brain was built with
	// [WithThinking]; empty otherwise.
	Thinking string
	Usage    Usage
}

// Usage is what the server measured for one answer.
type Usage struct {
	// Load is the time spent loading the model; zero when it was loaded.
	Load time.Duration
	// PromptTokens and PromptEval are the prompt's size and evaluation time.
	PromptTokens int
	PromptEval   time.Duration
	// Tokens and Eval are the answer's size and generation time.
	Tokens int
	Eval   time.Duration
	// Total is the whole request as the server saw it.
	Total time.Duration
}

// LoadedModel is one model the server holds in memory.
type LoadedModel struct {
	Name string
	// VRAMBytes is how much of the GPU the model occupies.
	VRAMBytes int64
	// ExpiresAt is when the server unloads it if nothing asks for it first.
	ExpiresAt time.Time
}

// StatusError reports a server that refused the request. Message is the
// server's own error text.
type StatusError struct {
	Status  int
	Message string
}

func (failure *StatusError) Error() string {
	return fmt.Sprintf("ollama brain: HTTP %d: %s", failure.Status, failure.Message)
}

// OllamaBrain proposes answers from one model on one Ollama server. It holds
// no state between decisions, so one value is safe to share between
// goroutines; the server serializes requests itself.
type OllamaBrain struct {
	client        iohttp.IHTTPClient
	endpoint      string
	model         string
	think         bool
	contextWindow int
	temperature   float64
}

var _ model.IBrain[*Prompt, Answer] = (*OllamaBrain)(nil)

// OllamaBrainOption configures an [OllamaBrain].
type OllamaBrainOption func(brain *OllamaBrain) error

// WithEndpoint reaches the server at endpoint, a URL with scheme and host,
// instead of [DefaultEndpoint].
func WithEndpoint(endpoint string) OllamaBrainOption {
	return func(brain *OllamaBrain) error {
		trimmed := strings.TrimRight(endpoint, "/")
		if trimmed == "" {
			return fmt.Errorf("%w: empty endpoint", ErrInvalidOption)
		}
		brain.endpoint = trimmed
		return nil
	}
}

// WithThinking lets the model reason before it answers, and returns the
// reasoning in [Answer.Thinking]. Off by default: a labeling answer is a
// schema-constrained JSON object, and the reasoning costs generation time.
func WithThinking() OllamaBrainOption {
	return func(brain *OllamaBrain) error {
		brain.think = true
		return nil
	}
}

// WithContextWindow sets the model's context window in tokens; the server's
// default applies otherwise.
func WithContextWindow(tokens int) OllamaBrainOption {
	return func(brain *OllamaBrain) error {
		if tokens <= 0 {
			return fmt.Errorf("%w: context window must be positive, got %d", ErrInvalidOption, tokens)
		}
		brain.contextWindow = tokens
		return nil
	}
}

// WithTemperature sets the sampling temperature; zero, the default, makes
// an answer a deterministic function of its prompt.
func WithTemperature(temperature float64) OllamaBrainOption {
	return func(brain *OllamaBrain) error {
		if temperature < 0 {
			return fmt.Errorf("%w: temperature must not be negative, got %g", ErrInvalidOption, temperature)
		}
		brain.temperature = temperature
		return nil
	}
}

// NewOllamaBrain builds the brain for modelName on the server client
// reaches. The client is the http capability, dialed by the binary that owns
// the process; the caller owns its lifetime.
func NewOllamaBrain(client iohttp.IHTTPClient, modelName string, options ...OllamaBrainOption) (*OllamaBrain, error) {
	if client == nil {
		return nil, ErrNoClient
	}
	if modelName == "" {
		return nil, ErrNoModel
	}
	brain := &OllamaBrain{client: client, endpoint: DefaultEndpoint, model: modelName}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(brain); err != nil {
			return nil, err
		}
	}
	return brain, nil
}

// Model is the model name every proposal is made with.
func (brain *OllamaBrain) Model() string { return brain.model }

// Propose sends one chat request and returns the model's answer as the
// single proposed action.
func (brain *OllamaBrain) Propose(ctx context.Context, prompt *Prompt) (*model.Proposal[Answer], error) {
	if prompt == nil || prompt.User == "" {
		return nil, ErrNoPrompt
	}
	body, err := json.Marshal(brain.chatRequest(prompt))
	if err != nil {
		return nil, fmt.Errorf("ollama brain: encode chat request: %w", err)
	}
	var answer chatResponse
	if err := brain.call(ctx, stdhttp.MethodPost, chatPath, body, &answer); err != nil {
		return nil, err
	}
	if !answer.Done {
		return nil, ErrIncompleteAnswer
	}
	return &model.Proposal[Answer]{Provider: ProviderName, Actions: []Answer{answer.action()}}, nil
}

// Embed returns one vector per input, in input order, from the brain's model,
// which must be an embedding model. Inputs longer than the model's context are
// truncated by the server. stay is as for [Prompt.KeepAlive].
func (brain *OllamaBrain) Embed(ctx context.Context, inputs []string, stay time.Duration) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{Model: brain.model, Input: inputs, Truncate: true, KeepAlive: keepAlive(stay)})
	if err != nil {
		return nil, fmt.Errorf("ollama brain: encode embed request: %w", err)
	}
	var answer embedResponse
	if err := brain.call(ctx, stdhttp.MethodPost, embedPath, body, &answer); err != nil {
		return nil, err
	}
	if len(answer.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("%w: %d inputs, %d vectors", ErrEmbeddingCount, len(inputs), len(answer.Embeddings))
	}
	return answer.Embeddings, nil
}

// Loaded lists the models the server holds in memory, with the GPU memory
// each occupies and when each expires.
func (brain *OllamaBrain) Loaded(ctx context.Context) ([]LoadedModel, error) {
	var listed loadedResponse
	if err := brain.call(ctx, stdhttp.MethodGet, loadedPath, nil, &listed); err != nil {
		return nil, err
	}
	models := make([]LoadedModel, 0, len(listed.Models))
	for _, entry := range listed.Models {
		models = append(models, LoadedModel{Name: entry.Name, VRAMBytes: entry.SizeVRAM, ExpiresAt: entry.ExpiresAt})
	}
	return models, nil
}

// call sends one request and decodes its JSON answer into target.
func (brain *OllamaBrain) call(ctx context.Context, method string, path string, body []byte, target any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := stdhttp.NewRequestWithContext(ctx, method, brain.endpoint+path, reader)
	if err != nil {
		return fmt.Errorf("ollama brain: build %s %s: %w", method, path, err)
	}
	if body != nil {
		request.Header.Set(headerContentType, contentTypeJSON)
	}
	response, err := brain.client.Do(request)
	if err != nil {
		return fmt.Errorf("ollama brain: %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes))
	if err != nil {
		return fmt.Errorf("ollama brain: read %s %s: %w", method, path, err)
	}
	if response.StatusCode < stdhttp.StatusOK || response.StatusCode >= stdhttp.StatusMultipleChoices {
		return &StatusError{Status: response.StatusCode, Message: serverMessage(content)}
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("ollama brain: decode %s %s: %w", method, path, err)
	}
	return nil
}

// serverMessage is the error text of a refused request: the server's own
// {"error": ...} when it sent one, the raw body otherwise.
func serverMessage(content []byte) string {
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(content, &failure) == nil && failure.Error != "" {
		return failure.Error
	}
	return strings.TrimSpace(string(content))
}

// The chat request as the server reads it.
type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []chatMessage   `json:"messages"`
	Stream    bool            `json:"stream"`
	Think     bool            `json:"think"`
	Format    json.RawMessage `json:"format,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Options   chatOptions     `json:"options"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	Temperature   float64 `json:"temperature"`
	ContextWindow int     `json:"num_ctx,omitempty"`
}

func (brain *OllamaBrain) chatRequest(prompt *Prompt) chatRequest {
	var messages []chatMessage
	if prompt.System != "" {
		messages = append(messages, chatMessage{Role: roleSystem, Content: prompt.System})
	}
	messages = append(messages, chatMessage{Role: roleUser, Content: prompt.User})
	return chatRequest{
		Model:     brain.model,
		Messages:  messages,
		Think:     brain.think,
		Format:    prompt.Schema,
		KeepAlive: keepAlive(prompt.KeepAlive),
		Options:   chatOptions{Temperature: brain.temperature, ContextWindow: brain.contextWindow},
	}
}

// keepAlive renders the duration the way the server parses it; zero leaves
// the field out so the server's default applies.
func keepAlive(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}

// The chat answer as the server writes it; durations are nanoseconds.
type chatResponse struct {
	Message struct {
		Content  string `json:"content"`
		Thinking string `json:"thinking"`
	} `json:"message"`
	Done               bool  `json:"done"`
	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

func (answer chatResponse) action() Answer {
	return Answer{
		Content:  answer.Message.Content,
		Thinking: answer.Message.Thinking,
		Usage: Usage{
			Load:         time.Duration(answer.LoadDuration),
			PromptTokens: answer.PromptEvalCount,
			PromptEval:   time.Duration(answer.PromptEvalDuration),
			Tokens:       answer.EvalCount,
			Eval:         time.Duration(answer.EvalDuration),
			Total:        time.Duration(answer.TotalDuration),
		},
	}
}

// The embed request and answer as the server reads and writes them.
type embedRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	Truncate  bool     `json:"truncate"`
	KeepAlive string   `json:"keep_alive,omitempty"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type loadedResponse struct {
	Models []struct {
		Name      string    `json:"name"`
		SizeVRAM  int64     `json:"size_vram"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"models"`
}
