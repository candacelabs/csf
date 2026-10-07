-- Copyright 2026 Candace Labs
--
-- CSF's PostgreSQL schema: the one migration CSF ships. It assumes a clean
-- database, is edited in place while CSF's schema evolves, and is applied by
-- candace/pkg/sqlmigrate under CSF's own version table, csf_schema_version.
-- A consumer applies its own migrations afterwards with any tool and its own
-- version table. sqlc generates csfpg's queries from this file. Amounts are
-- integer USD microdollars.

-- Hashes identify retained bytes, never a search index's normalized text.
-- The original locator survives idempotent repeats.
CREATE TABLE csf_documents (
    content_hash TEXT PRIMARY KEY CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    byte_size BIGINT NOT NULL CHECK (byte_size >= 0),
    artifact_ref TEXT NOT NULL
        CHECK (artifact_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/-]*$'
            AND artifact_ref !~ '(^|/)[.][.]?(/|$)'
            AND position('//' IN artifact_ref) = 0
            AND right(artifact_ref, 1) <> '/'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE csf_source_revisions (
    source_id TEXT NOT NULL CHECK (source_id <> ''),
    revision TEXT NOT NULL CHECK (revision <> ''),
    content_hash TEXT NOT NULL REFERENCES csf_documents (content_hash),
    raw_source_content_hash TEXT REFERENCES csf_documents (content_hash),
    source_uri TEXT NOT NULL CHECK (source_uri <> ''),
    title TEXT NOT NULL CHECK (title <> ''),
    media_type TEXT NOT NULL CHECK (media_type <> ''),
    license TEXT NOT NULL CHECK (license <> ''),
    retrieved_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (source_id, revision),
    UNIQUE (source_id, revision, content_hash)
);

CREATE INDEX csf_source_revisions_hash_idx ON csf_source_revisions (content_hash);

-- Nodes are immutable versions of symbols. A parent owns tree organization;
-- support, contradiction, and revision are separate directed edge assertions.
CREATE TABLE csf_nodes (
    node_id TEXT PRIMARY KEY CHECK (length(node_id) BETWEEN 1 AND 128),
    kind TEXT NOT NULL CHECK (kind IN ('concept', 'claim', 'requirement', 'capability', 'evidence')),
    symbol_key TEXT NOT NULL CHECK (symbol_key <> ''),
    title TEXT NOT NULL CHECK (title <> ''),
    statement TEXT NOT NULL CHECK (statement <> ''),
    parent_node_id TEXT REFERENCES csf_nodes (node_id),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('source', 'model', 'operator', 'checker')),
    author_ref TEXT NOT NULL CHECK (author_ref <> ''),
    citation_content_hash TEXT REFERENCES csf_documents (content_hash),
    citation_source_id TEXT,
    citation_revision TEXT,
    citation_chunk_id TEXT,
    citation_start_byte BIGINT,
    citation_end_byte BIGINT,
    text_projection_ref TEXT,
    vector_projection_ref TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (citation_source_id, citation_revision, citation_content_hash)
        REFERENCES csf_source_revisions (source_id, revision, content_hash),
    CHECK (parent_node_id IS NULL OR parent_node_id <> node_id),
    CHECK ((citation_source_id IS NULL) = (citation_revision IS NULL)),
    CHECK (citation_source_id IS NULL OR citation_content_hash IS NOT NULL),
    CHECK ((citation_start_byte IS NULL) = (citation_end_byte IS NULL)),
    CHECK (citation_start_byte IS NULL OR
        (citation_content_hash IS NOT NULL AND citation_start_byte >= 0
            AND citation_end_byte > citation_start_byte)),
    CHECK (citation_chunk_id IS NULL OR
        (citation_chunk_id <> '' AND citation_start_byte IS NOT NULL)),
    CHECK (kind <> 'evidence' OR citation_content_hash IS NOT NULL),
    CHECK (text_projection_ref IS NULL OR text_projection_ref <> ''),
    CHECK (vector_projection_ref IS NULL OR vector_projection_ref <> '')
);

CREATE INDEX csf_nodes_parent_idx ON csf_nodes (parent_node_id);
CREATE INDEX csf_nodes_symbol_idx ON csf_nodes (symbol_key);
CREATE INDEX csf_nodes_citation_idx ON csf_nodes (citation_content_hash);

