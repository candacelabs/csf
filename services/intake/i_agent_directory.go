// Copyright 2026 Candace Labs

package intake

import (
	"context"

	"github.com/candacelabs/csf/services/relay"
)

// IAgentDirectory is the part of the relay the intake registers itself with
// and resolves owning agents through. *relay.Relay[*intakev1.Event] satisfies
// it.
type IAgentDirectory interface {
	Register(ctx context.Context, registration relay.Registration) error
	Resolve(ctx context.Context, agent relay.AgentID) (relay.Registration, error)
}
