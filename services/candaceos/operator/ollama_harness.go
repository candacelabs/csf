package operator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

const (
	ollamaStreamBytesPerContextToken = 256
	ollamaErrorResponseBytes         = 64 << 10
)

var (
	errOllamaHarnessClosed      = errors.New("Ollama harness is closed")
	errOllamaHarnessStarted     = errors.New("Ollama harness is already started")
	errOllamaSessionUnavailable = errors.New("Ollama session is unavailable")
)

type ollamaHarness struct {
	config     *candaceosv1.OllamaConfig
	controller *Controller
	client     *ollamaClient

	mu              sync.Mutex
	lifecycle       context.Context
	cancelLifecycle context.CancelFunc
	turnID          string
	cancelTurn      context.CancelFunc
	closed          bool
	wg              sync.WaitGroup
}

type ollamaClient struct {
	baseURL       string
	httpClient    *http.Client
	streamByteCap int64
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
}

type ollamaToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Index     *int            `json:"index,omitempty"`
}

type ollamaTool struct {
	Type     string               `json:"type"`
	Function ollamaToolDefinition `json:"function"`
}

type ollamaToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []ollamaTool    `json:"tools"`
	Stream   bool            `json:"stream"`
	Think    bool            `json:"think"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaOptions struct {
	ContextTokens int32 `json:"num_ctx"`
}

type ollamaChatChunk struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
	Error   string        `json:"error,omitempty"`
}

func newOllamaHarness(cfg *candaceosv1.OllamaConfig, controller *Controller) *ollamaHarness {
	return &ollamaHarness{
		config:     cfg,
		controller: controller,
		client: &ollamaClient{
			baseURL:       strings.TrimRight(cfg.GetUrl(), "/"),
			httpClient:    http.DefaultClient,
			streamByteCap: int64(cfg.GetContextTokens()) * ollamaStreamBytesPerContextToken,
		},
	}
}

func (h *ollamaHarness) Start(ctx context.Context) (harnessStart, error) {
	if err := ctx.Err(); err != nil {
		return harnessStart{}, err
	}
	verificationContext, cancelVerification := context.WithTimeout(ctx, time.Duration(h.config.GetTurnTimeout()))
	defer cancelVerification()
	if err := h.client.verify(verificationContext, h.config.GetModel(), h.config.GetModelDigest()); err != nil {
		return harnessStart{}, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return harnessStart{}, errOllamaHarnessClosed
	}
	if h.lifecycle != nil && h.lifecycle.Err() == nil {
		return harnessStart{}, errOllamaHarnessStarted
	}
	h.lifecycle, h.cancelLifecycle = context.WithCancel(ctx)
	h.turnID = ""
	h.cancelTurn = nil
	sessionID := "ollama-" + uuid.NewString()
	return harnessStart{
		SessionID: sessionID,
		Activate: func() error {
			h.emit("", eventRecord{
				ID: uuid.NewString(), Type: eventKindSessionStart, Timestamp: time.Now().UTC(),
				Data: map[string]any{"message": "Ollama model " + h.config.GetModel() + " is ready."},
			})
			return nil
		},
	}, nil
}

func (h *ollamaHarness) Send(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	h.mu.Lock()
	if h.closed || h.lifecycle == nil || h.lifecycle.Err() != nil {
		h.mu.Unlock()
		return errOllamaSessionUnavailable
	}
	if h.turnID != "" {
		h.mu.Unlock()
		return errors.New("Ollama is already processing a turn")
	}
	turnID := uuid.NewString()
	turnContext, cancelTurn := context.WithTimeout(h.lifecycle, time.Duration(h.config.GetTurnTimeout()))
	h.turnID = turnID
	h.cancelTurn = cancelTurn
	h.wg.Add(1)
	h.mu.Unlock()

	if !h.emit(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindUserMessage, Timestamp: time.Now().UTC(),
		Data: map[string]any{"content": prompt.GetContent()},
	}) {
		h.finishTurn(turnID)
		h.wg.Done()
		return errOllamaSessionUnavailable
	}
	go h.completeTurn(turnContext, turnID, prompt.GetContent())
	return nil
}

func (h *ollamaHarness) completeTurn(ctx context.Context, turnID string, prompt string) {
	defer h.wg.Done()

	err := h.runTurn(ctx, turnID, prompt)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			h.finishTurn(turnID)
			return
		}
		h.finishTurnWithEvent(turnID, eventRecord{
			ID: uuid.NewString(), Type: eventKindSessionError, Timestamp: time.Now().UTC(),
			Data: map[string]any{"message": err.Error()},
		})
		return
	}
	h.finishTurnWithEvent(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindSessionIdle, Timestamp: time.Now().UTC(), Data: map[string]any{},
	})
}

func (h *ollamaHarness) runTurn(ctx context.Context, turnID string, prompt string) error {
	messages := []ollamaMessage{
		{Role: "system", Content: ollamaSystemInstructions},
		{Role: "user", Content: prompt},
	}
	tools := ollamaTools(h.controller.reconciler != nil)
	toolCalls := int32(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		messageID := uuid.NewString()
		response, err := h.client.chat(ctx, ollamaChatRequest{
			Model: h.config.GetModel(), Messages: messages, Tools: tools,
			Stream: true, Think: false,
			Options: ollamaOptions{ContextTokens: h.config.GetContextTokens()},
		}, func(delta string) error {
			if h.emit(turnID, eventRecord{
				ID: uuid.NewString(), Type: eventKindAssistantDelta, Timestamp: time.Now().UTC(), Ephemeral: true,
				Data: map[string]any{"messageId": messageID, "deltaContent": delta},
			}) {
				return nil
			}
			return context.Canceled
		})
		if err != nil {
			return err
		}
		messages = append(messages, response)
		if response.Content != "" {
			if !h.emit(turnID, eventRecord{
				ID: uuid.NewString(), Type: eventKindAssistantMessage, Timestamp: time.Now().UTC(),
				Data: map[string]any{"messageId": messageID, "content": response.Content},
			}) {
				return context.Canceled
			}
		}
		if len(response.ToolCalls) == 0 {
			if response.Content == "" {
				return errors.New("Ollama returned an empty assistant response")
			}
			return nil
		}
		if toolCalls+int32(len(response.ToolCalls)) > h.config.GetMaxToolCalls() {
			return fmt.Errorf("Ollama exceeded the %d native-tool-call limit", h.config.GetMaxToolCalls())
		}
		for _, call := range response.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			toolMessage, err := h.executeTool(ctx, turnID, call)
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			messages = append(messages, toolMessage)
			toolCalls++
		}
	}
}

func (h *ollamaHarness) executeTool(ctx context.Context, turnID string, call ollamaToolCall) (ollamaMessage, error) {
	toolCallID := call.ID
	if toolCallID == "" {
		toolCallID = uuid.NewString()
	}
	arguments, argumentErr := ollamaArguments(call.Function.Arguments)
	h.emit(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindToolExecutionStart, Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"toolCallId": toolCallID,
			"toolName":   call.Function.Name,
			"arguments":  arguments,
		},
	})

	var result any
	err := argumentErr
	if err == nil {
		result, err = h.callTool(ctx, toolCallID, call.Function.Name, arguments)
	}
	completion := map[string]any{
		"toolCallId": toolCallID,
		"toolName":   call.Function.Name,
	}
	if err != nil {
		completion["error"] = err.Error()
	} else {
		completion["result"] = result
	}
	h.emit(turnID, eventRecord{
		ID: uuid.NewString(), Type: eventKindToolExecutionComplete, Timestamp: time.Now().UTC(), Data: completion,
	})

	encoded, encodeErr := json.Marshal(completion)
	if encodeErr != nil {
		return ollamaMessage{}, encodeErr
	}
	return ollamaMessage{Role: "tool", Content: string(encoded), ToolName: call.Function.Name}, err
}

func (h *ollamaHarness) callTool(ctx context.Context, toolCallID string, name string, arguments any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case "candace_fleet_status":
		return h.controller.configuredFleetStatus()
	case "candace_reconcile_app":
		if h.controller.reconciler == nil {
			return nil, errors.New("reconciler is unavailable")
		}
		decision, err := h.controller.handlePermissionContext(ctx, harnessPermission{
			kind: "custom_tool", toolCallID: toolCallID, title: "Use candace_reconcile_app", risk: "high",
			payload: arguments, requiresFleetQuorum: true, reconcileArgs: arguments,
		})
		if err != nil {
			return nil, err
		}
		switch decision {
		case harnessPermissionApprove:
			input, err := reconcileIntent(arguments)
			if err != nil {
				return nil, fmt.Errorf("decoding approved reconcile input: %w", err)
			}
			return h.controller.reconcileApproved(ctx, input, toolCallID)
		case harnessPermissionReject:
			return nil, errors.New("the operator rejected this reconcile action")
		default:
			return nil, errors.New("the operator is unavailable to approve this reconcile action")
		}
	default:
		return nil, fmt.Errorf("unknown Ollama tool %q", name)
	}
}

func (h *ollamaHarness) Abort(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	if h.cancelTurn != nil {
		h.cancelTurn()
	}
	h.turnID = ""
	h.cancelTurn = nil
	lifecycle := h.lifecycle
	live := !h.closed && lifecycle != nil && lifecycle.Err() == nil
	if live {
		h.wg.Add(1)
	}
	h.mu.Unlock()
	if live {
		go func() {
			defer h.wg.Done()
			timer := time.NewTimer(time.Millisecond)
			defer timer.Stop()
			select {
			case <-lifecycle.Done():
			case <-timer.C:
				h.emit("", eventRecord{
					ID: uuid.NewString(), Type: eventKindSessionIdle, Timestamp: time.Now().UTC(), Data: map[string]any{},
				})
			}
		}()
	}
	return nil
}

func (h *ollamaHarness) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		h.wg.Wait()
		return nil
	}
	h.closed = true
	if h.cancelTurn != nil {
		h.cancelTurn()
	}
	if h.cancelLifecycle != nil {
		h.cancelLifecycle()
	}
	h.turnID = ""
	h.cancelTurn = nil
	h.lifecycle = nil
	h.cancelLifecycle = nil
	h.mu.Unlock()
	h.wg.Wait()
	return nil
}

func (h *ollamaHarness) emit(turnID string, event eventRecord) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.lifecycle == nil || h.lifecycle.Err() != nil {
		return false
	}
	if turnID != "" && h.turnID != turnID {
		return false
	}
	h.controller.ingest(event)
	return true
}

func (h *ollamaHarness) finishTurn(turnID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.turnID != turnID {
		return
	}
	if h.cancelTurn != nil {
		h.cancelTurn()
	}
	h.turnID = ""
	h.cancelTurn = nil
}

func (h *ollamaHarness) finishTurnWithEvent(turnID string, event eventRecord) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.lifecycle == nil || h.lifecycle.Err() != nil || h.turnID != turnID {
		return false
	}
	if h.cancelTurn != nil {
		h.cancelTurn()
	}
	h.turnID = ""
	h.cancelTurn = nil
	h.controller.ingest(event)
	return true
}

func (client *ollamaClient) verify(ctx context.Context, model string, expectedDigest string) error {
	var version struct {
		Version string `json:"version"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/api/version", nil, &version); err != nil {
		return fmt.Errorf("probing Ollama API: %w", err)
	}
	if version.Version == "" {
		return errors.New("probing Ollama API: response omitted version")
	}
	var show struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/api/show", map[string]string{"model": model}, &show); err != nil {
		return fmt.Errorf("verifying Ollama model %q: %w", model, err)
	}
	if !slices.Contains(show.Capabilities, "tools") {
		return fmt.Errorf("Ollama model %q does not advertise native tool support", model)
	}
	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/api/tags", nil, &tags); err != nil {
		return fmt.Errorf("verifying Ollama model %q digest: %w", model, err)
	}
	matchingDigests := make([]string, 0, 1)
	for _, candidate := range tags.Models {
		if candidate.Name == model || candidate.Model == model {
			matchingDigests = append(matchingDigests, strings.TrimPrefix(candidate.Digest, "sha256:"))
		}
	}
	if len(matchingDigests) != 1 {
		return fmt.Errorf("Ollama exposes %d models named %q", len(matchingDigests), model)
	}
	decoded, err := hex.DecodeString(matchingDigests[0])
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("Ollama model %q returned a malformed digest", model)
	}
	if matchingDigests[0] != expectedDigest {
		return fmt.Errorf("Ollama model %q digest is %s, expected %s", model, matchingDigests[0], expectedDigest)
	}
	return nil
}

