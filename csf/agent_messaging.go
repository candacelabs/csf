// Copyright 2026 Candace Labs

package csf

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/net/model/copilot"
	agentv1 "github.com/candacelabs/csf/proto/candace/agent/v1"
	"github.com/candacelabs/csf/services/relay"
)

// Agent messaging MCP tool names. A host or network agent calls them through
// AgentMCPHandler, so every call carries a verified agent identity.
const (
	RegisterAgentAddressTool  = "RegisterAgentAddress"
	SendAgentMessageTool      = "SendAgentMessage"
	FetchAgentInboxTool       = "FetchAgentInbox"
	AcknowledgeAgentInboxTool = "AcknowledgeAgentInbox"

	defaultInboxFetchLimit = 16
	maxInboxFetchLimit     = 256
)

// agentAddressBuilder turns a caller's declared endpoint into one provider's
// address type. The session is always the caller's verified MCP session, so a
// caller can register only its own session.
type agentAddressBuilder func(session uuid.UUID, endpoint string) (model.IAgentAddress, error)

// agentAddressBuilders are the providers whose agents can register over MCP.
// The provider's address type decides the tier: a Claude Code session names
// a local socket (host); a Copilot session reached over MCP is on another
// adapter (network). An in-process Copilot session registers in the runtime
// that mounts its adapter, never over MCP.
var agentAddressBuilders = map[string]agentAddressBuilder{
	claudecode.ProviderName: func(session uuid.UUID, endpoint string) (model.IAgentAddress, error) {
		return claudecode.NewHostAddress(session, endpoint)
	},
	copilot.ProviderName: func(session uuid.UUID, endpoint string) (model.IAgentAddress, error) {
		return copilot.NewNetworkAddress(session, endpoint)
	},
}

// RegisterAgentAddressInput declares where the calling agent's session is.
type RegisterAgentAddressInput struct {
	Provider string `json:"provider" jsonschema:"the session's provider: claudecode (a session on this machine) or copilot (a Workbench session on another adapter)"`
	Endpoint string `json:"endpoint" jsonschema:"claudecode: unix:/absolute/socket/path or a loopback host:port; copilot: the adapter's host:port"`
}

// AgentRegistrationView is a registration as an MCP caller sees it.
type AgentRegistrationView struct {
	Agent   string `json:"agent"`
	Tier    string `json:"tier"`
	Address string `json:"address"`
}

// SendAgentMessageInput addresses a message to a registered agent. The
// message is the shared contract candace.agent.v1.AgentMessage; the tool's
// input schema is derived from its generated type, and the message must pass
// its Liquid Proto refinements before it is relayed.
type SendAgentMessageInput struct {
	To      string                `json:"to" jsonschema:"the recipient agent's identifier"`
	Message *agentv1.AgentMessage `json:"message" jsonschema:"the message: kind (1 note, 2 request, 3 reply, 4 status), text (1 to 16384 characters), optional in_reply_to (an envelope id) and an optional reference {label, uri}"`
}

// AgentEnvelopeView is one envelope as an MCP caller sees it.
type AgentEnvelopeView struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Tier string `json:"tier"`
	// Message is the sender's message, relayed unchanged.
	Message *agentv1.AgentMessage `json:"message"`
	SentAt  string                `json:"sent_at"`
}

