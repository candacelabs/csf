// Copyright 2026 Candace Labs

package intake

import (
	"errors"

	"github.com/candacelabs/csf/ipc"
	"github.com/candacelabs/csf/ipc/model"
)

// ProviderName is the Provider of every [SourceAddress].
const ProviderName = "intake"

// ErrNoSource reports a source address built without a source name.
var ErrNoSource = errors.New("intake: a source address needs a source name")

// SourceAddress is where an intake's envelopes come from. The relay only
// carries envelopes between registered addresses, so the intake registers
// itself at this address before it sends; nothing is ever delivered to it.
// It is in-process: the intake runs in the runtime that holds the relay.
type SourceAddress struct {
	model.InProcessTier
	Source string
}

// NewSourceAddress addresses the intake named source.
func NewSourceAddress(source string) (SourceAddress, error) {
	if source == "" {
		return SourceAddress{}, ErrNoSource
	}
	return SourceAddress{Source: source}, nil
}

// Provider is [ProviderName].
func (address SourceAddress) Provider() string { return ProviderName }

// Key is intake/in_process/<source>.
func (address SourceAddress) Key() string {
	return model.AddressKey(ProviderName, ipc.TierInProcess, address.Source)
}