func (client *ollamaClient) chat(
	ctx context.Context,
	request ollamaChatRequest,
	onContent func(delta string) error,
) (ollamaMessage, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return ollamaMessage{}, fmt.Errorf("encoding Ollama chat request: %w", err)
	}
	if int64(len(encoded)) > client.streamByteCap {
		return ollamaMessage{}, fmt.Errorf("Ollama chat request exceeded %d bytes", client.streamByteCap)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/chat", bytes.NewReader(encoded))
	if err != nil {
		return ollamaMessage{}, fmt.Errorf("creating Ollama chat request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return ollamaMessage{}, fmt.Errorf("calling Ollama chat API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ollamaMessage{}, ollamaHTTPError(response)
	}

	limited := &io.LimitedReader{R: response.Body, N: client.streamByteCap + 1}
	decoder := json.NewDecoder(limited)
	message := ollamaMessage{Role: "assistant"}
	done := false
	for {
		var chunk ollamaChatChunk
		if err := decoder.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if limited.N == 0 {
				return ollamaMessage{}, fmt.Errorf("Ollama chat stream exceeded %d bytes", client.streamByteCap)
			}
			return ollamaMessage{}, fmt.Errorf("decoding Ollama chat stream: %w", err)
		}
		if chunk.Error != "" {
			return ollamaMessage{}, errors.New(chunk.Error)
		}
		if chunk.Message.Content != "" {
			if err := onContent(chunk.Message.Content); err != nil {
				return ollamaMessage{}, err
			}
			message.Content += chunk.Message.Content
		}
		message.Thinking += chunk.Message.Thinking
		message.ToolCalls = append(message.ToolCalls, chunk.Message.ToolCalls...)
		if chunk.Done {
			done = true
			break
		}
	}
	if limited.N == 0 {
		return ollamaMessage{}, fmt.Errorf("Ollama chat stream exceeded %d bytes", client.streamByteCap)
	}
	if !done {
		return ollamaMessage{}, errors.New("Ollama chat stream ended before its done marker")
	}
	return message, nil
}

