// Copyright 2026 Candace Labs

package relay

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/ipc"
	"github.com/candacelabs/csf/ipc/model"
)

var (
	// ErrInvalidRegistration reports a registration without a valid agent
	// identifier or address.
	ErrInvalidRegistration = errors.New("relay: invalid registration")
	// ErrUnknownAgent reports an agent that has never registered.
	ErrUnknownAgent = errors.New("relay: agent is not registered")
	// ErrUnknownAddress reports an address no agent currently holds — never
	// registered, or replaced when its agent re-registered after a restart.
	ErrUnknownAddress = errors.New("relay: no agent is registered at that address")
	// ErrAddressTaken reports an address another agent already holds.
	ErrAddressTaken = errors.New("relay: address is registered to another agent")
	// ErrNotStarted reports use of a relay whose runtime has not started it.
	ErrNotStarted = errors.New("relay: not started")
	// ErrStopped reports use of a relay whose scope has ended.
	ErrStopped = errors.New("relay: stopped")
)

// agentIDPattern is the agent identifier grammar CSF's agent MCP
// authentication already verifies, so an MCP caller's identity is always a
// valid relay agent.
var agentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// AgentID names an agent: the named worker, not its current session.
type AgentID string

// Validate reports whether the identifier matches the agent grammar.
func (agent AgentID) Validate() error {
	if !agentIDPattern.MatchString(string(agent)) {
		return fmt.Errorf("%w: agent identifier %q must match %s", ErrInvalidRegistration, agent, agentIDPattern)
	}
	return nil
}

// Registration binds an agent to the address its live session receives
// messages at. An agent re-registers after a restart: its new session's
// address replaces the old one and its inbox, with everything still queued in
// it, carries over.
type Registration struct {
	Agent   AgentID
	Address model.IAgentAddress
}

// Kind is the registration's tier, which its address type decides.
func (registration Registration) Kind() ipc.Tier {
	if registration.Address == nil {
		return 0
	}
	return registration.Address.Tier()
}

// Validate reports whether the registration names a valid agent and address.
func (registration Registration) Validate() error {
	if err := registration.Agent.Validate(); err != nil {
		return err
	}
	if registration.Address == nil || registration.Address.Key() == "" || !registration.Kind().Valid() {
		return fmt.Errorf("%w: agent %q needs an address", ErrInvalidRegistration, registration.Agent)
	}
	return nil
}

// Envelope is one message from one agent to another. Body is the sender's
// payload; for an in-process recipient it is handed over by value without
// encoding, so a pointer inside it is shared rather than copied — ownership of
// anything it references passes to the recipient on delivery.
type Envelope[Body any] struct {
	ID        uuid.UUID
	FromAgent AgentID
	From      model.IAgentAddress
	ToAgent   AgentID
	To        model.IAgentAddress
	// Tier is the widest tier the two addresses reach: the boundary this
	// envelope crosses between sender and recipient.
	Tier   ipc.Tier
	Body   Body
	SentAt time.Time
}
