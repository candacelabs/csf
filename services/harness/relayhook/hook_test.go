// Copyright 2026 Candace Labs

package relayhook

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type mockSender struct {
	calls       int
	lastMsg     string
	answer      string
	err         error
	getCalls    int
	cancelCalls int
	canceledID  string
	cancelErr   error
	getAnswer   string
}

func (m *mockSender) Send(orch string, msg string) (string, error) {
	m.calls++
	m.lastMsg = msg
	return m.answer, m.err
}

func (m *mockSender) Get(orch string) (string, error) {
	m.getCalls++
	if m.getAnswer != "" {
		return m.getAnswer, nil
	}
	return m.answer, nil
}

func (m *mockSender) Cancel(id string) error {
	m.cancelCalls++
	m.canceledID = id
	return m.cancelErr
}

func TestHookForwardsPrompt(t *testing.T) {
	sender := &mockSender{answer: "answer from orchestrator"}
	h, err := New(
		WithOrchestrator("orch-123"),
		WithSender(sender),
	)
	if err != nil {
		t.Fatalf("new hook: %v", err)
	}

	input := HookInput{
		SessionID:     "session-456",
		HookEventName: "UserPromptSubmit",
		UserMessage:   "hello orchestrator",
	}
	inputBytes, _ := json.Marshal(input)

	output, err := h.Handle(inputBytes)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if output == nil || output.HookSpecificOutput == nil {
		t.Fatal("nil output")
	}

	if output.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("want deny, got %q", output.HookSpecificOutput.PermissionDecision)
	}

	if sender.calls != 1 {
		t.Errorf("want 1 send call, got %d", sender.calls)
	}

	if sender.lastMsg != "hello orchestrator" {
		t.Errorf("want 'hello orchestrator', got %q", sender.lastMsg)
	}
}

func TestHookAttachesLastAnswer(t *testing.T) {
	sender := &mockSender{answer: "answer 1"}
	h, err := New(
		WithOrchestrator("orch-123"),
		WithSender(sender),
	)
	if err != nil {
		t.Fatalf("new hook: %v", err)
	}

	// First call.
	input1 := HookInput{
		SessionID:     "session-456",
		HookEventName: "UserPromptSubmit",
		UserMessage:   "first prompt",
	}
	input1Bytes, _ := json.Marshal(input1)
	output1, err := h.Handle(input1Bytes)
	if err != nil {
		t.Fatalf("first handle: %v", err)
	}

	// First call should have empty last answer.
	if output1.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("first call: want empty context, got %q", output1.HookSpecificOutput.AdditionalContext)
	}

	// Second call with different answer.
	sender.answer = "answer 2"
	input2 := HookInput{
		SessionID:     "session-456",
		HookEventName: "UserPromptSubmit",
		UserMessage:   "second prompt",
	}
	input2Bytes, _ := json.Marshal(input2)
	output2, err := h.Handle(input2Bytes)
	if err != nil {
		t.Fatalf("second handle: %v", err)
	}

	// Second call should attach the first answer as context.
	if output2.HookSpecificOutput.AdditionalContext != "answer 1" {
		t.Errorf("second call: want 'answer 1', got %q", output2.HookSpecificOutput.AdditionalContext)
	}
}

func TestHookBlocksPrompt(t *testing.T) {
	sender := &mockSender{answer: "response"}
	h, err := New(
		WithOrchestrator("orch-123"),
		WithSender(sender),
	)
	if err != nil {
		t.Fatalf("new hook: %v", err)
	}

	input := HookInput{
		SessionID:     "session-456",
		HookEventName: "UserPromptSubmit",
		UserMessage:   "test",
	}
	inputBytes, _ := json.Marshal(input)

	output, err := h.Handle(inputBytes)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if output.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("want deny, got %q", output.HookSpecificOutput.PermissionDecision)
	}
}

func TestNewHookValidatesOptions(t *testing.T) {
	tests := map[string]struct {
		opts []Option
		want error
	}{
		"no orchestrator": {
			opts: []Option{WithSender(&mockSender{})},
			want: ErrNoOrchestrator,
		},
		"no sender": {
			opts: []Option{WithOrchestrator("orch-123")},
			want: errors.New("sender is required"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(tt.opts...)
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestHookStatusCallsGet(t *testing.T) {
	sender := &mockSender{answer: "session running", getAnswer: "session status: ready"}
	h, err := New(
		WithOrchestrator("orch-123"),
		WithSender(sender),
	)
	if err != nil {
		t.Fatalf("new hook: %v", err)
	}

	input := HookInput{
		SessionID:     "session-456",
		HookEventName: "UserPromptSubmit",
		UserMessage:   "status",
	}
	inputBytes, _ := json.Marshal(input)

	output, err := h.Handle(inputBytes)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if sender.getCalls != 1 {
		t.Errorf("want 1 Get call, got %d", sender.getCalls)
	}

	if !strings.Contains(output.HookSpecificOutput.AdditionalContext, "session status: ready") {
		t.Errorf("want status in context, got %q", output.HookSpecificOutput.AdditionalContext)
	}
}

func TestHookKillItCancelsOrchestrator(t *testing.T) {
	sender := &mockSender{}
	h, err := New(
		WithOrchestrator("orch-123"),
		WithSender(sender),
	)
	if err != nil {
		t.Fatalf("new hook: %v", err)
	}

	input := HookInput{
		SessionID:     "session-456",
		HookEventName: "UserPromptSubmit",
		UserMessage:   "kill it",
	}
	inputBytes, _ := json.Marshal(input)

	output, err := h.Handle(inputBytes)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if sender.cancelCalls != 1 {
		t.Errorf("want 1 Cancel call, got %d", sender.cancelCalls)
	}

	if sender.canceledID != "orch-123" {
		t.Errorf("want cancel orch-123, got %q", sender.canceledID)
	}

	if output.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("want deny, got %q", output.HookSpecificOutput.PermissionDecision)
	}
}

func TestHookKillIDCancelsTarget(t *testing.T) {
	sender := &mockSender{}
	h, err := New(
		WithOrchestrator("orch-123"),
		WithSender(sender),
	)
	if err != nil {
		t.Fatalf("new hook: %v", err)
	}

	input := HookInput{
		SessionID:     "session-456",
		HookEventName: "UserPromptSubmit",
		UserMessage:   "kill target-456",
	}
	inputBytes, _ := json.Marshal(input)

	output, err := h.Handle(inputBytes)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	if sender.cancelCalls != 1 {
		t.Errorf("want 1 Cancel call, got %d", sender.cancelCalls)
	}

	if sender.canceledID != "target-456" {
		t.Errorf("want cancel target-456, got %q", sender.canceledID)
	}

	if output.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("want deny, got %q", output.HookSpecificOutput.PermissionDecision)
	}
}
