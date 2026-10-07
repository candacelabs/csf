// Copyright 2026 Candace Labs

// Package copilot is the first inference provider behind CSF's brain
// contract: the GitHub Copilot agent loop, reached through the existing
// Copilot adapter's generated Workbench client
// (services/copilot-adapter/gen/api). It wraps that client; it does not
// reimplement or move the adapter.
//
// Copilot is an agent loop, so what it proposes for an assignment is one
// queued agent turn in a Workbench session and worktree. The tool calls that
// turn makes are themselves proposals: the session's permission policy
// (ask, by default) holds each one as a pending session request until the
// caller resolves it. Accepting the turn grants no tool permission.
package copilot

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/io/net/model"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

// ProviderName is the Provider every Copilot proposal carries.
const ProviderName = "copilot"

// IWorkbenchClient is the slice of the adapter's generated client this
// provider uses. *api.ClientWithResponses satisfies it; specs substitute a
// gomock double at exactly this boundary.
type IWorkbenchClient interface {
	CreateSessionWithResponse(ctx context.Context, body api.CreateSessionJSONRequestBody, reqEditors ...api.RequestEditorFn) (*api.CreateSessionResponse, error)
	SubmitPromptWithResponse(ctx context.Context, sessionId api.SessionId, body api.SubmitPromptJSONRequestBody, reqEditors ...api.RequestEditorFn) (*api.SubmitPromptResponse, error)
}

// Assignment is the context the Copilot brain decides on: the generated
// Workbench session and prompt requests. Their idempotency keys make a
// repeated proposal replay the same session and turn.
type Assignment struct {
	Session api.CreateSessionJSONRequestBody
	Prompt  api.SubmitPromptJSONRequestBody
}

// Turn is the action the Copilot brain proposes for an assignment. A turn
// identifier acknowledges prompt acceptance; it does not prove completion.
type Turn struct {
	SessionID  uuid.UUID
	WorktreeID uuid.UUID
	TurnID     uuid.UUID
}

// UnconfirmedTurnError reports that the session exists but prompt acceptance
// was not confirmed. Turn carries the session and worktree with a zero TurnID;
// retrying the same assignment recovers its identities.
type UnconfirmedTurnError struct {
	Turn Turn
	Err  error
}

func (failure *UnconfirmedTurnError) Error() string {
	return failure.Err.Error()
}

func (failure *UnconfirmedTurnError) Unwrap() error {
	return failure.Err
}

// CopilotBrain proposes Copilot agent turns through a Workbench client.
type CopilotBrain struct {
	client IWorkbenchClient
}

var _ model.IBrain[*Assignment, Turn] = (*CopilotBrain)(nil)

// NewCopilotBrain wraps client, the capability that reaches the Copilot
// adapter. The caller owns the client's endpoint, transport and lifetime.
func NewCopilotBrain(client IWorkbenchClient) (*CopilotBrain, error) {
	if client == nil {
		return nil, errors.New("copilot brain: Workbench client is required")
	}
	return &CopilotBrain{client: client}, nil
}

// Propose creates (or replays) the assignment's session and queues its prompt,
// returning the accepted turn as the single proposed action.
func (brain *CopilotBrain) Propose(ctx context.Context, assignment *Assignment) (*model.Proposal[Turn], error) {
	if assignment == nil {
		return nil, errors.New("copilot brain: assignment is required")
	}
	created, err := brain.client.CreateSessionWithResponse(ctx, assignment.Session)
	if err != nil {
		return nil, fmt.Errorf("create agent session; retry the original plan: %w", err)
	}
	if created.JSON201 == nil || created.JSON201.Id == nil || created.JSON201.WorktreeId == nil {
		return nil, fmt.Errorf("create agent session: HTTP %d", created.StatusCode())
	}
	turn := Turn{SessionID: *created.JSON201.Id, WorktreeID: *created.JSON201.WorktreeId}
	prompted, err := brain.client.SubmitPromptWithResponse(ctx, turn.SessionID, assignment.Prompt)
	if err != nil {
		return nil, &UnconfirmedTurnError{Turn: turn, Err: fmt.Errorf("submit agent prompt; retry the original plan: %w", err)}
	}
	if prompted.JSON202 == nil || prompted.JSON202.Id == nil {
		return nil, &UnconfirmedTurnError{Turn: turn, Err: fmt.Errorf("submit agent prompt: HTTP %d", prompted.StatusCode())}
	}
	turn.TurnID = *prompted.JSON202.Id
	return &model.Proposal[Turn]{Provider: ProviderName, Actions: []Turn{turn}}, nil
}