CREATE TABLE csf_edges (
    from_node_id TEXT NOT NULL REFERENCES csf_nodes (node_id),
    to_node_id TEXT NOT NULL REFERENCES csf_nodes (node_id),
    relation TEXT NOT NULL
        CHECK (relation IN ('supports', 'refutes', 'depends-on', 'derived-from', 'supersedes')),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('source', 'model', 'operator', 'checker')),
    author_ref TEXT NOT NULL CHECK (author_ref <> ''),
    rationale TEXT NOT NULL CHECK (rationale <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (from_node_id, to_node_id, relation),
    CHECK (from_node_id <> to_node_id)
);

CREATE INDEX csf_edges_target_idx ON csf_edges (to_node_id, relation);

-- Durable projection work belongs to the existing Go host.
CREATE TYPE csf_projection_status AS ENUM ('pending', 'running', 'succeeded', 'failed');

CREATE TABLE csf_projection_tasks (
    source_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    status csf_projection_status NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_generation BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    lease_until TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(last_error) <= 4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, revision),
    FOREIGN KEY (source_id, revision)
        REFERENCES csf_source_revisions (source_id, revision),
    CHECK ((status = 'running') = (lease_until IS NOT NULL)),
    CHECK (status <> 'running' OR attempts > 0)
);

CREATE INDEX csf_projection_pending_idx
    ON csf_projection_tasks (next_attempt_at, source_id, revision)
    WHERE status = 'pending';
CREATE INDEX csf_projection_expired_idx
    ON csf_projection_tasks (lease_until, source_id, revision)
    WHERE status = 'running';

-- The job ledger: requests admitted to external executors. A job carries
-- a budget reservation, progress in caller-defined units, a log cursor,
-- cancellation and cleanup evidence, measurements with evidence hashes and
-- trace delivery state. Kind and executor are data; candace/services/jobs
-- owns the rules. Budgets are their own accounts.
CREATE TYPE csf_job_state AS ENUM
    ('pending', 'submitting', 'queued', 'running', 'succeeded', 'failed', 'cancelling', 'cancelled', 'submission_unknown');

