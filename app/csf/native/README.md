# Native candace/csf package

This directory builds a fixed-layout Debian 12 amd64 archive whose selected
components run as separately supervised systemd services. The service files
live in `systemd/`; the manifest maps each selected component to the units that
the assembler installs below `/usr/lib/systemd/system`. Docker is used only as
a pinned build environment for source compilation. The installed archive does
not contain or invoke Docker, Compose, or a process supervisor of its own.

The default selection is only `csf`. It starts the in-memory runtime on
`127.0.0.1:14111` without a database, search service, Workbench repository, or
telemetry backend. Every dependency is opt-in.

| Selection | Native process or boundary | Pin |
|---|---|---|
| `csf` | CSF Go application | selected source revision |
| `postgresql` | PostgreSQL server | 17.11 |
| `opensearch` | OpenSearch JVM | 3.8.0 |
| `minio` | Optional loopback S3-compatible fallback | RELEASE.2025-09-07T16-13-09Z |
| `object-storage` | Native Rust AWS S3 preflight and explicit optional local fallback command | selected source revision |
| `langfuse` | Langfuse web and worker | 4.36.1 |
| Langfuse bundled stores | PostgreSQL, ClickHouse and Redis | 17.11, 25.12.11.4 and 7.4.2 |
| Langfuse external stores | Operator-supplied PostgreSQL, ClickHouse and Redis endpoints | recorded in `/etc/candace/csf/langfuse.env` |
| Langfuse object storage | Operator-supplied AWS S3 bucket by default; optional explicit MinIO fallback after eligible AWS failures | external unless opted in |

Langfuse is never treated as one binary. A bundled selection resolves to its
web process, worker process, PostgreSQL, ClickHouse, Redis, and the
component-owned initialization units. An external selection packages the
web, worker and object-storage helper and requires every endpoint in `langfuse.env`. Both profiles
require an existing AWS S3 bucket by default. MinIO is not part of either
Langfuse dependency closure and is installed only when `minio` is explicitly
selected.

## Build payloads and assemble

Build each selected payload into a new directory. Each build records the exact
source URL or Git revision, source digest, target, modes, and every output file
hash in `PAYLOAD_RECEIPT.json`. Run these commands from the public Candace
repository root (`candace/` in the infrastructure monorepo).

```bash
mkdir -p /tmp/candace-csf-payloads
app/csf/native/build-payload.sh csf /tmp/candace-csf-payloads/csf
app/csf/native/build-payload.sh postgresql /tmp/candace-csf-payloads/postgresql
app/csf/native/build-payload.sh redis /tmp/candace-csf-payloads/redis
```

Both Langfuse profiles also require the native `object-storage` payload. Build
it from a clean Git checkout with
`app/csf/native/build-payload.sh object-storage /tmp/candace-csf-payloads/object-storage`.

Build the remaining selected payloads with the same command. OpenSearch and
ClickHouse consume their upstream Linux amd64 archives. Langfuse follows the
v4.36.1 web and worker builds on Node 24 and includes Prisma plus the pinned
ClickHouse migration CLI.

Assemble the minimal runtime:

```bash
app/csf/native/assemble.py \
  --payload-root /tmp/candace-csf-payloads \
  --components csf \
  --output /tmp/candace-csf-native.tar.gz
```

Assemble CSF, OpenSearch, and the complete local Langfuse profile:

```bash
app/csf/native/assemble.py \
  --payload-root /tmp/candace-csf-payloads \
  --components csf,opensearch,langfuse \
  --langfuse-dependencies bundled \
  --output /tmp/candace-csf-native-with-langfuse.tar.gz
```

Use `--langfuse-dependencies external` when PostgreSQL, ClickHouse, and Redis
already exist. AWS S3 remains the default object store in both profiles. Add
`minio` to `--components` only when packaging the optional local fallback. The
assembler refuses an unknown component, a wrong component version
or target, an undeclared file, a hash or mode mismatch, a missing executable,
or an escaping, dangling, or cyclic symlink below a payload root. Safe internal
relative symlinks are hash-receipted and preserved. It streams large payloads
rather than holding them in memory. The archive and its embedded
`NATIVE_RECEIPT.json` are deterministic for identical inputs.

## Payload receipt writer

`receipt-rs` owns the receipt format and rejects empty payloads and unsafe
symlinks. `build-payload.sh` runs its locked Rust implementation in the pinned
builder container. For an existing payload, `write-receipt.sh` exposes the same
implementation and requires Cargo with Rust 1.85 or newer. The Python assembler
consumes these receipts; it does not implement their production writer.

## Host preparation and systemd

The archive installs at fixed paths below `/opt/candace/csf` and
`/usr/lib/systemd/system`; it is not relocatable. Review the embedded receipt
before extracting it as root. Source work in this repository does not extract
the archive, create users, install packages, enable units, or start services.

Debian 12 runtime libraries required by the source-built PostgreSQL and Redis
payloads remain host packages: `libreadline8`, `libssl3`, `zlib1g`, and
`libgcc-s1`. The OpenSearch payload includes its upstream JDK. CSF and ClickHouse
are self-contained Linux amd64 executables. The Langfuse payload
includes Node 24 and the native Node modules produced on the Debian 12 baseline.
Node also requires the Debian `libstdc++6` runtime.

