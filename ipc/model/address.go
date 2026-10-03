// Copyright 2026 Candace Labs

package model

import (
	"strings"

	"github.com/candacelabs/csf/ipc"
)

// addressKeySeparator joins the parts of an [AddressKey].
const addressKeySeparator = "/"

// IAgentAddress is where one agent's live conversation session receives
// messages. Each provider declares its own address types beside its brain,
// under ipc/model/<provider>, because what identifies a session is the
// provider's to say: a Claude Code session is a session identifier plus the
// local socket it is reached on, a Copilot session is a session identifier
// reached through the Copilot provider.
//
// The tier is part of the address's TYPE, not a field: every address type
// embeds exactly one of [InProcessTier], [HostTier] or [NetworkTier], and the
// interface is closed by an unexported method only those three markers carry.
// That makes it a sealed sum type over the three tiers: an address that a
// caller built as in-process cannot be routed through a socket, because no
// value of its type answers any other tier.
type IAgentAddress interface {
	// Provider names the provider that owns this address type.
	Provider() string
	// Key identifies the session uniquely across providers and tiers. It is
	// the address's stable identity in registries, inboxes and logs.
	Key() string
	// Tier is how far a message to this address travels from the runtime
	// that holds the inbox.
	Tier() ipc.Tier
	// tierMarker seals the interface to the three tier markers.
	tierMarker()
}

// InProcessTier marks an address whose session runs in the same runtime: a
// direct call or a channel reaches it, with no capability.
type InProcessTier struct{}

// Tier is [ipc.TierInProcess].
func (InProcessTier) Tier() ipc.Tier { return ipc.TierInProcess }

func (InProcessTier) tierMarker() {}

// HostTier marks an address whose session runs in another process on the
// same machine, reached through an ipc capability (a unix socket, or loopback
// through ipc/net).
type HostTier struct{}

// Tier is [ipc.TierHost].
func (HostTier) Tier() ipc.Tier { return ipc.TierHost }

func (HostTier) tierMarker() {}

// NetworkTier marks an address whose session runs on another machine,
// reached through ipc/net.
type NetworkTier struct{}

// Tier is [ipc.TierNetwork].
func (NetworkTier) Tier() ipc.Tier { return ipc.TierNetwork }

func (NetworkTier) tierMarker() {}

// AddressKey builds an address key from the provider, the tier and the
// provider's own identifying parts, so two providers or two tiers can never
// produce the same key for the same session identifier.
func AddressKey(provider string, tier ipc.Tier, parts ...string) string {
	return strings.Join(append([]string{provider, tier.String()}, parts...), addressKeySeparator)
}
