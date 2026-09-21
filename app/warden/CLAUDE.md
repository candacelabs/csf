# CLAUDE.md — warden service dev guide

warden is the candacenet fleet watchdog: one binary on every fleet node,
Raft-style leader election over a static peer set, the leader watches peer
liveness and emails the operator on peer death. A single bound port per node
serves the node-to-node gRPC `WardenService` (cluster RPCs + `WatchCluster`
stream) multiplexed via cmux with the HTTP surface (dashboard + `/api/status` +
`/metrics`). See
[README.md](README.md) for the user-facing overview and configuration
reference.

## The contract package is the source of truth

`services/warden/` is the **frozen contract**: the core types
(`Node`, `ClusterView`, `Incident`, …), the HTTP wire protocol (`VoteRequest`,
`HeartbeatRequest`, route path constants), and every interface the subsystems
implement or consume (`ITransport`, `IRPCHandler`, `IStore`, `IViewSource`,
`IIncidentLog`, `INotifier`, `IClock`).

- **Do not edit `services/warden/`.** Every other package is written against
  its public API. If a contract change seems necessary, raise it — do not fork
  the types.
- Import it as `warden "github.com/candacelabs/csf/services/warden"`.

## Protobuf wire schema (source of truth for the gRPC plane)

`services/warden/proto/warden/v1/warden.proto` (package `candacenet.warden.v1`)
is the
Protocol-Buffers source of truth for the node-to-node wire messages and the
`WardenService` RPC surface — unary `Vote`/`Heartbeat`/`Identify` plus the
server-streaming `WatchCluster` (full-snapshot `ClusterViewUpdate`s keyed by a
`ClusterViewCursor`). The generated `warden.pb.go` and `warden_grpc.pb.go` are
committed; **do not hand-edit them.** The gRPC transport (client `services/warden/grpctransport`, server
`services/warden/grpcserver`, mux `services/warden/grpcmux`) is built against
this schema via the `services/warden/wireconv`
conversion layer; the schema is unchanged by that work and the HTTP/JSON RPC
transport it replaced has been retired.

The field-numbering / reserved-number policy and every protojson-vs-JSON delta
are documented in the `warden.proto` header. Schema-level contract specs
(protojson goldens, round-trip fidelity, unknown-field tolerance, zero-value
omission, `Membership.Supersedes` on proto-derived values) live in
`services/warden/proto/warden/v1/schema_contract_test.go`.

Toolchain (install once): `buf` binary from
<https://github.com/bufbuild/buf/releases> on PATH, plus the two plugins:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

Gates and regeneration (run from `services/warden/proto/`):

```bash
buf lint                                   # schema lint (STANDARD ruleset)
bash generate.sh                           # buf lint + buf generate (regenerate bindings)
#   or, from candace/:  go generate ./services/warden/proto/...

# Breaking-change gate: compare the working tree against the last committed
# schema (post-merge baseline: main). In a plain checkout:
buf breaking --against '.git#ref=main,subdir=candace/services/warden/proto'
# In a git *worktree* (whose .git is a file), point at the real repo .git:
buf breaking --against '<repo>/.git#ref=<full-sha>,subdir=candace/services/warden/proto'
```

`generate.sh` prepends the Go tool bin dir to PATH so a freshly `go install`-ed
plugin is picked up automatically. Note: `buf breaking` needs a baseline that
already contains the schema; run against a pre-schema commit it reports "no
.proto files" (nothing to break) rather than a clean pass.

## Package map

| Package              | One-liner                                                              |
| -------------------- | --------------------------------------------------------------------- |
| `services/warden`    | Frozen contract: types, wire protocol, interfaces, real clock.        |
| `services/warden/config`    | Load config from defaults → YAML → env; `Validate`; `Redacted`.       |
| `services/warden/discovery` | `IPeerDiscoverer` sources: `NewStatic`, `NewTailscale`, `NewFile`.     |
| `services/warden/election`  | Election state machine + peer-liveness + membership; `IViewSource` + `IRPCHandler`. |
| `services/warden/wireconv`  | Total, lossless conversions between the domain types and the `wardenv1` proto messages; the boundary the gRPC plane and protojson persistence cross. |
| `services/warden/grpcserver`| `WardenService` server: unary Vote/Heartbeat/Identify + `WatchCluster` stream (cursor dedup, drop-to-latest); gRPC error-code table. |
| `services/warden/grpcmux`   | Single-port cmux server: gRPC (h2c) + the gin HTTP engine on one listener; graceful drain of both. |
| `services/warden/grpctransport` | gRPC client implementing `warden.ITransport` (h2c, insecure creds, per-peer pooled conns, prompt reconnect). |
| `services/warden/store`     | Durable `PersistentState` (atomic write-then-rename file store; writes protojson, reads protojson AND legacy JSON). |
| `services/warden/testclock` | Deterministic fake `IClock` for tests.                                 |
| `services/warden/watchdog`  | Leader-only incident engine with dedup + cooldown; `IIncidentLog`.     |
| `services/warden/notify`    | `INotifier` implementations: SMTP, log, file.                          |
| `services/warden/dashboard` | SSR dashboard (HTMX, embedded offline assets) + `/api/status` JSON.   |
| `services/warden/metrics`   | Prometheus collectors + `/metrics` handler.                           |
| `cmd`                | `main.go`: config load, discoverer + component wiring, CSP supervisor. |
| `services/warden/proto/warden/v1` | Protobuf schema (`warden.proto`) + committed generated Go/gRPC bindings; wire source of truth for the gRPC plane. |
| `services/warden/internal/mocks` | Generated gomock doubles for the eight contract interfaces. Deliberately internal: the shapes are derived from the interfaces and would force `go.uber.org/mock` on embedders. |