func (client *ollamaClient) doJSON(ctx context.Context, method string, path string, input any, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ollamaHTTPError(response)
	}
	limited := &io.LimitedReader{R: response.Body, N: ollamaErrorResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(output); err != nil {
		if limited.N == 0 {
			return fmt.Errorf("Ollama API response exceeded %d bytes", ollamaErrorResponseBytes)
		}
		return err
	}
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	if limited.N == 0 {
		return fmt.Errorf("Ollama API response exceeded %d bytes", ollamaErrorResponseBytes)
	}
	if trailingErr == nil {
		return errors.New("Ollama API response contained trailing JSON")
	}
	if !errors.Is(trailingErr, io.EOF) {
		return trailingErr
	}
	return nil
}

func ollamaHTTPError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, ollamaErrorResponseBytes))
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	detail := strings.TrimSpace(payload.Error)
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	if detail == "" {
		return fmt.Errorf("Ollama API returned %s", response.Status)
	}
	return fmt.Errorf("Ollama API returned %s: %s", response.Status, detail)
}

func ollamaArguments(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var arguments any
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, fmt.Errorf("decoding Ollama tool arguments: %w", err)
	}
	return arguments, nil
}

func ollamaTools(reconcileAvailable bool) []ollamaTool {
	tools := []ollamaTool{{
		Type: "function",
		Function: ollamaToolDefinition{
			Name:        "candace_fleet_status",
			Description: "Read configured node roles and labels plus current Warden leader and quorum",
			Parameters: map[string]any{
				"type": "object", "properties": map[string]any{}, "additionalProperties": false,
			},
		},
	}}
	if !reconcileAvailable {
		return tools
	}
	tools = append(tools, ollamaTool{
		Type: "function",
		Function: ollamaToolDefinition{
			Name:        "candace_reconcile_app",
			Description: "Reconcile one immutable app revision through the fenced CandaceOS node agent; always requires operator approval",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"app":           map[string]any{"type": "string"},
					"project":       map[string]any{"type": "string"},
					"path":          map[string]any{"type": "string"},
					"desired_state": map[string]any{"type": "string", "enum": []string{"running", "stopped"}},
					"placement_mode": map[string]any{
						"type": "string", "enum": []string{"exact_node", "leader", "labels"},
					},
					"node_id":  map[string]any{"type": "string"},
					"labels":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
					"replicas": map[string]any{"type": "integer", "minimum": 1},
					"stateful": map[string]any{"type": "boolean"},
				},
				"required":             []string{"app", "project", "path", "desired_state", "placement_mode"},
				"additionalProperties": false,
			},
		},
	})
	return tools
}
