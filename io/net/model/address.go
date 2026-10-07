// Copyright 2026 Candace Labs

package model

import (
	"strings"

	"github.com/candacelabs/csf/io"
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
// embeds exactly one of [InProcessTier], [IpcTier] or [NetTier], and the
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
	Tier() io.Tier
	// tierMarker seals the interface to the three tier markers.
	tierMarker()
}

// InProcessTier marks an address whose session runs in the same runtime: a
// direct call or a channel reaches it, with no capability.
type InProcessTier struct{}

// Tier is [io.TierInProcess].
func (InProcessTier) Tier() io.Tier { return io.TierInProcess }

func (InProcessTier) tierMarker() {}

// IpcTier marks an address whose session runs in another process on the
// same machine, reached through an ipc capability (a unix socket, or loopback
// through ipc/net).
type IpcTier struct{}

// Tier is [io.TierIpc].
func (IpcTier) Tier() io.Tier { return io.TierIpc }

func (IpcTier) tierMarker() {}

// NetTier marks an address whose session runs on another machine,
// reached through ipc/net.
type NetTier struct{}

// Tier is [io.TierNet].
func (NetTier) Tier() io.Tier { return io.TierNet }

func (NetTier) tierMarker() {}

// AddressKey builds an address key from the provider, the tier and the
// provider's own identifying parts, so two providers or two tiers can never
// produce the same key for the same session identifier.
func AddressKey(provider string, tier io.Tier, parts ...string) string {
	return strings.Join(append([]string{provider, tier.String()}, parts...), addressKeySeparator)
}
