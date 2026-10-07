// Copyright 2026 Candace Labs

package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/io/net/model/claudecode"
)

func TestStubExecutorInject(t *testing.T) {
	stub := NewStubTurnExecutor()
	ctx := context.Background()

	turn := &claudecode.Turn{
		Messages: []json.RawMessage{
			json.RawMessage(`{"type":"user","message":{"role":"user","content":"test"}}`),
		},
	}

	proposal, err := stub.Propose(ctx, turn)
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}
	if proposal == nil {
		t.Error("Propose returned nil proposal")
	}
	if len(proposal.Actions) == 0 {
		t.Error("Propose returned no actions")
	}

	if err := stub.Inject(ctx, "injected message"); err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	stub.mu.Lock()
	if len(stub.injections) != 1 {
		t.Errorf("Inject: got %d injections, want 1", len(stub.injections))
	}
	if stub.injections[0] != "injected message" {
		t.Errorf("Inject: got %q, want 'injected message'", stub.injections[0])
	}
	stub.mu.Unlock()
}

func TestStubOpenExecutorInjectAndClose(t *testing.T) {
	stub := NewStubOpenTurnExecutor()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	turn := &claudecode.Turn{
		Messages: []json.RawMessage{
			json.RawMessage(`{"type":"user","message":{"role":"user","content":"test"}}`),
		},
	}

	proposal, err := stub.Propose(ctx, turn)
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}
	if proposal == nil {
		t.Error("Propose returned nil proposal")
	}

	if err := stub.Inject(ctx, "injected message 1"); err != nil {
		t.Fatalf("Inject 1 failed: %v", err)
	}

	if err := stub.Inject(ctx, "injected message 2"); err != nil {
		t.Fatalf("Inject 2 failed: %v", err)
	}

	if err := stub.Interrupt(ctx); err != nil {
		t.Fatalf("Interrupt failed: %v", err)
	}

	if err := stub.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	stub.mu.Lock()
	if stub.closed != true {
		t.Error("Close did not mark stub as closed")
	}
	if len(stub.injections) != 2 {
		t.Errorf("Injections: got %d, want 2", len(stub.injections))
	}
	stub.mu.Unlock()
}

func TestStubTurnExecutorMultipleProposals(t *testing.T) {
	stub := NewStubTurnExecutor()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		turn := &claudecode.Turn{
			Messages: []json.RawMessage{
				json.RawMessage(`{"type":"user","message":{"role":"user","content":"test"}}`),
			},
		}
		_, err := stub.Propose(ctx, turn)
		if err != nil {
			t.Fatalf("Propose %d failed: %v", i+1, err)
		}
	}

	stub.mu.Lock()
	if stub.proposals != 3 {
		t.Errorf("Proposals: got %d, want 3", stub.proposals)
	}
	stub.mu.Unlock()
}

func TestStubOpenTurnExecutorFactory(t *testing.T) {
	factory := StubOpenTurnExecutorFactory()
	ctx := context.Background()

	executor, err := factory(ctx, TurnExecutorSpec{
		Session:   uuid.New(),
		Directory: "/tmp",
	})
	if err != nil {
		t.Fatalf("Factory failed: %v", err)
	}

	openExec, ok := executor.(IOpenTurnExecutor)
	if !ok {
		t.Error("Factory did not return IOpenTurnExecutor")
	}

	if err := openExec.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}
