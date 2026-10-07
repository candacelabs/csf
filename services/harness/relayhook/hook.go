// Copyright 2026 Candace Labs

// Package relayhook provides the UserPromptSubmit hook that forwards prompts
// to an orchestrator session and blocks the proxy session from taking turns.
package relayhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	// ErrNoOrchestrator reports that the hook was built without an orchestrator ID.
	ErrNoOrchestrator = errors.New("relayhook: orchestrator assignment is required")
	// ErrMalformedInput reports hook input that cannot be decoded.
	ErrMalformedInput = errors.New("relayhook: malformed hook input")
)

// HookInput is the structure Claude Code sends to the UserPromptSubmit hook.
type HookInput struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	UserMessage   string `json:"user_message"`
}

// HookOutput is what the hook returns to Claude Code.
type HookOutput struct {
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput contains the hook's decision.
type HookSpecificOutput struct {
	HookEventName      string `json:"hookEventName"`
	PermissionDecision string `json:"permissionDecision,omitempty"`
	AdditionalContext  string `json:"additionalContext,omitempty"`
}

// Hook handles UserPromptSubmit hook calls.
type Hook struct {
	orchestrator string
	sender       ISender
	mu           sync.RWMutex
	lastAnswer   string
}

// Option configures a Hook.
type Option func(hook *Hook) error

// WithOrchestrator names the orchestrator session assignment.
func WithOrchestrator(id string) Option {
	return func(h *Hook) error {
		if id == "" {
			return ErrNoOrchestrator
		}
		h.orchestrator = id
		return nil
	}
}

// WithSender provides the interface to send messages to the orchestrator.
func WithSender(sender ISender) Option {
	return func(h *Hook) error {
		if sender == nil {
			return errors.New("relayhook: sender is required")
		}
		h.sender = sender
		return nil
	}
}

// New builds a Hook from options.
func New(options ...Option) (*Hook, error) {
	h := &Hook{}
	for _, opt := range options {
		if err := opt(h); err != nil {
			return nil, err
		}
	}
	if h.orchestrator == "" {
		return nil, ErrNoOrchestrator
	}
	if h.sender == nil {
		return nil, errors.New("relayhook: sender is required")
	}
	return h, nil
}

// Handle processes one UserPromptSubmit hook call.
func (h *Hook) Handle(input []byte) (*HookOutput, error) {
	var hookInput HookInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedInput, err)
	}

	// Attach the last answer from the orchestrator as additionalContext.
	h.mu.RLock()
	lastAnswer := h.lastAnswer
	h.mu.RUnlock()

	// Check for control verbs and map them to operations.
	msg := hookInput.UserMessage

	if msg == "status" {
		// Get the current status from the orchestrator.
		status, err := h.sender.Get(h.orchestrator)
		if err != nil {
			status = fmt.Sprintf("error getting status: %v", err)
		}

		// Update the last answer with the fresh status.
		h.mu.Lock()
		h.lastAnswer = status
		h.mu.Unlock()

		// Return the status as context; block the prompt.
		output := &HookOutput{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:      "UserPromptSubmit",
				PermissionDecision: "deny",
				AdditionalContext:  "orchestrator status: " + status,
			},
		}
		return output, nil
	}

	if msg == "redeploy" {
		// Allow "redeploy" to pass through to the proxy.
		output := &HookOutput{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName: "UserPromptSubmit",
			},
		}
		return output, nil
	}

	// Check for "kill" control verbs.
	if msg == "kill it" {
		// Cancel the orchestrator session.
		err := h.sender.Cancel(h.orchestrator)
		if err != nil {
			return nil, fmt.Errorf("cancel orchestrator: %w", err)
		}

		output := &HookOutput{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:      "UserPromptSubmit",
				PermissionDecision: "deny",
				AdditionalContext:  "orchestrator session canceled",
			},
		}
		return output, nil
	}

	if strings.HasPrefix(msg, "kill ") {
		// Parse "kill <id>" and cancel that session.
		parts := strings.Fields(msg)
		if len(parts) == 2 {
			targetID := parts[1]
			err := h.sender.Cancel(targetID)
			if err != nil {
				return nil, fmt.Errorf("cancel session %s: %w", targetID, err)
			}

			output := &HookOutput{
				HookSpecificOutput: &HookSpecificOutput{
					HookEventName:      "UserPromptSubmit",
					PermissionDecision: "deny",
					AdditionalContext:  fmt.Sprintf("session %s canceled", targetID),
				},
			}
			return output, nil
		}
		// If the format is wrong, fall through to regular prompt forwarding.
	}

	// For regular prompts, forward to orchestrator.
	answer, err := h.sender.Send(h.orchestrator, msg)
	if err != nil {
		return nil, fmt.Errorf("send to orchestrator: %w", err)
	}

	// Store the answer for the next prompt.
	h.mu.Lock()
	h.lastAnswer = answer
	h.mu.Unlock()

	// Return the decision: block this prompt and attach the last answer as context.
	output := &HookOutput{
		HookSpecificOutput: &HookSpecificOutput{
			HookEventName:      "UserPromptSubmit",
			PermissionDecision: "deny",
			AdditionalContext:  lastAnswer,
		},
	}

	return output, nil
}
