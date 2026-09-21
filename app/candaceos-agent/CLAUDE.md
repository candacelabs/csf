# candaceos-agent

`candaceos-agent` is the node-local execution boundary for CandaceOS. It has no
HTML, JavaScript, templates, or browser surface. A fleet controller sends one
desired app assignment at a time; the agent validates the request, fences old
leaders, and runs a narrowly constrained Docker Compose operation.

This is the only CandaceOS component that may receive `/var/run/docker.sock`.
Possession of that socket is equivalent to root authority on the Docker host,
so never mount it into a controller, dashboard, Copilot process, or app.

## API

Every success payload uses the shared `candace.candaceos.v1` protobuf contract
encoded as canonical ProtoJSON with proto field names. In particular, 64-bit
terms are JSON strings and desired states are protobuf enum names. Errors remain
small JSON objects. When `CANDACEOS_AGENT_TOKEN` is set, every route requires
`Authorization: Bearer <token>`.

- `GET /healthz` — authenticated process health.
- `GET /v1/status` — node identity, dry-run mode, highest accepted fence, and
  the last successfully reconciled assignment.
- `PUT /v1/assignment` — idempotently converge one Compose service.

Example request:

```json
{
  "fence": {"term": "7", "leader_id": "warden-a"},
  "assignment": {
    "app": "notes",
    "project": "candace-notes",
    "path": "notes",
    "desired_state": "DESIRED_STATE_RUNNING",
    "source_revision": "0123456789abcdef0123456789abcdef01234567",
    "content_sha256": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
  }
}
```

Both `fence` and `assignment` messages must be present. The unspecified desired
state is rejected. Unknown fields and trailing JSON values are also rejected.

`path` is a clean Git subtree below the configured workspace.
`source_revision` is the exact full commit object and `content_sha256` is the
approved canonical subtree digest. For a running assignment, the agent extracts
that commit into its bounded, content-addressed revision cache. When a source
remote is configured and the snapshot is not already cached, it first fetches
remote branches and tags into an agent-owned bare repository without checking
out or merging them into the read-only workspace. It then rejects links and
special files, independently verifies the approved digest, seals the snapshot
read-only, and runs Compose only there. The snapshot must contain one of `compose.yaml`,
`compose.yml`, `docker-compose.yaml`, or `docker-compose.yml`. `app` is the
Compose service name; `project` is the Compose project name. Stops use the
persisted Compose identity and do not require source files to remain present.

The preflight is always:

```text
docker compose --project-directory DIR --project-name PROJECT --file FILE config --quiet
```

For `running`, it is followed by `up -d --remove-orphans APP`. For `stopped`,
it is followed by `stop APP`. The agent never invokes `down`, removes
containers explicitly, or deletes volumes. Commands are executed directly as
argument vectors, never through a shell.

## Fencing and persistence

The state file records the highest accepted `(term, leader_id)` tuple. A lower
term is stale; a different leader in the same term conflicts. A higher term is
atomically persisted and fsynced before Compose is invoked, so a crash cannot
allow an old leader back in. If execution fails, the fence remains accepted and
the same leader may retry safely.

## Configuration

| Environment variable | Default | Meaning |
|---|---|---|
| `CANDACEOS_AGENT_BIND` | `127.0.0.1:8094` | HTTP listen address |
| `CANDACEOS_AGENT_TOKEN` | empty | Bearer token; mandatory for non-loopback binds |
| `CANDACEOS_AGENT_NODE_ID` | OS hostname | Stable node identifier |
| `CANDACEOS_AGENT_WORKSPACE` | `/var/lib/candaceos/apps` | Absolute app workspace |
| `CANDACEOS_AGENT_REVISION_ROOT` | `/var/lib/candaceos-agent/revisions` | Absolute immutable revision cache outside the workspace |
| `CANDACEOS_AGENT_REVISION_MAX_ENTRIES` | `128` | Maximum persistent revision snapshots |
| `CANDACEOS_AGENT_REVISION_MAX_BYTES` | `4294967296` | Maximum regular-file bytes across revision snapshots |
| `CANDACEOS_AGENT_SOURCE_REMOTE` | empty | Optional workspace Git remote name or URL fetched read-only before creating an uncached snapshot |
| `CANDACEOS_AGENT_SOURCE_REPOSITORY` | `/var/lib/candaceos-agent/source.git` | Writable agent-owned bare repository, separate from the workspace and revision cache |
| `CANDACEOS_AGENT_SOURCE_FETCH_TIMEOUT` | `30s` | Hard deadline for fetching and verifying an approved commit from the configured remote |
| `CANDACEOS_AGENT_STATE_FILE` | `/var/lib/candaceos-agent/state.json` | Atomic state file |
| `CANDACEOS_AGENT_DOCKER_BIN` | `docker` | Docker CLI executable |
| `CANDACEOS_AGENT_DRY_RUN` | `false` | Return plans without running Docker |

Loopback without a token is intended only for local development. Any
non-loopback bind fails closed unless a token is configured. In production,
place the API on the trusted node network as requested by the operator; do not
add firewall, Tailscale ACL, or host-network changes from this service.

Leaving `CANDACEOS_AGENT_SOURCE_REMOTE` empty preserves local-only behavior: an
approved commit must already exist in the workspace repository. A configured
remote uses the Git process's inherited credentials, including `SSH_AUTH_SOCK`
when the runtime explicitly passes that socket through. The agent invokes Git
directly, resolves a configured remote name from the workspace without writing
there, disables interactive credential prompts, fetches only branch and tag refs
into `CANDACEOS_AGENT_SOURCE_REPOSITORY`, and never pushes. Keep that bare
repository in the writable agent state volume; the app workspace remains
read-only. A previously verified cached snapshot remains usable if the remote
later becomes unavailable.

## Build and test

Run from the Go module root:

```bash
go test ./app/candaceos-agent/...
docker build -f app/candaceos-agent/Dockerfile -t candaceos-agent:dev .
```

The runtime image contains Git, the Docker CLI, and the Compose plugin because
the agent materializes exact commits and invokes Compose. Mount the workspace
read-only, the revision cache read-write at an identical host/container path,
the state directory read-write, and the Docker socket only into this container.
The image deliberately does not install or modify anything on the host.

Existing verified snapshots remain usable when either cache quota is full, but
new revisions fail closed before exceeding the configured capacity. There is no
automatic eviction because an existing snapshot may back a live Compose bind
mount. Stop reconciliation and the agent before manually removing revisions
that are no longer used from `CANDACEOS_AGENT_REVISION_ROOT`, then restart the
agent.
