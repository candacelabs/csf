// Copyright 2026 Candace Labs

// Package relay delivers messages between agents. An agent is a named worker
// (versioned instructions plus a live conversation session) that receives
// messages at a typed agent address; the relay holds one inbox per registered
// agent and moves typed envelopes into it.
//
// It is named for what it does: it relays an envelope from a sender's address
// to a recipient's inbox and records how far the envelope travelled. It does
// not decide what an agent does with a message — a message from an agent is a
// proposal, never permission to execute anything — and it is not a durable
// queue: inboxes live in memory until the durable registry, claims and
// checkpoints arrive (slice A1, stored through csfpg after S4).
//
// # Tiers are decided by address types
//
// Every agent address is a provider's type from ipc/model/<provider>, and the
// type alone fixes its tier (see model.IAgentAddress): in_process for a session
// in this runtime, host for another process on this machine, network for
// another machine. Every [Envelope] records the widest tier its two addresses
// reach, and the relay counts envelopes per tier so avoidable crossings are
// measurable.
//
// The relay itself holds no capability and opens no socket. In-process agents
// exchange envelopes by direct calls into their inboxes ([Messenger.Send] and
// [Messenger.Receive]); a host or network agent reaches the same inbox through
// CSF's per-agent authenticated MCP endpoint, whose listener the binary grants
// from candace/ipc/net, and pulls it with [Messenger.Fetch] and
// [Messenger.Acknowledge].
//
// # Goroutines
//
// The relay is a runtime service: [Relay.Start] receives its scope, and every
// goroutine it needs — the directory's owner and one owner per inbox, each a
// candace/pkg/mailbox — starts through that scope. Nothing here calls go.
package relay
