// Copyright 2026 Candace Labs

// Package io is the umbrella for every boundary crossing a CSF process makes.
// Each crossing is a capability granted through a constructor and lives in a
// tier subpackage: io/inproc stays inside the runtime, io/kernel makes a
// syscall, io/ipc reaches another process on this machine, and io/net reaches
// another machine. This package holds only what every subpackage shares: the
// [Tier] that classifies how far a crossing reaches.
package io

import "fmt"

// Tier is how far a message or call travels from the code that sends it.
// Tiers are ordered: a crossing that touches two tiers reaches the wider one.
type Tier int

const (
	// TierInProcess stays inside one runtime: a direct call or a channel,
	// with no capability. Copilot sessions multiplexed on the bridge of a
	// Copilot service mounted in the same runtime are in this tier.
	TierInProcess Tier = iota + 1
	// TierKernel reaches the kernel through a syscall on this machine:
	// clock, files, and process control.
	TierKernel
	// TierIpc reaches another process on this machine through an ipc
	// capability: a unix socket, or loopback through io/net.
	TierIpc
	// TierNet reaches another machine through io/net.
	TierNet
)

// Tier names are the ontology's terms and the label values the relay's
// counters export; they are stable.
const (
	tierInProcessName = "in_process"
	tierKernelName    = "kernel"
	tierIpcName       = "ipc"
	tierNetName       = "net"
)

// Tiers lists every tier, narrowest first, so a caller can initialize one
// series per tier and a test can assert completeness.
var Tiers = []Tier{TierInProcess, TierKernel, TierIpc, TierNet}

// String is the tier's ontology name.
func (tier Tier) String() string {
	switch tier {
	case TierInProcess:
		return tierInProcessName
	case TierKernel:
		return tierKernelName
	case TierIpc:
		return tierIpcName
	case TierNet:
		return tierNetName
	default:
		return fmt.Sprintf("tier(%d)", int(tier))
	}
}

// Valid reports whether tier is one of [Tiers].
func (tier Tier) Valid() bool { return tier >= TierInProcess && tier <= TierNet }

// Widest is the tier a crossing between two endpoints reaches: an in-process
// sender talking to a host-tier receiver crosses the host tier.
func Widest(left Tier, right Tier) Tier {
	if left > right {
		return left
	}
	return right
}
