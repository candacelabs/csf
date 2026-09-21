# candace

One repository, one Go module, one Bazel module: the public half of a private
infrastructure monorepo, published whole.

It is not a framework and not a grab bag. It is a working agent-operated
deployment system and the pieces it is built from, released together so that
the pieces are usable on their own and the system is reproducible as a whole.

| | |
|---|---|
| [**CSF — The Cerebrospinal Fluid**](csf) | Shared Go coordination for agents, typed tools, knowledge and simulation evidence. Start with the [consumer example](examples/csf-consumer). |
| [**CandaceOS**](services/candaceos) | An agent-operated app lab: a harness proposes, Core approves and fences, a node executor reconciles Compose applications, and an operator UI watches. Its deployment kit is [`candaceos/`](candaceos). |
| [**Warden**](services/warden) | A fleet watchdog: Raft-style leader election over a static peer set, liveness, incidents, and an authoritative view every mutation is fenced against. |
| [**gotth-live**](pkg/gotth) | Server-driven live user interfaces from Go. State and rendering stay in your process; one WebSocket per tab carries events up and re-rendered fragments down. No npm, no CDN. |
| [**xetcas**](xetcas) | A self-hosted Xet content-addressable storage server with a Git LFS front door. Re-pushing a 48 MiB model after editing 2% of it costs about 1 MiB. |
| [**pkg/**](pkg) | The primitives the rest is built on: `pgmem` (a process-local PostgreSQL emulator for tests), `liquidproto` (protobuf refinement types), `cron`, `config`, `redact`, `telemetry`, and more. |

First-party source is Apache-2.0. Dependencies, vendor simulator images and externally hosted paper figures retain their own licenses.

## Run one of them

For this gotth-live example: Go 1.26 and a clone; no npm or code generation is needed.
The optional CSF Workbench has a separate browser-asset build documented in its README:

```bash
go run ./examples/gotth/counter
```

```text
counter: http://127.0.0.1:8080
counter: allowed origins [http://127.0.0.1:8080 http://localhost:8080]
```

Open that URL in two browser tabs. The number lives in the Go process and
neither tab holds a copy of it: click in one and the other repaints, reload
either and the count survives, and the client runtime that carried the patch
was compiled into the binary and served by the same handler that serves the
WebSocket. [`examples/gotth/counter/README.md`](examples/gotth/counter/README.md)
follows one click all the way through and names the file each step lives in.

## What is in here

```text
candace/
├── csf/          typed coordination library, contracts, examples and consumer guide
├── pkg/          domain-neutral primitives — nothing in them knows what CandaceOS is
├── services/     composable business logic — candaceos, warden
├── app/          runnable compositions — candaceos-core, candaceos-agent, warden
├── proto/        .proto sources and their committed Go bindings
├── candaceos/    the deployment kit: Compose stack, installer, fleet driver, updater
├── xetcas/       a Rust workspace (xetcasd) plus its generated Go bindings
├── examples/     one worked consumer per extension seam, each with its own suite
├── extensions/   copilot-pair, a GitHub Copilot CLI extension
├── docs/         extending.md — the four compile-time seams
└── bazel/        the legacy WORKSPACE shim
```

The three Go trees are separated by one rule, about who may import whom:

| Imports | Allowed direction |
|---|---|
| Runnable compositions (`app/`) | Services, CSF and shared packages |
| Domain services and CSF | Shared packages |
| Domain-neutral packages (`pkg/`) | No import of services or application compositions |

Nothing in `pkg/` imports `services/` or `app/`, which is what makes the
primitives usable on their own:

| Package | What it is |
|---|---|
| [`gotth`](pkg/gotth) | Server-driven live UI. Large enough to have its own documentation set. |
| [`pgmem`](pkg/pgmem) | A process-local PostgreSQL emulator for fast tests — real PostgreSQL AST, no server. |
| [`cron`](pkg/cron) | Durable in-process scheduling with human-readable declarations and an explicit state store. |
| [`liquidproto`](pkg/liquidproto) | The runtime for Liquid Proto: protobuf with refinement predicates compiled into the generated Go. |
| [`telemetry`](pkg/telemetry) | Trace propagation and structured JSONL over the `candace.telemetry.v1` contracts, with no observability SDK. |
| [`config`](pkg/config) | Configuration-boundary parsing: environment lookup, private-origin validation, `provider/model` strings. |
| [`mailbox`](pkg/mailbox) | Serializes ownership of a mutable value onto one goroutine — commands run in turn, so no field needs a lock. |
| [`boundedbuffer`](pkg/boundedbuffer) | An `io.Writer` that retains at most a fixed number of bytes while still reporting the true write lengths. |
| [`redact`](pkg/redact) | Removes caller-declared sensitive values, and their URL-userinfo spellings, from log-bound text. |
| [`labels`](pkg/labels) | Canonicalizes case-insensitive label lists so services compare and deduplicate them one way. |
| [`core`](pkg/core) | The zerolog logger the Go trees log through, plus the few formatters operator pages share. |
| [`patience`](pkg/patience) | The one typed await for tests: poll a value, judge it with a predicate, get the value that satisfied it back. |
| [`widget`](pkg/widget) | The widget dialect and its toolchain: interpreter, validator, generator, and the typed SDK that mounts generated cards into a gotth-live host. |

`pkg/proto` and `pkg/scripts` hold tooling rather than a package.

## This repository is generated

It is a **one-way snapshot** of a private monorepo's `candace/` folder at one
exact revision, published with no upstream history. Snapshot updates arrive as ready pull requests from `candace-export` against
`main`. Make source changes in the canonical repository; editing the generated
destination directly would conflict with its next snapshot.

After its review PR is merged, the publisher verifies that tree and creates an immutable `export-<sha12>` tag, a matching GitHub
Release, and a provenance marker, `.candace-export.json`, naming the exact
source revision it came from. Cite the tag, not a branch.

## Consume it in 60 seconds

Each Release carries `candace-<sha12>.tar.gz` and its `.sha256`. The tarball is
this tree re-rooted so `MODULE.bazel` is at the archive root, plus a deterministic
`.candace-source.json` recording the source revision and selected tree, built twice and
byte-compared before it is kept.

Download both files from the same Release. In their directory, replace `<sha12>`
with the 12-character revision from its tag and verify the hexadecimal checksum, then compute
the base64 SRI value required by Bazel (Bash, `sha256sum` and OpenSSL):

```bash
set -euo pipefail
archive='candace-<sha12>.tar.gz'
sha256sum --check "$archive.sha256"
printf 'sha256-'
openssl dgst -sha256 -binary "$archive" | openssl base64 -A
printf '\n'
```

Copy the complete `sha256-...` output line into `integrity` in your own
`MODULE.bazel`; the `.sha256` file's hexadecimal value is not an SRI value:

```python
bazel_dep(name = "candace", version = "0.0.0")

archive_override(
    module_name = "candace",
    integrity = "sha256-...",          # base64 SRI output from the command above
    strip_prefix = "candace-<sha12>",
    urls = ["https://github.com/candacelabs/csf/releases/download/export-<sha12>/candace-<sha12>.tar.gz"],
)
```

Then depend on what you use — `@candace//services/candaceos/component`,
`@candace//pkg/gotth/live`, `@candace//services/warden` — and build.

Not a Bazel repository? The module path is the repository path:

```bash
go get github.com/candacelabs/csf@export-<sha12>
```

There is no semantic-version tag, so `@latest` resolves a moving pseudo-version
of the default branch; naming the export tag is what pins a build.

[`docs/extending.md`](docs/extending.md) covers both shapes in full, plus the
`http_archive` fallback and the legacy `WORKSPACE` path.

## Examples

Every extension seam has a worked example with its own test suite. They are the
contract's executable half — the documentation says what is guaranteed, and
these fail if it stops being true.

| Example | Shows |
|---|---|
| [`external-consumer`](examples/external-consumer) | A complete outside repository choosing every seam at once: its own identity and overlay, its own sidebar entry and page, three composed services, a custom agent harness, and the Core binary linked from them — built and tested both supported Bazel ways. This is also the acceptance test every release archive passes. |
| [`custom-brand`](examples/custom-brand) | Core wearing another product's identity — name, agent, wordmark, palette, an overlay asset, an extra sidebar entry and page — with no edit to Core. |
| [`custom-ui-page`](examples/custom-ui-page) | The smallest useful UI extension: stock identity, one sidebar entry, one page of your own. |
| [`gotth/counter`](examples/gotth/counter) | gotth-live at its smallest: a number that lives in Go, four buttons, and every open tab kept in step by the server. |
| [`gotth/chat`](examples/gotth/chat) | One room in Go, several browsers, and every message reaching every session over a server push. |
| [`gotth/dashboard`](examples/gotth/dashboard) | A feed pushing twenty times a second, three live regions patched independently, and two plain-HTMX regions on the same page. |

## Build it

Bazel is the primary build and comes from a pinned container, so the command is
the same on a laptop and on a runner. Docker is the only prerequisite:

```bash
tools/bazel.sh build -- //... -//xetcas/...   # everything but the Rust workspace
tools/bazel.sh test  -- //... -//xetcas/...
tools/bazel.sh build //xetcas/...             # the Rust workspace and its Go bindings
tools/bazel.sh test  //xetcas/...
```

The plain `go` command works on the same tree and needs no Bazel:

```bash
go build ./...
go test ./...
```

The Rust workspace builds with plain Cargo too — that is the path its demo,
container images, and `just` targets take:

```bash
cd xetcas && cargo build --workspace && cargo test --workspace
```

`.bazelversion` (Bazel 9.2.0) and `MODULE.bazel` (rules_go 0.62.0, Gazelle
0.52.2, Go SDK 1.26.5, rules_rust 0.73.0) are the only version authority. BUILD
files are generated by Gazelle (`tools/bazel.sh run //:gazelle`) and CI fails on
drift.

## Run CandaceOS

The deployment kit installs and runs the whole one-box stack from this clone.
The default install is deliberately harmless: a simulated harness, a dry-run
executor, and no Docker socket mounted anywhere.

```bash
./candaceos/install.sh          # then open http://<host>:7780
./candaceos/status.sh
./candaceos/uninstall.sh
```

Core publishes on all host IPv4 interfaces with **no built-in authentication**:
put it behind your own authenticating proxy before exposing it beyond a trusted
network. [`candaceos/README.md`](candaceos/README.md) is the operations manual,
and [`candaceos/AGENTS.md`](candaceos/AGENTS.md) states the trust model as eight
invariants with their enforcement points.

## Where to go next

- [`AGENTS.md`](AGENTS.md) — the repository's own guide: taxonomy, seams,
  invariants, conventions.
- [`docs/extending.md`](docs/extending.md) — the four compile-time seams and how
  to pin a snapshot.
- [`pkg/gotth/README.md`](pkg/gotth/README.md), [`xetcas/README.md`](xetcas/README.md)
  — each subsystem's own front page.
- `app/*/CLAUDE.md` — what may not be changed casually in each binary.

## License

Apache License 2.0. See [`LICENSE`](LICENSE).

AI systems assisted with work in this repository. Their output is not presumed
correct, secure, reviewed, or production-ready.
