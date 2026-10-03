# relay — agent messaging across tiers

`services/relay` delivers messages between **agents**. An agent is a named
worker — versioned instructions plus a live conversation session — that holds
assignments and receives messages at a typed **agent address**. A message from
an agent is a proposal; it is never permission to execute anything.

Every address is a provider's type under `ipc/model/<provider>`, and the type
alone fixes the **tier** the message travels:

| Tier | Meaning | Address types today |
|---|---|---|
| `in_process` | same runtime: a direct call, no capability | `copilot.InProcessAddress` — a Workbench session on the adapter mounted in this runtime |
| `host` | another process on this machine, through a unix socket or loopback | `claudecode.HostAddress` — a Claude Code session and its local socket |
| `network` | another machine, through `ipc/net` | `copilot.NetworkAddress` — a Workbench session on another adapter |

Each envelope records the widest tier between sender and recipient, and the
relay exports `csf_relay_envelopes_sent_total{tier}` and
`csf_relay_envelopes_delivered_total{tier}` so avoidable `host` and `network`
crossings are measurable.

## Mounting it (bare binary first)

The relay is a runtime service and owns no listener. A binary builds it, mounts
it into its `runtime.HostRuntime` (or starts it on a `runtime.Scope`), and
hands it to whatever needs it:

```go
core, err := relay.NewRelay[*agentv1.AgentMessage](relay.WithMetrics(registry))
host.Mount("relay", core)                          // before anything that sends
service, err := csf.New(csf.WithAgentMessaging(core)) // MCP tools for host/network agents
messenger, err := relay.NewMessenger(core)            // the same contract, in-process
```

A relay carries exactly one message type, its contract: every sender, inbox,
receiver and MCP tool on it agrees on that type at compile time, and nothing is
erased or asserted between them. CSF's relay carries
`candace.agent.v1.AgentMessage` (`proto/candace/agent/v1/agent.proto`):
a kind, the text, an optional `in_reply_to` and an optional reference, each
refined by Liquid Proto. `relaytest.DescribeRelayConformance[Body]` is the
messaging contract any relay instantiation runs.

`app/csf` does exactly this, and binds every Workbench session's
`register_agent` / `send_agent_message` tools through
`copilotadapter.SessionMessaging`; a message to a Workbench agent becomes a
queued prompt in its session.

## Using it from a Claude Code session (host tier)

A Claude Code session is a host-tier agent: it calls CSF's **authenticated**
agent MCP endpoint (`csf.Service.AgentMCPHandler`, mounted by `app/csf` at
`/mcp/agent` when started with `--agent-mcp-key-file`). Every request carries
three headers minted for that agent and session by the host that holds the
signing key (`csf.AgentMCPAuthenticator.AgentMCPHeaders`):
`Authorization: Bearer v1.…`, `X-CSF-Agent-ID`, `X-CSF-Session-ID`.

1. Add the endpoint to the session's MCP configuration (for example a project
   `.mcp.json` entry of type `http` whose `url` is the loopback address and
   `/mcp/agent` path, with the three headers above).
2. Register once per session, and again after every restart:
   `RegisterAgentAddress {"provider": "claudecode", "endpoint": "127.0.0.1:14111"}`
   (or `"unix:/absolute/socket/path"` when CSF serves a unix socket). Only a
   unix socket or a loopback address is accepted, so the registration is
   host-tier by construction. Messages queued while the session was away are
   kept.
3. Send: `SendAgentMessage {"to": "reviewer", "message": {"kind": 2, "text": "…"}}`.
   Kind is 1 note, 2 request, 3 reply, 4 status; the message is refused unless
   it satisfies the contract. A Workbench agent reads it as a queued prompt.
4. Poll: `FetchAgentInbox {"limit": 16}` returns unacknowledged envelopes
   oldest first, each with `id`, `from`, `tier`, `message`, `sent_at`; the same
   envelopes come back until
   `AcknowledgeAgentInbox {"ids": ["…"]}` removes them.

## Limits

- Inboxes and registrations are in memory: a process restart loses them. The
  durable registry, claims and checkpoints are slice A1; any `IRegistry`
  implementation must keep the semantics written on that interface.
- Host and network agents pull; nothing pushes to them yet.
- In-process delivery is at most once: an envelope taken by `Receive` whose
  prompt submission then fails is logged, not retried.
- There is no command yet that mints agent MCP headers for an out-of-process
  agent; the host that holds the signing key must call `AgentMCPHeaders`.
