# CandaceOS Core service guide

`candaceos-core` is the single-operator control plane for a CandaceOS fleet.
It owns approval policy, app/deployment intent, run projection, and the
operator web UI. Core's packages live in `services/candaceos`; this directory
holds only `cmd/` and `bootstrap/`. A selected agent-harness adapter owns its
runtime SDK and session lifecycle. `services/candaceos/harness` exposes
`Factory`, `Runtime`, and `Host` as the compiled-in provider seam; the stock
OpenCode implementation is `services/candaceos/harness/opencode`.
`app/candaceos-core/bootstrap` is the composition root, and it also owns the
component tier: bootstrap's own built-in steps and every `WithComponent`
extension are `services/candaceos/component` definitions resolved as one
topologically ordered bring-up list. Core does not elect the fleet leader and
never receives a Docker socket.

## Boundaries

- Warden remains the authority for membership, health, and leader identity.
- Node `role` and labels are fixed Core configuration used by placement. They
  are exposed by `candace_fleet_status` and in node cards; Warden's current
  `leader_id` remains a separate, dynamic observation.
- `candaceos-agent` is the only process allowed to reconcile containers.
- Copilot runs with `ModeEmpty`. Built-ins are explicitly enabled, then
  constrained by managed policy and the host permission callback; fleet reads
  and reconciliations are typed custom tools. Copilot SDK types stay in
  `services/candaceos/operator/copilot_harness.go`; Controller receives only
  backend-neutral events and permission-policy inputs.
- Copilot steering is native. `immediate` delivery injects the prompt into the
  in-flight run, which the runtime reports back as a `steering` user message;
  it does not abort and resubmit the way OpenCode must. `enqueue` delivery
  defers the prompt to its own agentic loop. The runtime's idle drain takes
  queue depth as an input and processes the queue instead of reporting idle, so
  `session.idle` already means the provider finished everything Core admitted.
  Only `session.idle` may conclude a run: `assistant.idle` fires between queued
  follow-ups and while background agents or attached shell commands are still
  running.
- The Copilot GitHub credential is transport-specific. A spawned CLI takes a
  client-level token; an externally managed runtime rejects one and takes a
  per-session token instead. Supplying a client-level token alongside a
  `URIConnection` makes `copilot.NewClient` panic.
- `ManagedSettings` is startup-only and must be re-supplied on resume, because
  omitting it clears the injected layer. `exit_plan_mode` and `ask_user` are
  registered only when their handler is configured, so leaving those handlers
  unset removes the tools rather than auto-approving them.
- Ollama uses the official HTTP API through the stdlib adapter in
  `services/candaceos/operator/ollama_harness.go`. Its tool surface is
  deliberately smaller: fleet status plus approval-bound reconciliation of an
  already-materialized app. The Liquid Proto CoreConfig bounds context, total
  tool calls, and turn duration.
- OpenCode 1.18.21 runs as a separate private container in the same one-box
  Compose project. It owns the agent, model, and tool loop. Only Core's
  `0.0.0.0:7780` listener is published; the sidecar has no control-network
  route or Docker socket.
- The OpenCode package uses the official generated Go SDK v0.19.2 for typed
  session, message, part, tool-state, and error APIs. `sdkAdapter` owns only
  authentication, workspace scoping, request timeouts, the three generated
  endpoint gaps (`global/health`, `session/status`, and
  `session/{id}/prompt_async`), and forward-compatible event invalidation.
- Candace maps provider state into the normalized Liquid Proto `HarnessEvent`,
  fences work to the current run, and implements bounded FIFO follow-ups plus
  abort-and-resubmit interruption. Those mappings and queues are in memory;
  durable restart replay is a later milestone.
- PostgreSQL stores relational control-plane records.
  `services/candaceos/store` owns the pool and the migrations; its sqlc output
  stays private in
  `services/candaceos/internal/storedb`. Copilot's own session directory is
  opaque and mounted separately.
- Complete harness events may hydrate the live UI. Streaming deltas are transient
  and must not be replayed after a final event.
- Custom harnesses receive only the typed public Host capabilities. They carry
  the supplied prompt run ID into normalized events; Controller retains target
  validation, fleet authority, approval, fencing, and reconciliation. Do not
  expose Controller or internal event records as SDK types.
- `harness.Runner` is the shared provider-neutral actor for SDK-native session
  lifecycles. It serializes install, replay activation, early callback
  buffering, and close while native send, abort, and cleanup calls run outside
  the actor; Copilot uses this path.
- OpenCode intentionally keeps a separate actor. Candace owns its bounded FIFO
  admission and abort-and-resubmit transitions, and `Host.Publish` can fail, so
  queue state, run correlation, and publication retry stay in the OpenCode
  runtime while the official SDK owns provider-specific session, model, and
  tool APIs. Do not force these semantics through `harness.Runner`.
- No quorum blocks fleet reconciliation: do not create or dispatch deployments.
  Unrelated Copilot workspace and publishing approvals remain available only
  when that adapter is selected.

## Local checks

Run from `candace/`:

```bash
go fmt ./app/candaceos-core/... ./services/candaceos/...
go test ./app/candaceos-core/... ./services/candaceos/...
go build ./app/candaceos-core/cmd
```

The normal runtime is containerized. `CANDACEOS_HARNESS_BACKEND` is the
canonical selector: fleet pins `copilot-cli`, while the isolated acceptance
stack pins `demo`; fleet may explicitly select `ollama`, and the one-box stack
may select `opencode`. `CANDACEOS_MODE` is a legacy load-boundary alias only.
Demo mode must never pretend to have changed a real node.