// FetchAgentInboxInput bounds one fetch of the caller's inbox.
type FetchAgentInboxInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"at most this many envelopes, oldest first; default 16, maximum 256"`
}

// FetchAgentInboxOutput lists unacknowledged envelopes, oldest first.
type FetchAgentInboxOutput struct {
	Envelopes []AgentEnvelopeView `json:"envelopes"`
}

// AcknowledgeAgentInboxInput names fetched envelopes the caller has handled.
type AcknowledgeAgentInboxInput struct {
	IDs []string `json:"ids" jsonschema:"envelope identifiers returned by FetchAgentInbox"`
}

// AcknowledgeAgentInboxOutput counts envelopes removed from the inbox.
type AcknowledgeAgentInboxOutput struct {
	Acknowledged int `json:"acknowledged"`
}

// agentMessaging serves the agent messaging tools for one relay.
type agentMessaging struct {
	relay     *relay.Relay[*agentv1.AgentMessage]
	messenger *relay.Messenger[*agentv1.AgentMessage]
}

// WithAgentMessaging adds the agent messaging MCP tools over core. A host or
// network agent registers its address, sends to another agent by identifier,
// and fetches and acknowledges its own inbox; the tools act only as the
// caller's verified identity, so they work only behind AgentMCPHandler. The
// relay carries the agent message contract, so every tool speaks it.
func WithAgentMessaging(core *relay.Relay[*agentv1.AgentMessage]) Option {
	return func(service *Service) {
		messenger, err := relay.NewMessenger(core)
		if err != nil {
			service.consumerMCPTools = append(service.consumerMCPTools, func(service *Service) error {
				return fmt.Errorf("%w: agent messaging: %w", ErrInvalidMCPTool, err)
			})
			return
		}
		messaging := &agentMessaging{relay: core, messenger: messenger}
		for _, option := range []Option{
			WithMCPTool(mcp.Tool{Name: RegisterAgentAddressTool, Description: "Register (or, after a restart, re-register) the calling agent's session address so other agents can message it. Messages queued while the agent was away are kept."}, messaging.register),
			WithMCPTool(mcp.Tool{Name: SendAgentMessageTool, Description: "Send a message to another registered agent. The caller must have registered its own address first. A message is a proposal; it grants the recipient no permission."}, messaging.send),
			WithMCPTool(mcp.Tool{Name: FetchAgentInboxTool, Description: "Fetch the calling agent's unacknowledged messages, oldest first. A fetched message is returned again until it is acknowledged."}, messaging.fetch),
			WithMCPTool(mcp.Tool{Name: AcknowledgeAgentInboxTool, Description: "Acknowledge fetched messages so they leave the calling agent's inbox."}, messaging.acknowledge),
		} {
			option(service)
		}
	}
}

func (messaging *agentMessaging) register(ctx context.Context, _ *mcp.CallToolRequest, input RegisterAgentAddressInput) (*mcp.CallToolResult, AgentRegistrationView, error) {
	identity, err := verifiedAgentIdentity(ctx)
	if err != nil {
		return nil, AgentRegistrationView{}, err
	}
	build, known := agentAddressBuilders[input.Provider]
	if !known {
		return nil, AgentRegistrationView{}, fmt.Errorf("%w: provider %q cannot register over MCP", ErrInvalidRequest, input.Provider)
	}
	address, err := build(uuid.MustParse(identity.sessionID), input.Endpoint)
	if err != nil {
		return nil, AgentRegistrationView{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	registration := relay.Registration{Agent: relay.AgentID(identity.id), Address: address}
	if err := messaging.relay.Register(ctx, registration); err != nil {
		return nil, AgentRegistrationView{}, err
	}
	return nil, AgentRegistrationView{Agent: identity.id, Tier: registration.Kind().String(), Address: address.Key()}, nil
}

func (messaging *agentMessaging) send(ctx context.Context, _ *mcp.CallToolRequest, input SendAgentMessageInput) (*mcp.CallToolResult, AgentEnvelopeView, error) {
	identity, err := verifiedAgentIdentity(ctx)
	if err != nil {
		return nil, AgentEnvelopeView{}, err
	}
	sender, err := messaging.relay.Resolve(ctx, relay.AgentID(identity.id))
	if err != nil {
		return nil, AgentEnvelopeView{}, fmt.Errorf("register your address before sending: %w", err)
	}
	if err := agentv1.ValidateAgentMessage(input.Message); err != nil {
		return nil, AgentEnvelopeView{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	recipient, err := messaging.relay.Resolve(ctx, relay.AgentID(input.To))
	if err != nil {
		return nil, AgentEnvelopeView{}, err
	}
	envelope, err := messaging.messenger.Send(ctx, sender.Address, recipient.Address, input.Message)
	if err != nil {
		return nil, AgentEnvelopeView{}, err
	}
	return nil, agentEnvelopeView(envelope), nil
}

func (messaging *agentMessaging) fetch(ctx context.Context, _ *mcp.CallToolRequest, input FetchAgentInboxInput) (*mcp.CallToolResult, FetchAgentInboxOutput, error) {
	identity, err := verifiedAgentIdentity(ctx)
	if err != nil {
		return nil, FetchAgentInboxOutput{}, err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultInboxFetchLimit
	}
	envelopes, err := messaging.messenger.Fetch(ctx, relay.AgentID(identity.id), min(limit, maxInboxFetchLimit))
	if err != nil {
		return nil, FetchAgentInboxOutput{}, err
	}
	output := FetchAgentInboxOutput{Envelopes: make([]AgentEnvelopeView, 0, len(envelopes))}
	for _, envelope := range envelopes {
		output.Envelopes = append(output.Envelopes, agentEnvelopeView(envelope))
	}
	return nil, output, nil
}

func (messaging *agentMessaging) acknowledge(ctx context.Context, _ *mcp.CallToolRequest, input AcknowledgeAgentInboxInput) (*mcp.CallToolResult, AcknowledgeAgentInboxOutput, error) {
	identity, err := verifiedAgentIdentity(ctx)
	if err != nil {
		return nil, AcknowledgeAgentInboxOutput{}, err
	}
	identifiers := make([]uuid.UUID, 0, len(input.IDs))
	for _, raw := range input.IDs {
		identifier, err := uuid.Parse(raw)
		if err != nil {
			return nil, AcknowledgeAgentInboxOutput{}, fmt.Errorf("%w: envelope id %q: %w", ErrInvalidRequest, raw, err)
		}
		identifiers = append(identifiers, identifier)
	}
	removed, err := messaging.messenger.Acknowledge(ctx, relay.AgentID(identity.id), identifiers)
	if err != nil {
		return nil, AcknowledgeAgentInboxOutput{}, err
	}
	return nil, AcknowledgeAgentInboxOutput{Acknowledged: removed}, nil
}

func agentEnvelopeView(envelope relay.Envelope[*agentv1.AgentMessage]) AgentEnvelopeView {
	return AgentEnvelopeView{
		ID:      envelope.ID.String(),
		From:    string(envelope.FromAgent),
		To:      string(envelope.ToAgent),
		Tier:    envelope.Tier.String(),
		Message: envelope.Body,
		SentAt:  envelope.SentAt.UTC().Format(time.RFC3339Nano),
	}
}