## Membership & discovery

Discovery is **advisory**. `services/warden/discovery` only *reports* candidate nodes
as `warden.Roster` snapshots on a channel; it never touches membership. The
election manager consumes rosters, verifies candidates via
`ITransport.Identify`, and **only the leader** turns stable, verified candidates
into one-at-a-time voting-membership changes. **Quorum is always computed over
the persisted voter set (`Membership.Voters`)** — never over a roster or over
"reachable peers" — so a quiet/broken discovery source can never shrink quorum.

- Modes (`discovery.mode`, default `static`): `static` = the `peers` seed only;
  `tailscale` = poll `tailscaled`'s LocalAPI over its unix socket (`net/http`
  with a unix-socket `DialContext` — **do not** import `tailscale.com`); `file`
  = poll a JSON roster file (also a manual dynamic mode). The `peers` seed is
  required in every mode.
- Polling sources send the first snapshot, then **only on change**; on any
  read/parse error they log a rate-limited warning and **send nothing** (never
  an empty/partial roster). A valid empty roster *is* sent. Channel closes on
  ctx end. The package is IO-bound and uses the real clock — no `testclock`.
- A **joiner** ships the same config as the fleet with its own `node_id` not in
  `peers`; `cmd/main.go` synthesizes `Self` from `advertise_addr` and it runs as
  an observer until the leader admits it. This is why `config.Validate` relaxes
  the `node_id`-in-`peers` rule outside static mode.
- `discovery.CompileHostPattern` and `config` anchor host patterns identically
  (`\A(?:…)\z`) — keep the two in sync.

## Concurrency model (design principle, not an accident)

warden is channel-first / single-owner, **not** mutex-guarded shared state:

- The **election manager** and the **watchdog** are each one event-loop
  goroutine that solely owns its mutable state. External callers reach them by
  **request/reply over channels** and receive **immutable `ClusterView`
  snapshots** (`IViewSource.View()` returns a copy the caller may keep).
- `cmd/main.go` supervises with channels + `context`, never
  `sync.WaitGroup`-plus-shared-error-slice: cancellation flows in via the
  `signal.NotifyContext` context; each long-running goroutine (election,
  watchdog, HTTP server) reports its terminal error on one buffered channel;
  `main` cancels on the first signal or error, runs a bounded `srv.Shutdown`,
  and drains one result from every goroutine before exiting. No abandoned
  goroutines.

This is why `go test -race` is clean by construction. When adding code, prefer
a channel or a single owner; a small, well-scoped `sync.Mutex` is acceptable
only where it is genuinely clearest, with a comment justifying it — a
mutex-as-backbone will be rejected.

## Mocks and simulators

Two different kinds of test double live in this tree, and the boundary between
them is deliberate. **Do not convert one kind into the other.**

**Generated mocks (gomock).** The eight contract interfaces — `ITransport`,
`INotifier`, `IStore`, `IClock`, `IPeerDiscoverer`, `IViewSource`, `IIncidentLog`,
`IRPCHandler` — have expectation-based mocks generated by
[go.uber.org/mock](https://github.com/uber-go/mock) (the maintained fork of the
archived `golang/mock`; **not** the archived one) into `services/warden/internal/mocks/`
(package `mocks`, one `Mock<Interface>` type each). The generated code is
committed. The single `//go:generate` directive lives in
`services/warden/generate.go` (a directive-only file — it adds no types and
does not alter the frozen contract). Regenerate from `candace/` after any interface
change:

```bash
go install go.uber.org/mock/mockgen@v0.6.0   # once, onto PATH
go generate ./services/warden/...
```

Never point a `mockgen` directive at anything under `services/warden/proto/`: those bindings are
generated by `buf` (see the schema section above), not gomock, and stay
untouched.

**Behavioral simulators.** `services/warden/testclock` (a deterministic fake `IClock`
that models time as a state machine) and the election test harness
(`services/warden/election/harness_test.go`, which models an entire network of nodes as
a state machine) are **simulators, not mocks** — hand-written on purpose, not
gomock. Their whole job is to make the timing- and message-driven state machines
*deterministic*: they advance simulated time and deliver simulated RPCs in a
controlled order. Re-expressing them as expectation-based gomock doubles would
destroy that determinism — call-order/count expectations cannot say "step the
whole cluster forward N ticks and settle." **Do not convert them to gomock.**

