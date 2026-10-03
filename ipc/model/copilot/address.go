// Copyright 2026 Candace Labs

package copilot

import (
	"errors"
	"fmt"
	stdnet "net"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/ipc"
	"github.com/candacelabs/csf/ipc/model"
)

// ErrNoSession reports an address built without a session identifier.
var ErrNoSession = errors.New("copilot address: a nonzero session identifier is required")

// InProcessAddress is a Copilot Workbench session multiplexed on the bridge of
// a Copilot adapter mounted in the same runtime as the inbox. Messages reach
// it by a direct call into that adapter, so it is in the in-process tier.
// Build one only in the composition that mounts the adapter.
type InProcessAddress struct {
	model.InProcessTier
	Session uuid.UUID
}

// NewInProcessAddress addresses session on the adapter mounted in this runtime.
func NewInProcessAddress(session uuid.UUID) (InProcessAddress, error) {
	if session == uuid.Nil {
		return InProcessAddress{}, ErrNoSession
	}
	return InProcessAddress{Session: session}, nil
}

// Provider is [ProviderName].
func (address InProcessAddress) Provider() string { return ProviderName }

// Key is copilot/in_process/<session>.
func (address InProcessAddress) Key() string {
	return model.AddressKey(ProviderName, ipc.TierInProcess, address.Session.String())
}

// NetworkAddress is a Copilot Workbench session on an adapter in another
// runtime, reached through the Copilot provider over ipc/net. Adapter is that
// adapter's host:port; on this fleet it is a tailnet address.
type NetworkAddress struct {
	model.NetworkTier
	Session uuid.UUID
	Adapter string
}

// NewNetworkAddress addresses session on the adapter at adapter (host:port).
func NewNetworkAddress(session uuid.UUID, adapter string) (NetworkAddress, error) {
	if session == uuid.Nil {
		return NetworkAddress{}, ErrNoSession
	}
	if _, _, err := stdnet.SplitHostPort(adapter); err != nil {
		return NetworkAddress{}, fmt.Errorf("copilot address: adapter %q must be host:port: %w", adapter, err)
	}
	return NetworkAddress{Session: session, Adapter: adapter}, nil
}

// Provider is [ProviderName].
func (address NetworkAddress) Provider() string { return ProviderName }

// Key is copilot/network/<adapter>/<session>.
func (address NetworkAddress) Key() string {
	return model.AddressKey(ProviderName, ipc.TierNetwork, address.Adapter, address.Session.String())
}
