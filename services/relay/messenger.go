// Copyright 2026 Candace Labs

package relay

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/ipc/model"
)

// IMessenger is the agent messaging contract: send a typed envelope to an
// agent address, and receive from an inbox. The addresses' types decide the
// tier; Body is the message type both sides agree on.
type IMessenger[Body any] interface {
	// Send delivers body from the agent registered at from to the agent
	// registered at to, and returns the envelope as queued.
	Send(ctx context.Context, from model.IAgentAddress, to model.IAgentAddress, body Body) (Envelope[Body], error)
	// Receive takes the agent's oldest envelope, waiting until one arrives.
	Receive(ctx context.Context, agent AgentID) (Envelope[Body], error)
}

// Messenger is a [Relay]'s agent-facing view. It shares its relay's message
// type: a relay carries one contract, and every messenger over it speaks it.
type Messenger[Body any] struct {
	relay *Relay[Body]
}

var _ IMessenger[string] = (*Messenger[string])(nil)

// NewMessenger returns a messenger over relay.
func NewMessenger[Body any](relay *Relay[Body]) (*Messenger[Body], error) {
	if relay == nil {
		return nil, errors.New("relay: NewMessenger needs a relay")
	}
	return &Messenger[Body]{relay: relay}, nil
}

// Send delivers body; see [IMessenger.Send].
func (messenger *Messenger[Body]) Send(ctx context.Context, from model.IAgentAddress, to model.IAgentAddress, body Body) (Envelope[Body], error) {
	return messenger.relay.send(ctx, from, to, body)
}

// Receive takes the oldest envelope; see [IMessenger.Receive]. It is the
// in-process agent's side: the handoff is the delivery.
func (messenger *Messenger[Body]) Receive(ctx context.Context, agent AgentID) (Envelope[Body], error) {
	return messenger.relay.receive(ctx, agent)
}

// Fetch returns up to limit of the agent's unacknowledged envelopes, oldest
// first, without removing them. It is the host or network agent's side: what
// it fetched is redelivered until it acknowledges it.
func (messenger *Messenger[Body]) Fetch(ctx context.Context, agent AgentID, limit int) ([]Envelope[Body], error) {
	return messenger.relay.fetch(ctx, agent, limit)
}

// Acknowledge removes fetched envelopes from the agent's inbox and reports
// how many it removed; identifiers already acknowledged are ignored.
func (messenger *Messenger[Body]) Acknowledge(ctx context.Context, agent AgentID, identifiers []uuid.UUID) (int, error) {
	return messenger.relay.acknowledge(ctx, agent, identifiers)
}