**Which to reach for.** Use a generated mock when a test wants to control or
assert *calls* against a single collaborator: an `IStore` primed to fail `Save`, a
static `IViewSource`/`IIncidentLog` returning a fixed fixture, a simple
`INotifier`/`IPeerDiscoverer` stub. Use (or extend) a simulator when a test needs
a faithful *behavioral model* of time or the network. These two are not
interchangeable, and future contributors must not convert in either direction.

## Run a 3-process cluster locally

Each process is one "node"; they share the same peer list but bind different
ports and use separate data dirs. Use `WARDEN_LOG_FORMAT=console` for readable
logs and a 3-node peer set (quorum = 2). Run each in its own terminal:

```bash
# from the Go module root
PEERS="n1=127.0.0.1:7717,n2=127.0.0.1:7727,n3=127.0.0.1:7737"

# terminal 1
WARDEN_LOG_FORMAT=console WARDEN_NODE_ID=n1 WARDEN_BIND=:7717 \
  WARDEN_DATA_DIR=/tmp/warden-n1 WARDEN_PEERS="$PEERS" \
  WARDEN_NOTIFY_MODE=log go run ./app/warden/cmd

# terminal 2
WARDEN_LOG_FORMAT=console WARDEN_NODE_ID=n2 WARDEN_BIND=:7727 \
  WARDEN_DATA_DIR=/tmp/warden-n2 WARDEN_PEERS="$PEERS" \
  WARDEN_NOTIFY_MODE=log go run ./app/warden/cmd

# terminal 3
WARDEN_LOG_FORMAT=console WARDEN_NODE_ID=n3 WARDEN_BIND=:7737 \
  WARDEN_DATA_DIR=/tmp/warden-n3 WARDEN_PEERS="$PEERS" \
  WARDEN_NOTIFY_MODE=log go run ./app/warden/cmd

# observe: exactly one leader, shared authoritative view
curl -s localhost:7717/api/status | jq '{self,role,leader_id,authoritative}'
curl -s localhost:7717/metrics | grep -E '^warden_(is_leader|term)'
# kill one node (Ctrl-C) and watch the others re-elect and log a peer_dead incident
```

`WARDEN_NOTIFY_MODE=file WARDEN_NOTIFY_FILE=/tmp/warden-incidents.jsonl` sends
incidents to a file you can `tail -f` instead of the log.

## Commands

```bash
# from the Go module root

# format (required before committing; CI-equivalent check must be empty)
gofmt -l ./services/warden/... ./app/warden/...   # or: go fmt <same>

# vet + build + test
go vet ./services/warden/... ./app/warden/...
go build ./services/warden/... ./app/warden/...
go test ./services/warden/... ./app/warden/...
go test -race ./services/warden/config/...        # config, race-checked
go test -run TestName ./services/warden/<pkg>     # one Go test function
```

These suites are Ginkgo, and Ginkgo rejects `go test -count` with anything but
`-count=1` — it cannot rerun a suite in one process. To repeat a suite, use the
Ginkgo CLI at the pinned version:

```bash
go install github.com/onsi/ginkgo/v2/ginkgo@v2.32.0
ginkgo -race -repeat=2 ./services/warden/config   # or -until-it-fails
```

To pick individual specs rather than a whole Go test function, use Ginkgo's own
focus flag, `go test ./services/warden/<pkg> -args -ginkgo.focus='<regexp>'`.

```bash
# run the binary
go run ./app/warden/cmd -version
go run ./app/warden/cmd -config /etc/warden/warden.yaml
```

## Conventions

- **Logging:** structured, via `pkg/core` (`core.Logger`, a `*zerolog.Logger`).
  Log with fields, not string concatenation:
  `logger.Info().Str("node_id", id).Int("peers", n).Msg("...")`. `cmd/main.go`
  emits JSON to stdout by default and switches to the console writer only when
  `WARDEN_LOG_FORMAT=console`; it sets the global zerolog level from
  `log_level`.
- **Errors:** always checked and wrapped with context using `%w`:
  `fmt.Errorf("loading config %q: %w", path, err)`. Sentinel errors follow the
  `ErrXxx` pattern.
- **Imports:** three groups (stdlib / third-party / local) separated by blank
  lines.
- **Dependencies:** keep them minimal — stdlib, `gopkg.in/yaml.v3`, `zerolog`
  via `pkg/core`, and the contract package. Do not add modules or run
  `go get`/`go mod tidy` casually; `services/warden/config` in particular imports only
  stdlib, yaml.v3, and the contract package.
- **Testing:** table-driven; the fake `IClock` in `services/warden/testclock` makes the
  timing-dependent state machines deterministic — do not sleep on the wall
  clock in tests.
