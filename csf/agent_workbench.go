package csf

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/copilot"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

const workbenchSessionURLPrefix = "/ui/#/sessions/"

// IAgentAssignmentBrain is the brain an agent assignment is handed to: given
// the prepared Workbench requests it proposes one agent turn. Production wires
// copilot.NewCopilotBrain over the adapter's generated client; specs wire
// stub.NewCannedBrain and need no model at all.
type IAgentAssignmentBrain = model.IBrain[*AgentWorkbenchRequests, copilot.Turn]

// SubmitAgentAssignment asks brain to take up a prepared plan and links the
// proposed turn to the plan in a receipt. endpoint is the Workbench URL the
// receipt's session link points at. A partial receipt is returned if the
// session exists but prompt acceptance is unconfirmed. Retry the original plan
// to recover its identities. A receipt records a proposal that was accepted;
// it does not prove the work completed.
func SubmitAgentAssignment(ctx context.Context, brain IAgentAssignmentBrain, endpoint string, plan *pb.AgentAssignmentPlan) (*pb.AgentAssignmentReceipt, error) {
	requests, err := NewAgentWorkbenchRequests(plan)
	if err != nil {
		return nil, err
	}
	if brain == nil {
		return nil, fmt.Errorf("agent assignment brain is required")
	}
	proposal, err := brain.Propose(ctx, requests)
	if unconfirmed, ok := errors.AsType[*copilot.UnconfirmedTurnError](err); ok {
		return agentAssignmentReceipt(endpoint, plan, unconfirmed.Turn), err
	}
	if err != nil {
		return nil, err
	}
	turn, err := proposal.Only()
	if err != nil {
		return nil, fmt.Errorf("agent assignment: %w", err)
	}
	if turn.TurnID == uuid.Nil {
		return agentAssignmentReceipt(endpoint, plan, turn), fmt.Errorf("agent assignment: brain %q proposed no accepted turn; retry the original plan", proposal.Provider)
	}
	return agentAssignmentReceipt(endpoint, plan, turn), nil
}

func agentAssignmentReceipt(endpoint string, plan *pb.AgentAssignmentPlan, turn copilot.Turn) *pb.AgentAssignmentReceipt {
	receipt := &pb.AgentAssignmentReceipt{
		Plan: proto.CloneOf(plan), SessionId: turn.SessionID.String(),
		WorktreeId: turn.WorktreeID.String(),
		SessionUrl: strings.TrimRight(endpoint, "/") + workbenchSessionURLPrefix + url.PathEscape(turn.SessionID.String()),
	}
	if turn.TurnID != uuid.Nil {
		receipt.TurnId = turn.TurnID.String()
	}
	return receipt
}
