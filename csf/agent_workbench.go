package csf

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const workbenchSessionURLPrefix = "/ui/#/sessions/"

// SubmitAgentAssignmentHTTP is a consumer-side example of executing a plan
// through the existing generated Workbench client. Hosts composing in process
// can pass NewAgentWorkbenchRequests directly to their adapter instead.
// A partial receipt is returned if session creation succeeds but prompt
// acceptance is unconfirmed. Retry the original plan to recover its identities.
func SubmitAgentAssignmentHTTP(ctx context.Context, client *api.ClientWithResponses, endpoint string, plan *pb.AgentAssignmentPlan) (*pb.AgentAssignmentReceipt, error) {
	requests, err := NewAgentWorkbenchRequests(plan)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("Workbench client is required")
	}
	created, err := client.CreateSessionWithResponse(ctx, requests.Session)
	if err != nil {
		return nil, fmt.Errorf("create agent session; retry the original plan: %w", err)
	}
	if created.JSON201 == nil || created.JSON201.Id == nil || created.JSON201.WorktreeId == nil {
		return nil, fmt.Errorf("create agent session: HTTP %d", created.StatusCode())
	}
	session := created.JSON201
	receipt := &pb.AgentAssignmentReceipt{
		Plan: proto.CloneOf(plan), SessionId: session.Id.String(),
		WorktreeId: session.WorktreeId.String(),
		SessionUrl: strings.TrimRight(endpoint, "/") + workbenchSessionURLPrefix + url.PathEscape(session.Id.String()),
	}
	prompted, err := client.SubmitPromptWithResponse(ctx, *session.Id, requests.Prompt)
	if err != nil {
		return receipt, fmt.Errorf("submit agent prompt; retry the original plan: %w", err)
	}
	if prompted.JSON202 == nil || prompted.JSON202.Id == nil {
		return receipt, fmt.Errorf("submit agent prompt: HTTP %d", prompted.StatusCode())
	}
	receipt.TurnId = prompted.JSON202.Id.String()
	return receipt, nil
}