CREATE TABLE csf_job_budgets (
    account TEXT PRIMARY KEY CHECK (account ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$'),
    limit_usd_micros BIGINT NOT NULL CHECK (limit_usd_micros >= 0),
    reserved_usd_micros BIGINT NOT NULL DEFAULT 0 CHECK (reserved_usd_micros >= 0 AND reserved_usd_micros <= limit_usd_micros),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE csf_jobs (
    job_id TEXT PRIMARY KEY CHECK (job_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$'),
    kind TEXT NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_.-]{0,79}$'),
    executor TEXT NOT NULL CHECK (executor ~ '^[a-z][a-z0-9_-]{0,39}$'),
    budget_account TEXT NOT NULL REFERENCES csf_job_budgets(account),
    request JSONB NOT NULL,
    request_sha256 TEXT NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    state csf_job_state NOT NULL DEFAULT 'pending',
    total_units BIGINT NOT NULL CHECK (total_units >= 1),
    completed_units BIGINT NOT NULL DEFAULT 0 CHECK (completed_units >= 0 AND completed_units <= total_units),
    managed BOOLEAN NOT NULL DEFAULT FALSE,
    executor_target TEXT NOT NULL DEFAULT '',
    executor_image TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    artifact_uri TEXT NOT NULL DEFAULT '',
    reservation_usd_micros BIGINT NOT NULL CHECK (reservation_usd_micros >= 0),
    timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds >= 1),
    reason TEXT NOT NULL DEFAULT '',
    log_stream TEXT NOT NULL DEFAULT '',
    log_cursor TEXT NOT NULL DEFAULT '',
    inspection_error TEXT NOT NULL DEFAULT '',
    cancellation_requested BOOLEAN NOT NULL DEFAULT FALSE,
    cleanup_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    trace_url TEXT NOT NULL DEFAULT '',
    trace_export_error TEXT NOT NULL DEFAULT '',
    trace_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    log_document_id TEXT NOT NULL DEFAULT '',
    log_projection_error TEXT NOT NULL DEFAULT '',
    log_indexed_at TIMESTAMPTZ,
    log_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX csf_job_external ON csf_jobs(executor, external_id) WHERE external_id <> '';
CREATE INDEX csf_job_queue ON csf_jobs(executor, state, created_at);

CREATE TABLE csf_job_metric_definitions (
    job_id TEXT NOT NULL REFERENCES csf_jobs(job_id),
    name TEXT NOT NULL,
    unit TEXT NOT NULL,
    description TEXT NOT NULL,
    PRIMARY KEY (job_id, name)
);

CREATE TABLE csf_job_measurements (
    job_id TEXT NOT NULL,
    metric TEXT NOT NULL,
    step BIGINT NOT NULL CHECK (step >= 0),
    value DOUBLE PRECISION NOT NULL CHECK (value > '-Infinity'::DOUBLE PRECISION AND value < 'Infinity'::DOUBLE PRECISION),
    recorded_at TIMESTAMPTZ NOT NULL,
    evidence_hash TEXT NOT NULL CHECK (evidence_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (job_id, metric, step),
    FOREIGN KEY (job_id, metric) REFERENCES csf_job_metric_definitions(job_id, name)
);

CREATE TYPE csf_trace_delivery_state AS ENUM
    ('attempted', 'succeeded', 'ambiguous');

CREATE TABLE csf_job_trace_deliveries (
    destination TEXT NOT NULL CHECK (destination <> ''),
    trace_id TEXT NOT NULL CHECK (trace_id ~ '^[0-9a-f]{32}$'),
    job_id TEXT NOT NULL REFERENCES csf_jobs(job_id),
    state csf_trace_delivery_state NOT NULL DEFAULT 'attempted',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CHECK ((state = 'attempted') = (finished_at IS NULL)),
    PRIMARY KEY (destination, trace_id)
);

-- The slice graph: the dispatch service's work queue, persisted so it
-- survives a harness restart. A slice is one unit of work with the recipe
-- its session runs; depends_on edges order slices, contends edges keep them
-- from running at once, and intents attach to the slice whose touch-set
-- covers their terms. candace/services/dispatch owns the rules; the typed
-- parts (recipe, touch-set, provenance, intent) are stored as protobuf JSON.
CREATE TYPE csf_slice_state AS ENUM
    ('queued', 'running', 'preempted', 'merged', 'failed', 'canceled');

CREATE TABLE csf_slices (
    slice_id TEXT PRIMARY KEY CHECK (slice_id ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$'),
    sequence BIGINT NOT NULL UNIQUE CHECK (sequence > 0),
    title TEXT NOT NULL CHECK (title <> ''),
    recipe JSONB NOT NULL,
    touch_set JSONB NOT NULL,
    provenance JSONB NOT NULL,
    state csf_slice_state NOT NULL DEFAULT 'queued',
    assignment_id TEXT NOT NULL DEFAULT '',
    pull_request_url TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    checkpoint TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE csf_slice_edges (
    from_slice_id TEXT NOT NULL REFERENCES csf_slices (slice_id),
    to_slice_id TEXT NOT NULL REFERENCES csf_slices (slice_id),
    relation TEXT NOT NULL CHECK (relation IN ('depends_on', 'contends')),
    PRIMARY KEY (from_slice_id, to_slice_id, relation),
    CHECK (from_slice_id <> to_slice_id)
);

CREATE TABLE csf_intents (
    intent_id TEXT PRIMARY KEY CHECK (intent_id ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$'),
    slice_id TEXT REFERENCES csf_slices (slice_id),
    intent JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX csf_intents_slice_idx ON csf_intents (slice_id);

-- Every control the slice dispatcher was given, in order, with its reason:
-- pause and resume name no slice, hold and release name one. The dispatcher
-- replays them on start, so a hold or a pause survives a restart.
CREATE TABLE csf_dispatch_controls (
    sequence BIGINT PRIMARY KEY CHECK (sequence > 0),
    action TEXT NOT NULL CHECK (action IN ('pause', 'resume', 'hold', 'release')),
    slice_id TEXT REFERENCES csf_slices (slice_id),
    reason TEXT NOT NULL CHECK (reason <> '' AND length(reason) <= 4096),
    recorded_at TIMESTAMPTZ NOT NULL,
    CHECK ((action IN ('hold', 'release')) = (slice_id IS NOT NULL))
);

-- Per-agent external-tool configuration is durable state. Values are opaque
-- secret references; secret material remains outside this database.
CREATE TABLE csf_agent_configurations (
    agent_id TEXT PRIMARY KEY,
    revision BIGINT NOT NULL CHECK (revision > 0),
    langfuse_endpoint_url TEXT NOT NULL DEFAULT '',
    langfuse_public_key_secret_ref TEXT NOT NULL DEFAULT '',
    langfuse_secret_key_secret_ref TEXT NOT NULL DEFAULT '',
    opensearch_endpoint_url TEXT NOT NULL DEFAULT '',
    opensearch_index TEXT NOT NULL DEFAULT '',
    opensearch_embedding_model TEXT NOT NULL DEFAULT '',
    opensearch_credentials_secret_ref TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()
);

-- Cron: the declared triggers and their occurrences. A trigger is a named,
-- human-readable schedule with a catch-up and an overlap policy; each firing
-- is an occurrence, identified by its trigger and scheduled instant and run
-- under a fenced lease. The schedule is relational columns, never a wire
-- blob; candace/services/cron owns the rules. Every time is a value the
-- service passed in from its clock, so no column defaults to now().
CREATE TABLE csf_cron_triggers (
    trigger_name TEXT PRIMARY KEY
        CHECK (length(trigger_name) BETWEEN 1 AND 128 AND trigger_name ~ '^[a-z][a-z0-9._/-]*$'),
    schedule_kind TEXT NOT NULL
        CHECK (schedule_kind IN ('daily', 'weekly', 'monthly', 'last_day_of_month', 'every', 'raw')),
    local_hour SMALLINT CHECK (local_hour IS NULL OR local_hour BETWEEN 0 AND 23),
    local_minute SMALLINT CHECK (local_minute IS NULL OR local_minute BETWEEN 0 AND 59),
    weekday SMALLINT CHECK (weekday IS NULL OR weekday BETWEEN 0 AND 6),
    month_day SMALLINT CHECK (month_day IS NULL OR month_day BETWEEN 1 AND 31),
    interval_nanoseconds BIGINT
        CHECK (interval_nanoseconds IS NULL OR (interval_nanoseconds >= 1000 AND interval_nanoseconds % 1000 = 0)),
    raw_expression TEXT CHECK (raw_expression IS NULL OR length(raw_expression) BETWEEN 1 AND 256),
    timezone TEXT NOT NULL CHECK (length(timezone) BETWEEN 1 AND 128),
    interval_anchor_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ NOT NULL,
    catch_up_policy TEXT NOT NULL CHECK (catch_up_policy IN ('none', 'latest', 'all')),
    overlap_policy TEXT NOT NULL CHECK (overlap_policy IN ('skip', 'allow')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK ((schedule_kind IN ('daily', 'weekly', 'monthly', 'last_day_of_month'))
        = (local_hour IS NOT NULL AND local_minute IS NOT NULL)),
    CHECK ((schedule_kind = 'weekly') = (weekday IS NOT NULL)),
    CHECK ((schedule_kind = 'monthly') = (month_day IS NOT NULL)),
    CHECK ((schedule_kind = 'every') = (interval_nanoseconds IS NOT NULL AND interval_anchor_at IS NOT NULL)),
    CHECK ((schedule_kind = 'raw') = (raw_expression IS NOT NULL))
);

CREATE TABLE csf_cron_occurrences (
    occurrence_id TEXT PRIMARY KEY CHECK (occurrence_id ~ '^occ_[0-9a-f]{64}$'),
    trigger_name TEXT NOT NULL REFERENCES csf_cron_triggers (trigger_name),
    scheduled_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'canceled', 'skipped')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    lease_owner TEXT CHECK (lease_owner IS NULL OR length(lease_owner) BETWEEN 1 AND 128),
    lease_token TEXT CHECK (lease_token IS NULL OR length(lease_token) BETWEEN 1 AND 256),
    lease_until TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    error_summary TEXT CHECK (error_summary IS NULL OR length(error_summary) <= 4096),
    skip_reason TEXT CHECK (skip_reason IS NULL OR length(skip_reason) BETWEEN 1 AND 4096),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (trigger_name, scheduled_at),
    CHECK ((status = 'running')
        = (lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK ((status = 'running') = (finished_at IS NULL)),
    CHECK ((status = 'skipped') = (skip_reason IS NOT NULL)),
    CHECK (error_summary IS NULL OR status IN ('failed', 'canceled')),
    CHECK (status = 'skipped' OR (started_at IS NOT NULL AND attempt >= 1))
);

CREATE INDEX csf_cron_occurrences_expired_idx
    ON csf_cron_occurrences (lease_until, scheduled_at, occurrence_id)
    WHERE status = 'running';
CREATE INDEX csf_cron_occurrences_recent_idx
    ON csf_cron_occurrences (scheduled_at DESC, occurrence_id DESC);

-- Ouroboros: the mining loop's ledger. candace/services/ouroboros owns the
-- rules. Items are the corpus files a miner has read, with the size it read
-- them at, so a grown event log is mined again and an unchanged one is not.
-- Findings are the miner's own typed records, one row per verdict subject
-- per item. Proposals are the pre-check's verdicts on a ticket's mining
-- proposal: a launched one became a fixer; one that needs labels is the queue
-- the labeler reads. Fixers, merges and series are the ledger the budget and
-- the compounding number are computed from. Every instant is a value the
-- service read from its clock, so no column defaults to now().
CREATE TABLE csf_ouroboros_items (
    miner TEXT NOT NULL CHECK (miner ~ '^[a-z_][a-z0-9_-]{0,79}$'),
    item TEXT NOT NULL CHECK (length(item) BETWEEN 1 AND 1024),
    byte_size BIGINT NOT NULL CHECK (byte_size >= 0),
    mined_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (miner, item)
);

CREATE TABLE csf_ouroboros_findings (
    miner TEXT NOT NULL CHECK (miner ~ '^[a-z_][a-z0-9_-]{0,79}$'),
    rule TEXT NOT NULL CHECK (rule ~ '^[a-z][a-z0-9_]{0,63}$'),
    subject TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 256),
    item TEXT NOT NULL CHECK (length(item) BETWEEN 1 AND 1024),
    severity TEXT NOT NULL
        CHECK (severity IN ('SEVERITY_S0', 'SEVERITY_S1', 'SEVERITY_S2', 'SEVERITY_S3')),
    -- Generic holds for any harness user and may be shared; tenant never
    -- leaves the tenant (#120 amendment C).
    scope TEXT NOT NULL DEFAULT 'SCOPE_UNSPECIFIED'
        CHECK (scope IN ('SCOPE_UNSPECIFIED', 'SCOPE_GENERIC', 'SCOPE_TENANT')),
    finding JSONB NOT NULL,
    found_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (miner, rule, subject, item)
);

CREATE INDEX csf_ouroboros_findings_found_idx ON csf_ouroboros_findings (found_at);

CREATE TABLE csf_ouroboros_proposals (
    ticket BIGINT NOT NULL CHECK (ticket > 0),
    proposal TEXT NOT NULL CHECK (length(proposal) BETWEEN 1 AND 128),
    miner TEXT NOT NULL CHECK (length(miner) BETWEEN 1 AND 80),
    corpus TEXT NOT NULL CHECK (length(corpus) <= 256),
    instances JSONB NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('launch', 'needs_labels')),
    reason TEXT NOT NULL CHECK (length(reason) <= 4096),
    checked_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (ticket, proposal)
);

CREATE TABLE csf_ouroboros_fixers (
    assignment_id TEXT PRIMARY KEY
        CHECK (assignment_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    ticket BIGINT NOT NULL CHECK (ticket > 0),
    proposal TEXT NOT NULL CHECK (length(proposal) BETWEEN 1 AND 128),
    miner TEXT NOT NULL CHECK (length(miner) BETWEEN 1 AND 80),
    model TEXT NOT NULL CHECK (length(model) BETWEEN 1 AND 200),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    outcome TEXT NOT NULL
        CHECK (outcome IN ('running', 'ready', 'draft', 'no_pull_request', 'failed', 'canceled')),
    pull_request_url TEXT NOT NULL DEFAULT '' CHECK (length(pull_request_url) <= 2048),
    cost_usd_micros BIGINT NOT NULL DEFAULT 0 CHECK (cost_usd_micros >= 0),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    seconds BIGINT NOT NULL DEFAULT 0 CHECK (seconds >= 0),
    FOREIGN KEY (ticket, proposal) REFERENCES csf_ouroboros_proposals (ticket, proposal),
    CHECK ((outcome = 'running') = (finished_at IS NULL))
);

CREATE INDEX csf_ouroboros_fixers_started_idx ON csf_ouroboros_fixers (started_at);

CREATE TABLE csf_ouroboros_merges (
    pull_request BIGINT NOT NULL CHECK (pull_request > 0),
    head_sha TEXT NOT NULL CHECK (head_sha ~ '^[0-9a-f]{7,40}$'),
    author_sessions TEXT NOT NULL DEFAULT '' CHECK (length(author_sessions) <= 4096),
    merger TEXT NOT NULL CHECK (length(merger) BETWEEN 1 AND 128),
    merged BOOLEAN NOT NULL,
    exit_code INTEGER NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) <= 4096),
    recorded_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (pull_request, head_sha)
);

-- A day is its ISO calendar date in the loop's time zone, kept as text: the
-- series is read back as a label, never compared as an instant.
CREATE TABLE csf_ouroboros_series (
    series TEXT NOT NULL CHECK (series ~ '^[a-z][a-z0-9_.]{0,79}$'),
    day TEXT NOT NULL CHECK (day ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
    value DOUBLE PRECISION,
    low DOUBLE PRECISION,
    high DOUBLE PRECISION,
    numerator BIGINT NOT NULL DEFAULT 0 CHECK (numerator >= 0),
    denominator BIGINT NOT NULL DEFAULT 0 CHECK (denominator >= 0),
    computed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (series, day)
);
