// Copyright 2026 Candace Labs

package session

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
)

// StubTurnExecutor is a test double that records Propose and Inject calls.
type StubTurnExecutor struct {
	mu         sync.Mutex
	proposals  int
	injections []string
	lastErr    error
}

var _ ITurnExecutor = (*StubTurnExecutor)(nil)

// Propose records the call and returns a stub proposal.
func (stub *StubTurnExecutor) Propose(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.proposals++
	if stub.lastErr != nil {
		return nil, stub.lastErr
	}
	return &model.Proposal[claudecode.Event]{
		Provider: "stub",
		Actions: []claudecode.Event{
			{
				Type: "result",
				Raw:  json.RawMessage(`{"type":"result","result":{}}`),
			},
		},
	}, nil
}

// Inject records the message.
func (stub *StubTurnExecutor) Inject(ctx context.Context, message string) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.injections = append(stub.injections, message)
	return nil
}

// StubOpenTurnExecutor is a test double for open sessions.
type StubOpenTurnExecutor struct {
	*StubTurnExecutor
	closed bool
}

var _ IOpenTurnExecutor = (*StubOpenTurnExecutor)(nil)

// Interrupt is a no-op for the stub.
func (stub *StubOpenTurnExecutor) Interrupt(ctx context.Context) error {
	return nil
}

// Close marks the stub as closed.
func (stub *StubOpenTurnExecutor) Close(ctx context.Context) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.closed = true
	return nil
}

// NewStubTurnExecutor creates a test double executor.
func NewStubTurnExecutor() *StubTurnExecutor {
	return &StubTurnExecutor{}
}

// NewStubOpenTurnExecutor creates a test double open executor.
func NewStubOpenTurnExecutor() *StubOpenTurnExecutor {
	return &StubOpenTurnExecutor{StubTurnExecutor: NewStubTurnExecutor()}
}

// StubTurnExecutorFactory returns a factory that creates stub executors.
func StubTurnExecutorFactory() TurnExecutorFactory {
	return func(spec TurnExecutorSpec) (ITurnExecutor, error) {
		return NewStubTurnExecutor(), nil
	}
}

// StubOpenTurnExecutorFactory returns a factory that creates stub open executors.
func StubOpenTurnExecutorFactory() OpenTurnExecutorFactory {
	return func(ctx context.Context, spec TurnExecutorSpec) (IOpenTurnExecutor, error) {
		return NewStubOpenTurnExecutor(), nil
	}
}
