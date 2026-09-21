# Brain-spine records

`schema.sql` owns the original PostgreSQL model; versioned migrations own
additions, and `queries.sql` owns application SQL.
`sqlc.yaml` generates package `brainspinedb` using `pgx/v5`, already present in
the private Go module. Fresh bootstrap applies the base schema and the same
versioned additive migrations used for existing databases; SQLC reads both.
These files do not themselves operate a deployed database or create a service
deployment.

The five execution tables retain:

- Runs and their immutable configuration artifact reference and spending cap.
- Candidates identified by SHA256, name, and program artifact reference.
- Evaluation attempts, their immutable request identity, `train` / `validation`
  / `test` split, provider operation, reservation, terminal outcome, and result
  artifact reference.
- Named metric values and sample counts for successful attempts.
- Activation receipts naming the successful scored validation attempt, epoch,
  acceptance-policy artifact, and decision artifact.

Queryable state is relational. Programs, requests, detailed results, and policy
documents remain immutable artifacts addressed by references; no JSON/protobuf
payload substitutes for columns here. Attempt and receipt IDs are globally
unique within this schema. Candidate hashes are unique within a run.

## Transaction protocol

Use `READ COMMITTED` and acquire `LockRun` **as a separate first statement** in
every transaction that mutates that run's attempts, scores, activations, or
completion. Every caller must use the same lock order: run, then attempt. The
separate statement matters: subsequent queries take a fresh snapshot after any
wait for the run lock. Do not combine the lock and reservation into one new CTE
or reuse a snapshot established before the lock.

1. Create the run and candidate metadata. Exact retries return existing records;
   reuse of an identity with different immutable fields returns no row.
2. Begin a transaction, `LockRun`, then `ReserveAttempt`, then commit. The query
   increases the reservation and inserts the attempt atomically. No external
   work starts before this transaction commits. An identical request returns
   the original attempt, including its current status, without reserving twice.
3. In another locked transaction, `StartAttempt` changes `reserved` to `running`.
   Only the first call returns a row. Commit before dispatch. A repeated call
   returning no row requires inspecting `GetAttempt`, never blind redispatch.
   Pass the attempt ID as the provider idempotency key when supported, and use
   `BindProviderOperation` to retain the provider's immutable operation ID.
4. On an uncertain timeout or crash, use `MarkAttemptAmbiguous` and reconcile
   with the provider. The whole reservation remains held. A crash after marking
   `running` but before receiving an operation ID is also ambiguous; no generic
   exactly-once claim is made for a provider without an idempotency/recovery API.
5. After a known result, begin a transaction, `LockRun`, `FinalizeAttempt`, and
   `RecordScore` for each metric, then commit. Finalization releases the reserved
   upper bound and charges the actual amount atomically with its terminal
   status, outcome, and result reference. Exact repeats return the original
   result. Conflicting terminal fields return no row and change nothing.
6. For promotion, begin a transaction, `LockRun`, check the immutable acceptance
   rule and expected current epoch, then `RecordActivation`, then commit. SQL
   requires a successful scored `validation` attempt for that candidate; it
   rejects a `test` attempt in this role. The caller owns thresholds, comparison
   to the incumbent, expected-epoch checks, and the rule that held-out test data
   never guides selection. A receipt retry must match all recorded fields; an
   identical existing receipt can be replayed after the run closes.

`FinalizeAttempt` permits `succeeded`, `failed`, or `cancelled`. Each needs a
nonempty outcome and result artifact reference, including cancellation receipts.
`succeeded` means the evaluation completed, not that its candidate passed the
acceptance policy. A failed evaluation may still have a nonzero actual charge.
Never turn an uncertain charge into a zero-cost cancellation to release funds.

`RecordScore` accepts only successful attempts and rejects conflicting metric
replays, NaN, infinities, and nonpositive sample counts. Scores belong to their
attempt's fixed split. `FinishRun` rejects all pending attempts, even a zero-cost
one; `ambiguous` must be reconciled before a run can finish.

## Budget invariant and failure handling

All amounts are integer USD microdollars: USD 300 is `300000000`. The run has:

```text
0 <= reserved_usd_micros + charged_usd_micros <= budget_limit_usd_micros <= 300000000
```

A reservation is part of the attempt, so it cannot acquire an independently
retryable identity. A new reservation fits only within the unreserved remainder.
Finalization accepts charges between zero and that attempt's reserved maximum.
The guarded queries maintain agreement between the counters and attempt rows;
the database constraints independently reject a counter total above the cap.
Administrative direct table writes are outside this API contract.

The cap belongs to **one run**. Reuse the same run ID for the entire authorized
spending campaign; creating another run does not provide additional operator
authorization. Every externally paid operation must be charged to this owner.
Reserve a conservative, enforceable upper bound before dispatch. If the provider
can charge an unbounded amount, these records alone cannot enforce a real-world
spending cap. An observed charge above the reservation is rejected and requires
reconciliation; the query does not silently increase authorization.

No returned row means insufficient allowance, conflicting identity, an invalid
state transition, or a failed prerequisite. Inspect the existing row to classify
it; never interpret it as success. Constraint errors require transaction
rollback. In particular, a concurrent attempt-ID collision across different runs
may raise a uniqueness error; rollback also removes its attempted reservation.
Deadlock/serialization retries must restart the whole transaction. The ordinary
same-run path is serialized by `LockRun`.

