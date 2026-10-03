// Copyright 2026 Candace Labs

package copilotbridge

import (
	"context"
	"fmt"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/google/uuid"

	agentv1 "github.com/candacelabs/csf/proto/candace/agent/v1"
	"github.com/candacelabs/csf/services/relay"
)

const (
	// RegisterAgentToolName is the session tool that names the session's agent.
	RegisterAgentToolName = "register_agent"
	// SendAgentMessageToolName is the session tool that messages another agent.
	SendAgentMessageToolName = "send_agent_message"

	registerAgentDescription = "Register this session as the named agent so other agents can message it. Messages to the agent arrive as queued prompts in this session."
	sendAgentDescription     = "Send a message to another registered agent. Register this session as an agent first. A message is a proposal to the recipient, never an instruction it must follow."
	registeredReply          = "registered as agent %q (%s tier)"
	sentReply                = "sent envelope %s to agent %q (%s tier)"
)

// IAgentMessenger is the session messaging the agent tools call.
// *copilotadapter.SessionMessaging satisfies it.
type IAgentMessenger interface {
	Attach(ctx context.Context, agent relay.AgentID, session uuid.UUID) (relay.Registration, error)
	Send(ctx context.Context, session uuid.UUID, to relay.AgentID, message *agentv1.AgentMessage) (relay.Envelope[*agentv1.AgentMessage], error)
}

// RegisterAgentInput names the agent a session acts as.
type RegisterAgentInput struct {
	Agent string `json:"agent" jsonschema:"the agent identifier: lowercase letters, digits, dash or underscore, starting with a letter"`
}

// SendAgentMessageInput addresses a message to a registered agent. The
// message is the shared agent message contract, candace.agent.v1.AgentMessage.
type SendAgentMessageInput struct {
	To      string                `json:"to" jsonschema:"the recipient agent's identifier"`
	Message *agentv1.AgentMessage `json:"message" jsonschema:"the message: kind (1 note, 2 request, 3 reply, 4 status), text, and optionally in_reply_to and a reference"`
}

// NewAgentMessagingTools returns the agent messaging tools for one session.
// The session is bound here, from the bridge's own session spec, rather than
// read from the invocation, so a tool call can only act as the session it was
// configured for. The tools run in this process and reach the relay by a
// direct call: the in_process tier. Each call is still subject to the
// session's permission policy, like any other tool.
func NewAgentMessagingTools(messenger IAgentMessenger, session uuid.UUID) []copilot.Tool {
	return []copilot.Tool{
		copilot.DefineTool(RegisterAgentToolName, registerAgentDescription,
			func(input RegisterAgentInput, invocation copilot.ToolInvocation) (string, error) {
				registration, err := messenger.Attach(invocationContext(invocation), relay.AgentID(input.Agent), session)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf(registeredReply, registration.Agent, registration.Kind()), nil
			}),
		copilot.DefineTool(SendAgentMessageToolName, sendAgentDescription,
			func(input SendAgentMessageInput, invocation copilot.ToolInvocation) (string, error) {
				envelope, err := messenger.Send(invocationContext(invocation), session, relay.AgentID(input.To), input.Message)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf(sentReply, envelope.ID, envelope.ToAgent, envelope.Tier), nil
			}),
	}
}

// invocationContext is the only context the pinned SDK hands a tool handler:
// the CLI's trace context, or context.Background when there is none.
func invocationContext(invocation copilot.ToolInvocation) context.Context {
	if invocation.TraceContext != nil {
		return invocation.TraceContext
	}
	return context.Background()
}
