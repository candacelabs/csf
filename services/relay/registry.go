// Copyright 2026 Candace Labs

package relay

import (
	"context"
	"fmt"
	"maps"
	"sync/atomic"
)

// IRegistry records which address each agent's live session is at. The relay
// consults it to route; it never decides placement or ownership.
//
// [MemoryRegistry] is the implementation until slice A1 provides the durable
// one (stored through csfpg after S4). A durable implementation must keep these
// semantics:
//
//   - Register is an upsert keyed by agent: re-registering replaces the
//     agent's address, and the old address stops resolving at once.
//   - One address belongs to at most one agent; a second agent registering
//     a held address fails with [ErrAddressTaken].
//   - Resolve and Locate read the latest committed registration and report
//     [ErrUnknownAgent] and [ErrUnknownAddress] respectively.
//   - Each address round-trips: a registration read back must carry a value
//     of the same provider address type, equal to the one registered, so its
//     tier is unchanged by storage.
//
// A registry needs no coordination with inboxes: the relay opens an agent's
// inbox before it calls Register, so every registration a registry commits
// already has an inbox. A failed Register leaves no registration behind.
type IRegistry interface {
	Register(ctx context.Context, registration Registration) error
	Resolve(ctx context.Context, agent AgentID) (Registration, error)
	Locate(ctx context.Context, addressKey string) (Registration, error)
}

// registrySnapshot is one immutable version of the registry's contents.
type registrySnapshot struct {
	byAgent map[AgentID]Registration
	byKey   map[string]AgentID
}

// MemoryRegistry keeps registrations in this process. Reads take the current
// immutable snapshot; a write copies it and publishes the copy, so readers
// never wait and registration — rare next to routing — pays for the copy.
type MemoryRegistry struct {
	current atomic.Pointer[registrySnapshot]
}

// NewMemoryRegistry returns an empty in-memory registry.
func NewMemoryRegistry() *MemoryRegistry {
	registry := &MemoryRegistry{}
	registry.current.Store(&registrySnapshot{byAgent: map[AgentID]Registration{}, byKey: map[string]AgentID{}})
	return registry
}

// Register upserts the agent's registration.
func (registry *MemoryRegistry) Register(ctx context.Context, registration Registration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := registration.Validate(); err != nil {
		return err
	}
	key := registration.Address.Key()
	for {
		current := registry.current.Load()
		if holder, held := current.byKey[key]; held && holder != registration.Agent {
			return fmt.Errorf("%w: %s belongs to %q", ErrAddressTaken, key, holder)
		}
		next := &registrySnapshot{byAgent: maps.Clone(current.byAgent), byKey: maps.Clone(current.byKey)}
		if previous, registered := next.byAgent[registration.Agent]; registered {
			delete(next.byKey, previous.Address.Key())
		}
		next.byAgent[registration.Agent] = registration
		next.byKey[key] = registration.Agent
		if registry.current.CompareAndSwap(current, next) {
			return nil
		}
	}
}

// Resolve returns the agent's current registration.
func (registry *MemoryRegistry) Resolve(ctx context.Context, agent AgentID) (Registration, error) {
	if err := ctx.Err(); err != nil {
		return Registration{}, err
	}
	registration, registered := registry.current.Load().byAgent[agent]
	if !registered {
		return Registration{}, fmt.Errorf("%w: %q", ErrUnknownAgent, agent)
	}
	return registration, nil
}

// Locate returns the registration currently holding addressKey.
func (registry *MemoryRegistry) Locate(ctx context.Context, addressKey string) (Registration, error) {
	if err := ctx.Err(); err != nil {
		return Registration{}, err
	}
	current := registry.current.Load()
	agent, held := current.byKey[addressKey]
	if !held {
		return Registration{}, fmt.Errorf("%w: %s", ErrUnknownAddress, addressKey)
	}
	return current.byAgent[agent], nil
}