After extraction, create the static identities from the shipped sysusers
declaration. Copy selected `.env.example` files into `/etc/candace/csf` without
the suffix as `root:root` mode `0600`; systemd PID 1 reads them. Install
`database.json` as `root:candace-csf` mode `0640`, the two ClickHouse XML files
as `root:candace-csf-clickhouse` mode `0640`, and `opensearch.yml` below
`/etc/candace/csf/opensearch/` as `root:candace-csf-opensearch` mode `0640`.
The shipped `config/permissions.md` records this boundary. Set every placeholder
before starting its unit.

The object-storage helper accepts single-line environment assignments. Spaces
and hashes inside unquoted values are literal. Quote values containing
backslashes; unquoted backslash escapes and multiline values are rejected.
Rewrites use double quotes and escape embedded quotes and backslashes.

The Langfuse environment names existing AWS S3 buckets and regions. For each
storage, configure its Langfuse-specific access-key pair, or set the standard
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` pair in this service environment
(with `AWS_SESSION_TOKEN` when using temporary credentials). If no static pair
is configured, the preflight uses the EC2 instance role. It does not read the
invoking user's shell environment or `~/.aws` files. AWS profile, web-identity,
container-credential, and global endpoint/region overrides are unsupported in
the service environment and are rejected instead of being silently ignored.
The preflight performs no AWS provisioning. It checks the configured buckets
without changing AWS or the host:

```bash
/opt/candace/csf/components/object-storage/bin/object-storage preflight \
  --langfuse-env /etc/candace/csf/langfuse.env \
  --non-interactive
```

The preflight reads the selected event and media bucket, region, and supported
service credentials from that environment file. It checks both S3 uses with
their configured identities through the AWS SDK. The SDK configuration is
isolated from the invoking user's AWS variables and profile files; absent a
service-file static pair, only EC2 instance metadata credentials are used.
Custom S3 endpoints must be set with the Langfuse-specific endpoint variables,
not global AWS endpoint variables. The preflight checks S3 access directly
with `HeadBucket`; it makes no separate STS identity request. `HeadBucket`
proves bucket existence and access, not `PutObject` permission.

Missing credentials, access denial, or a missing bucket produces an actionable
`use-minio --yes` command. Non-interactive use never selects the fallback.
Interactive use asks before doing so. Wrong-region responses, throttling,
timeouts, and server failures report their actual class without offering local
infrastructure. The fallback requires an archive assembled with the explicit
`minio` component and configured `/etc/candace/csf/minio.env` credentials. Its
explicit command starts and enables only the loopback MinIO unit, lets that unit
create the local bucket, atomically changes the Langfuse object-store settings,
and try-restarts only active Langfuse units. It never creates an AWS account,
bucket, policy, or credential.

The preflight exit codes are stable for automation: `0` means AWS is ready,
`10` means an authorization or missing-bucket result permits explicit local
fallback, and `20` means a wrong-region, throttled, transient, or unclassified
AWS failure for which fallback is not offered. Local configuration and host
command failures return `2`.

OpenSearch also requires the host's `vm.max_map_count` to already meet its
documented minimum of 262144; this package never changes the kernel setting.

The units bind their application ports to loopback. They do not add firewall
rules or public routing. Start and enable only the selected services. Each
store owns its own initialization:

| Owner | Initialization |
|---|---|
| PostgreSQL | `candace-csf-postgresql-init.service`, then idempotent role/database provisioning |
| Langfuse | `candace-csf-langfuse-migrate.service` invokes the v4.36.1 PostgreSQL and ClickHouse migration entrypoint |
| Optional MinIO | `candace-csf-minio.service` creates only its configured local bucket after explicit opt-in |
| CSF durable store | `candace-csf-initialize.service`, only when `database.json` exists |

The primary service units remain independently restartable. A failed optional
dependency does not prevent the minimal in-memory CSF unit from starting.

## Workbench boundary

`bin/csf` is the shared Go application. A usable Copilot Workbench additionally
needs an operator-selected repository, Git and Git LFS, a built Workbench UI,
worktree and receipt directories, provider credentials, and the corresponding
`--workbench-*` flags. Those inputs are deliberately absent from the minimal
native payload. Installing this archive alone does not claim a configured
Workbench session runtime.

## Acceptance boundary

The fixture suite assembles every profile twice, compares bytes, verifies exact
receipt coverage, and exercises rejection paths without installing anything on
the host. Small-payload acceptance requires building CSF, PostgreSQL 17, and
Redis 7 on the Debian 12 baseline and feeding their receipts through the same
assembler; report those build results separately from fixture results. The
latest lightweight CSF and Redis run is recorded in [ACCEPTANCE.md](ACCEPTANCE.md).

OpenSearch, ClickHouse, and Langfuse are substantially larger. Their source and
artifact recipes are pinned here, but a full build and first-boot acceptance of
those heavy payloads must be recorded separately on a builder with sufficient
disk. A fixture result is not that runtime evidence.
