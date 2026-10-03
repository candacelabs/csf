# csfpg queries: CSF records

`candace/ipc/db/csfpg` holds CSF's SQL queries (`queries.sql`, `jobs.sql`,
`cron.sql`, `agent_configurations.sql`) and the Go that sqlc generates from
them. `jobs.sql` is the job ledger that `candace/services/jobs` runs and
`cron.sql` the trigger and occurrence store that `candace/services/cron` runs. The
schema they run against is not here: it is CSF's one migration,
[`schema/001_init.sql`](schema/001_init.sql),
which `sqlc.yaml` names as its schema input and `csfpg.ApplySchema` applies
under CSF's own version table, `csf_schema_version`. No Go declares DDL.
[Services](../../../csf/docs/generated/ontology_cgen.md#term-service) receive the pool as the `csfpg.IDB` capability and hand it to
`csfpg.New`; they never open or close one.

## Job ledger

`csf_jobs` holds requests admitted to external executors; `jobs.sql` is the
ledger that [`candace/services/jobs`](../../../services/jobs) runs. Budgets are
their own accounts: a `csf_job_budgets` row has an immutable limit in integer
USD microdollars (USD 300 is `300000000`), `EnsureJobBudget` opens it,
`LockJobBudget` is the first statement of every admission transaction and
`ReserveJobBudget` moves only what fits, so
`0 <= reserved_usd_micros <= limit_usd_micros` holds by constraint. Every read
that returns jobs is restricted to the kinds its caller decodes. The
`csf_runs` campaign table, the simulation tables and the controller-search
tables (candidates, attempts, scores and activations) were removed on
2026-10-01.

The pinned SQLC image checks the queries against the schema without emitting
Go:

```sh
docker run --rm --network none \
  --mount "type=bind,src=$PWD/candace,dst=/src,readonly" \
  --workdir /src/ipc/db/csfpg \
  sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537 compile
```

## Cron triggers and occurrences

`csf_cron_triggers` holds each declared trigger with its schedule as relational
columns and its cursor, `next_run_at`; `csf_cron_occurrences` holds one row
per scheduled instant with its status, attempt and lease. `cron.sql` is the
store that [`candace/services/cron`](../../../services/cron) runs: every
fenced write (`AcquireExpiredCronOccurrence`, `RenewCronOccurrenceLease`,
`FinishCronOccurrence`, `SkipExpiredCronOccurrence`) is one conditional
statement on the lease token or expiry, and `AdvanceCronTrigger` only moves a
cursor forward, so the store takes no row lock or advisory lock and the same
SQL runs on pgmem. No column defaults to `now()`: every instant is a
parameter the service read from its clock.

## Knowledge metadata and symbolic graph

Four additional tables form the metadata authority:

| Table | Identity and role |
|---|---|
| `csf_documents` | SHA256 of retained bytes, byte size, and portable artifact path. |
| `csf_source_revisions` | `(source_id, revision)` names one immutable content hash and its source metadata. |
| `csf_nodes` | Immutable typed and attributed symbol versions, parent organization, and optional citations. |
| `csf_edges` | Directed, attributed assertions relating two nodes. |

`CreateDocument` compares the hash and byte size, returning the existing row on
repeated content while retaining the first artifact locator. `CreateSourceRevision`
requires the same hash, raw-source hash, title, media type, and reported license
for an existing `(source_id, revision)`. Repeated retrieval timestamps and URIs
are allowed; the first recorded URI and timestamp remain unchanged. A changed
hash or other immutable metadata returns no row. Different sources or revisions
may point to the same content hash. The optional `raw_source_content_hash`
separately retains an upstream response when the canonical document is a derived
text object. Every referenced hash must have a document record first.

Hash the exact retained bytes. Text normalization for search must not silently
change the meaning of a content hash. The Go ingestor owns byte hashing, artifact
creation, source-ID normalization, upstream revision selection, and validation
that reported metadata corresponds to the retrieval. A hash proves a byte
identity under its cryptographic assumptions; it does not prove source identity,
authorship, reported license, or a claim's truth. These are provenance records,
not authenticated-source or epistemic proofs.

Nodes have kinds `concept`, `claim`, `requirement`, `capability`, and `evidence`.
`symbol_key` groups versions of the same conceptual item; `node_id` identifies
one immutable version. Parent nodes must already exist, self-parenting is
rejected, and there is no update/reparent/delete query. Consequently, trees built
through this API cannot acquire cycles by reparenting. Administrative direct
table writes remain outside that guarantee. Cross edges need not form a tree.

Each node and edge records `author_kind` (`source`, `model`, `operator`, or
`checker`) and an `author_ref`. These attributes never imply truth. Public
model-facing wrappers must set the author to `model` themselves; only trusted
internal sinks may record checker/operator provenance. There is no `proven`
column or operation that promotes a model assertion into a proven fact.

Edge direction reads `from_node relation to_node`. The relations are `supports`,
`refutes`, `depends-on`, `derived-from`, and `supersedes`. Supersession requires
matching node kind and `symbol_key`. It records a proposed replacement and does
not erase either version. Refutation is an independent contradiction assertion;
it does not implicitly supersede anything. Different relation types can coexist
between the same nodes. Conflicting edge attribution/rationale is rejected under
the same edge key. Resolution policy belongs to the trusted caller; neither
timestamps nor model-authored edges select an authoritative requirement version.

Citations name a document hash and optionally a source revision. Composite
foreign keys reject a citation that pairs a revision with another content hash.
An `evidence` node must cite a retained document. Optional chunk citations carry
an opaque chunk ID plus a half-open `[start_byte, end_byte)` range in those exact
bytes. `CreateNode` checks the range is within the recorded document size.
The ingestor additionally owns UTF-8 boundary checks. There is no fifth chunk
table: search chunks are derivatives, while cited ranges live on the nodes that
use them and remain resolvable without the search index.

`text_projection_ref` and `vector_projection_ref` are optional derivative
references. [OpenSearch](../../../csf/docs/generated/ontology_cgen.md#term-opensearch) owns text/vector indexes, which may be dropped and rebuilt
from retained documents. These references may identify a particular projection
generation; a missing/stale projection does not invalidate a document or its
citation. PostgreSQL owns metadata and relationships, not search-result truth.

All [knowledge](../../../csf/docs/generated/ontology_cgen.md#term-knowledge) list/search queries take `result_limit` and `result_offset` and
have deterministic ordering. The Go wrapper must validate a limit in `1..500`
and a nonnegative offset. Pagination across separate requests is a live view;
concurrent inserts can change offsets. Use one database snapshot for a consistent
archive. `SearchSourceRevisions` searches source ID, URI, and title metadata; its
query uses SQL `ILIKE` patterns. It is not the full-text/vector retrieval engine.

Artifact paths are relative to the archive's artifact root. SQL rejects absolute
paths, `.`/`..` path components, repeated separators, backslashes, and trailing
separators. The packager must verify file hashes and sizes and reject symlinks
that escape that root. Package the referenced document bytes and metadata
snapshot, not machine-specific directories. No operator identifiers are required
by this schema or its example values.

## Schema

CSF ships one migration, `001_init.sql`, and edits it in place while the
schema evolves; it assumes a clean database. The earlier chain (a base
`schema.sql` plus migrations 003-009, the last renaming `brainspine_*` to
`csf_*`) was collapsed into it on 2026-10-01. On PostgreSQL 17 the collapsed
file's schema-only dump equalled the old chain's, except that two foreign keys
with long column lists got different generated (truncated) names; the four
controller-search tables were then removed with the in-Go controller. A database
installed from the old chain is not upgraded by this file; moving one needs a
deliberate, separately reviewed step.

An [application](../../../csf/docs/generated/ontology_cgen.md#term-application) that embeds CSF applies its own migrations after
`csfpg.ApplySchema`, with any tool and its own version table; see the
[csfpg README](README.md).

## Durable projection queue

`csf_projection_tasks` is the durable queue for bounded [goroutines](../../../csf/docs/generated/ontology_cgen.md#term-goroutine) in the
existing Go host. It does not introduce a process or an in-memory authority.
The composite key `(source_id, revision)` references the immutable source row;
workers load its retained content through the existing document/artifact owner.
The native PostgreSQL enum `csf_projection_status` owns `pending`,
`running`, `succeeded`, and `failed`, including SQLC's generated status type.

`PutDocument` must call `EnqueueProjectionTask` in the **same transaction** as
`CreateDocument` and `CreateSourceRevision`, after immutable metadata has been
accepted. Roll back the whole transaction on an error. Duplicate enqueue returns
the existing task without resetting its state, retry schedule, generation, or
attempt count. In particular, it cannot revive a terminal failure or steal a
running lease. A later operator-controlled rebuild needs an explicit policy;
ordinary ingestion is not that policy.

The query contract is:

| Query | Parameters | Result |
| --- | --- | --- |
| `EnqueueProjectionTask` | `source_id`, `revision` (text) | One task, including an existing duplicate. |
| `BackfillProjectionTasks` | None | Number of newly queued source revisions. |
| `ClaimProjectionTask` | `lease_seconds`, `max_attempts` (int32) | One running task, or no lease granted. |
| `CompleteProjectionTask` | `source_id`, `revision`, `lease_generation` (int64) | Updated task, or no row for a stale/expired lease. |
| `FailProjectionTask` | Same identity/generation; `max_attempts`, `retry_base_seconds`, `retry_max_seconds` (int32); `last_error` (text) | Pending retry or terminal failed task, or no row. |
| `GetProjectionTask` | `source_id`, `revision` (text) | Current task. |
| `CountProjectionTasks` | None | Native enum `status` and int64 `count`, including zero counts. |

A task row contains `source_id`, `revision`, `status`, int32 `attempts`, int64
`lease_generation`, nullable timestamp `lease_until`, timestamps
`next_attempt_at`, `created_at`, `updated_at`, and text `last_error`.

The claim uses `FOR UPDATE SKIP LOCKED` and updates at most one due pending row
or expired running row. It increments both attempts and lease generation before
returning a running task. Commit that claim before calling the projection
backend. Use a provider-call deadline shorter than the lease and bounded Go
worker concurrency; never hold the database transaction during external work.
A restart leaves the lease in PostgreSQL, and expiry makes it reclaimable.

An expired task already at the attempt limit is instead marked failed, with no
new lease returned. Therefore `ErrNoRows` means **no lease granted**, not proof
that the queue is empty. Keep a periodic worker wakeup even after no-row claims.
Both completion and failure require the current generation, running state, and
an unexpired lease. Rejected acknowledgements do not change the newer owner's
row. A backend write may succeed before an acknowledgement is lost, so projection
writes must remain idempotent; lease fencing does not promise exactly-once
external effects.

Retry delay is `min(retry_max_seconds, retry_base_seconds * 2^(attempts-1))`;
`next_attempt_at` owns the wait, so a delayed task consumes no worker slot.
Failure at `max_attempts` becomes terminal. The SQL bounds leases to 1–3600
seconds, attempt limits to 1–1000, and both retry delays to 1–86400 seconds with
base no greater than maximum. Validate these settings before starting workers:
invalid query arguments grant no lease or acknowledgement. Failure details are
truncated to 4096 characters. Completion clears the previous error.

### Schema

The queue enum, table and indexes are part of `001_init.sql`. A separate
`BackfillProjectionTasks` call is safe to repeat and never resets existing
tasks.

### SQL-level verification, 2026-09-16

The pinned SQLC 1.31.1 `compile` command above passed without generating Go.
Disposable PostgreSQL 17.11, with `--network none` and no published ports, passed:

- Fresh base-plus-migration and migrated schemas produce identical schema-only dumps.
- Existing sources are backfilled; repeated backfill is harmless; raw migration
  replay fails on existing declarations instead of hiding drift.
- Missing-source foreign keys reject enqueue; eight concurrent duplicates create
  one task; duplicates preserve running, succeeded, and failed states.
- Eight concurrent claims lease eight distinct rows; a locked candidate is
  skipped without waiting; counts include every enum value.
- Expired and old-generation completion/failure are rejected; reclaim increments
  generation and attempts; the current generation completes successfully.
- Retries wait 10, 20, then capped 25 seconds in the exercised policy; early
  claims are rejected; attempt four is terminal; expired final leases cannot
  remain stranded running.
- Source registration and enqueue roll back together. Task and lease state
  survive a disposable PostgreSQL restart and remain reclaimable afterward.

These are SQL-level tests, not a claim about the root's Go transaction wrapper,
worker cancellation, or production deployment. The root owns SQLC generation
and the integrated worker/store acceptance.
