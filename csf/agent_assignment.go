package csf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const (
	agentSessionKeyDomain  = "csf.agent.session.v1"
	agentPromptKeyDomain   = "csf.agent.prompt.v1"
	agentTicketHTTPScheme  = "http"
	agentTicketHTTPSScheme = "https"
)

// PrepareAgentAssignment prepares data only. The caller decides when to submit
// it; this capability allocates no session, worker, process or persistent state.
func (service *Service) PrepareAgentAssignment(ctx context.Context, request *pb.PrepareAgentAssignmentRequest) (*pb.PrepareAgentAssignmentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := PrepareAgentAssignment(request.GetRecipe())
	if err != nil {
		return nil, err
	}
	return &pb.PrepareAgentAssignmentResponse{Plan: plan}, nil
}

// PrepareAgentAssignment freezes a recipe into an independently owned plan.
// Retry keys depend on assignment identity, not content: editing an already
// submitted recipe must conflict with Workbench's existing idempotency receipt.
func PrepareAgentAssignment(recipe *pb.AgentAssignmentRecipe) (*pb.AgentAssignmentPlan, error) {
	if err := validateAgentRecipe(recipe); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	assignmentID := uuid.MustParse(recipe.AssignmentId)
	definitionHash, err := agentFingerprint(recipe.Agent)
	if err != nil {
		return nil, err
	}
	recipeHash, err := agentFingerprint(recipe)
	if err != nil {
		return nil, err
	}
	return &pb.AgentAssignmentPlan{
		Recipe: proto.CloneOf(recipe), DefinitionSha256: definitionHash, RecipeSha256: recipeHash,
		SessionKey: uuid.NewSHA1(assignmentID, []byte(agentSessionKeyDomain)).String(),
		PromptKey:  uuid.NewSHA1(assignmentID, []byte(agentPromptKeyDomain)).String(),
	}, nil
}

func validateAgentRecipe(recipe *pb.AgentAssignmentRecipe) error {
	if err := pb.ValidateAgentAssignmentRecipe(recipe); err != nil {
		return err
	}
	if err := pb.ValidateAgentDefinition(recipe.Agent); err != nil {
		return err
	}
	if identifier, err := uuid.Parse(recipe.AssignmentId); err != nil || identifier == uuid.Nil {
		return fmt.Errorf("assignment_id must be a nonzero UUID")
	}
	ticket, err := url.Parse(recipe.TicketUrl)
	if err != nil || ticket.Hostname() == "" || ticket.User != nil || (ticket.Scheme != agentTicketHTTPScheme && ticket.Scheme != agentTicketHTTPSScheme) {
		return fmt.Errorf("ticket_url must be an absolute HTTP(S) URL without credentials")
	}
	return nil
}

func agentFingerprint(message proto.Message) (string, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return "", fmt.Errorf("fingerprint agent recipe: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// AgentWorkbenchRequests uses the Workbench's generated request types. This
// translation is the sole boundary between CSF recipes and its current backend.
type AgentWorkbenchRequests struct {
	Session api.CreateSessionJSONRequestBody
	Prompt  api.SubmitPromptJSONRequestBody
}

// NewAgentWorkbenchRequests verifies a prepared plan before translating it.
// The same keys safely replay session creation and prompt acceptance; retries
// never claim that a backend completed work whose result was not observed.
func NewAgentWorkbenchRequests(plan *pb.AgentAssignmentPlan) (*AgentWorkbenchRequests, error) {
	expected, err := PrepareAgentAssignment(plan.GetRecipe())
	if err != nil {
		return nil, err
	}
	if !proto.Equal(expected, plan) {
		return nil, fmt.Errorf("%w: agent plan does not match its recipe", ErrInvalidRequest)
	}
	recipe := expected.Recipe
	requests := &AgentWorkbenchRequests{}
	err = requests.Session.FromNewWorktreeSessionRequest(api.NewWorktreeSessionRequest{
		AgentId: &recipe.Agent.Id, IdempotencyKey: uuid.MustParse(plan.SessionKey), Model: recipe.Model,
		RepositoryId: recipe.RepositoryId, DisplayName: &recipe.Agent.DisplayName,
		SystemInstructions: &recipe.Agent.Instructions,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare Workbench session: %w", err)
	}
	requests.Prompt = api.SubmitPromptJSONRequestBody{
		IdempotencyKey: uuid.MustParse(plan.PromptKey), Mode: api.Queue,
		Author: &recipe.Agent.DisplayName,
		Text: fmt.Sprintf("Agent: %s (revision %d)\nAssignment: %s\nRecipe SHA-256: %s\nTicket: %s\n\n%s",
			recipe.Agent.Id, recipe.Agent.Revision, recipe.AssignmentId, plan.RecipeSha256, recipe.TicketUrl, recipe.Task),
	}
	return requests, nil
}