## Validation and integration handoff

The following command was run with the repository's pinned SQLC image. It only
analyzes SQL and emits no Go files:

```sh
docker run --rm --network none \
  --mount "type=bind,src=$PWD/candace/csf/internal/brainspinedb,dst=/src,readonly" \
  --workdir /src \
  sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537 compile
```

On 2026-09-16 the schema and named queries were exercised against a disposable
PostgreSQL 17.10 container with networking disabled and no published ports:

```text
PASS: exact retries, conflicts, ambiguous retention, overcharge rejection,
      scored validation activation, test exclusion, and pending-attempt guard.
PASS: 8 concurrent USD 60 reservations admitted exactly 5; reserved=USD 300.
PASS: 8 duplicate USD 100 reservations reserved once.
PASS: 8 duplicate finalizations charged USD 25 once.
PASS: identical activation receipt replayed after run completion.
PASS: failed score insertion rolled back finalization and retained reservation.
PASS: concurrent cross-run ID reuse admitted one attempt and reserved once.
```

This was SQL-level execution with query arguments substituted by a small Python
driver in the work session; it was not a Go store or LITHE integration test.
The Go owner must generate the bindings and test its transaction wrapper against
PostgreSQL. Preserve the cases above and additionally exercise connection loss
after commit and provider reconciliation. The attempt must remain reserved if
the result transaction fails.

## Knowledge metadata and symbolic graph

Four additional tables form the metadata authority:

| Table | Identity and role |
|---|---|
| `brainspine_documents` | SHA256 of retained bytes, byte size, and portable artifact path. |
| `brainspine_source_revisions` | `(source_id, revision)` names one immutable content hash and its source metadata. |
| `brainspine_nodes` | Immutable typed and attributed symbol versions, parent organization, and optional citations. |
| `brainspine_edges` | Directed, attributed assertions relating two nodes. |

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
references. OpenSearch owns text/vector indexes, which may be dropped and rebuilt
from retained documents. These references may identify a particular projection
generation; a missing/stale projection does not invalidate a document or its
citation. PostgreSQL owns metadata and relationships, not search-result truth.

All knowledge list/search queries take `result_limit` and `result_offset` and
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

## Schema version and migration strategy

The execution-only schema at commit
`c4ebf1106a974e45b810f3b4ce92ee8417f69518` is schema contract version 1. This
knowledge addition is version 2. The version-2 DDL starts at the explicit
`Schema version 2 starts here` marker in `schema.sql`; it adds four tables and
their indexes without changing version-1 rows or columns.

`schema.sql` remains the fresh-database version-2 bootstrap; it is never
reapplied to an existing database. Version 3 and later additive upgrades live in
immutable numbered `.up.sql` files and use the shared `sqlmigrate.ApplyPrefixed`
runner. Its ledger records migration names and application time, not checksums.
Retained deployment receipts pin source and migration hashes. Existing version-1
databases still require the separately reviewed version-2 upgrade before this
host starts; startup does not infer or silently repair old schema drift.

The version-2 queries passed the pinned SQLC compiler and actual PostgreSQL 17.10
checks on 2026-09-16, without generating or reviewing Go:

```text
PASS: blob deduplication and immutable source revisions.
PASS: first-observation URI/time retained across duplicate retrievals.
PASS: relative artifact paths and metadata pagination.
PASS: parent immutability, citation ranges, revision/hash consistency.
PASS: cited evidence and distinct refutation/supersession records.
PASS: 8 concurrent retrievals returned one canonical source-revision row.
PASS: additive version-1 to version-2 upgrade preserved existing run data.
```

As with the execution tests, these were direct SQL checks against an isolated
disposable container. Go/MCP/OpenSearch integration and archive portability
require the owning service's separate tests.

## Durable projection queue (schema version 3)

`brainspine_projection_tasks` is the durable queue for bounded goroutines in the
existing Go host. It does not introduce a process or an in-memory authority.
The composite key `(source_id, revision)` references the immutable source row;
workers load its retained content through the existing document/artifact owner.
The native PostgreSQL enum `brainspine_projection_status` owns `pending`,
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

### Migration and replay

`schema.sql` remains the unchanged base schema. Both fresh bootstrap and
existing version-2 databases apply `migrations/003_projection_tasks.up.sql`,
which alone owns the queue enum, table and indexes. `sqlc.yaml` lists the base
schema and this migration as its schema inputs. The migration adds those objects
and an initial `INSERT ... SELECT ... ON CONFLICT DO NOTHING` backfill. It does
not change source documents or existing graph/execution records. A separate
`BackfillProjectionTasks` call is safe to repeat and never resets existing tasks.

The root CLI uses the existing `candace/pkg/sqlmigrate` owner and
`ApplyPrefixed` with a service-specific migration prefix. That owner consumes
`*.up.sql` files and tracks applied migration prefixes; it does not compare file
checksums. The acceptance receipt retains the migration SHA256 as evidence,
not as an enforced migration-ledger field. Preserve migration bytes after
application and add a new migration for later changes. Do not blindly rerun
the DDL or add `IF NOT EXISTS` to hide unexpected existing declarations. The
DDL and backfill must be applied transactionally. No deployed database was
touched while preparing this SQL.

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
