-- Prototype-owned PostgreSQL schema. Amounts are integer USD microdollars.
-- All paid attempts for one authorized campaign share one immutable run budget.
CREATE TABLE brainspine_runs (
    run_id TEXT PRIMARY KEY CHECK (length(run_id) BETWEEN 1 AND 128),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    configuration_artifact_ref TEXT NOT NULL CHECK (configuration_artifact_ref <> ''),
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed')),
    budget_limit_usd_micros BIGINT NOT NULL CHECK (budget_limit_usd_micros BETWEEN 0 AND 300000000),
    reserved_usd_micros BIGINT NOT NULL DEFAULT 0 CHECK (reserved_usd_micros BETWEEN 0 AND 300000000),
    charged_usd_micros BIGINT NOT NULL DEFAULT 0 CHECK (charged_usd_micros BETWEEN 0 AND 300000000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CHECK (reserved_usd_micros + charged_usd_micros <= budget_limit_usd_micros),
    CHECK ((status = 'running') = (finished_at IS NULL))
);

CREATE TABLE brainspine_candidates (
    run_id TEXT NOT NULL REFERENCES brainspine_runs (run_id),
    candidate_hash TEXT NOT NULL CHECK (candidate_hash ~ '^[0-9a-f]{64}$'),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    program_artifact_ref TEXT NOT NULL CHECK (program_artifact_ref <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, candidate_hash)
);

-- A reservation is an attempt's immutable upper bound, not a second record
-- with an independently retryable identity. Ambiguous attempts retain it.
CREATE TABLE brainspine_attempts (
    attempt_id TEXT PRIMARY KEY CHECK (length(attempt_id) BETWEEN 1 AND 128),
    run_id TEXT NOT NULL,
    candidate_hash TEXT NOT NULL,
    split TEXT NOT NULL CHECK (split IN ('train', 'validation', 'test')),
    evaluator TEXT NOT NULL CHECK (evaluator <> ''),
    request_artifact_ref TEXT NOT NULL CHECK (request_artifact_ref <> ''),
    reservation_usd_micros BIGINT NOT NULL CHECK (reservation_usd_micros BETWEEN 0 AND 300000000),
    charged_usd_micros BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved', 'running', 'ambiguous', 'succeeded', 'failed', 'cancelled')),
    provider_operation_id TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    result_artifact_ref TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    FOREIGN KEY (run_id, candidate_hash)
        REFERENCES brainspine_candidates (run_id, candidate_hash),
    UNIQUE (run_id, candidate_hash, attempt_id),
    CHECK (charged_usd_micros BETWEEN 0 AND reservation_usd_micros),
    CHECK ((status IN ('succeeded', 'failed', 'cancelled')) = (finished_at IS NOT NULL)),
    CHECK (status IN ('succeeded', 'failed', 'cancelled') OR charged_usd_micros = 0),
    CHECK (status NOT IN ('succeeded', 'failed', 'cancelled') OR
        (outcome <> '' AND result_artifact_ref <> ''))
);

CREATE INDEX brainspine_attempts_run_status_idx ON brainspine_attempts (run_id, status);

-- A successful evaluation may produce several named metrics. The attempt owns
-- the train/validation/test split; values cannot be moved between splits.
CREATE TABLE brainspine_scores (
    attempt_id TEXT NOT NULL REFERENCES brainspine_attempts (attempt_id),
    metric_name TEXT NOT NULL CHECK (metric_name <> ''),
    metric_value DOUBLE PRECISION NOT NULL
        CHECK (metric_value > '-Infinity'::DOUBLE PRECISION AND
               metric_value < 'Infinity'::DOUBLE PRECISION),
    sample_count BIGINT NOT NULL CHECK (sample_count > 0),
    PRIMARY KEY (attempt_id, metric_name)
);

CREATE TABLE brainspine_activations (
    receipt_id TEXT PRIMARY KEY CHECK (length(receipt_id) BETWEEN 1 AND 128),
    run_id TEXT NOT NULL,
    candidate_hash TEXT NOT NULL,
    validation_attempt_id TEXT NOT NULL,
    epoch BIGINT NOT NULL CHECK (epoch > 0),
    policy_artifact_ref TEXT NOT NULL CHECK (policy_artifact_ref <> ''),
    decision_artifact_ref TEXT NOT NULL CHECK (decision_artifact_ref <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (run_id, candidate_hash, validation_attempt_id)
        REFERENCES brainspine_attempts (run_id, candidate_hash, attempt_id),
    UNIQUE (run_id, epoch)
);

-- Schema version 2 starts here. Hashes identify retained bytes, never a search
-- index's normalized text. The original locator survives idempotent repeats.
CREATE TABLE brainspine_documents (
    content_hash TEXT PRIMARY KEY CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    byte_size BIGINT NOT NULL CHECK (byte_size >= 0),
    artifact_ref TEXT NOT NULL
        CHECK (artifact_ref ~ '^[A-Za-z0-9][A-Za-z0-9._/-]*$'
            AND artifact_ref !~ '(^|/)[.][.]?(/|$)'
            AND position('//' IN artifact_ref) = 0
            AND right(artifact_ref, 1) <> '/'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE brainspine_source_revisions (
    source_id TEXT NOT NULL CHECK (source_id <> ''),
    revision TEXT NOT NULL CHECK (revision <> ''),
    content_hash TEXT NOT NULL REFERENCES brainspine_documents (content_hash),
    raw_source_content_hash TEXT REFERENCES brainspine_documents (content_hash),
    source_uri TEXT NOT NULL CHECK (source_uri <> ''),
    title TEXT NOT NULL CHECK (title <> ''),
    media_type TEXT NOT NULL CHECK (media_type <> ''),
    license TEXT NOT NULL CHECK (license <> ''),
    retrieved_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (source_id, revision),
    UNIQUE (source_id, revision, content_hash)
);

CREATE INDEX brainspine_source_revisions_hash_idx ON brainspine_source_revisions (content_hash);

-- Nodes are immutable versions of symbols. A parent owns tree organization;
-- support, contradiction, and revision are separate directed edge assertions.
CREATE TABLE brainspine_nodes (
    node_id TEXT PRIMARY KEY CHECK (length(node_id) BETWEEN 1 AND 128),
    kind TEXT NOT NULL CHECK (kind IN ('concept', 'claim', 'requirement', 'capability', 'evidence')),
    symbol_key TEXT NOT NULL CHECK (symbol_key <> ''),
    title TEXT NOT NULL CHECK (title <> ''),
    statement TEXT NOT NULL CHECK (statement <> ''),
    parent_node_id TEXT REFERENCES brainspine_nodes (node_id),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('source', 'model', 'operator', 'checker')),
    author_ref TEXT NOT NULL CHECK (author_ref <> ''),
    citation_content_hash TEXT REFERENCES brainspine_documents (content_hash),
    citation_source_id TEXT,
    citation_revision TEXT,
    citation_chunk_id TEXT,
    citation_start_byte BIGINT,
    citation_end_byte BIGINT,
    text_projection_ref TEXT,
    vector_projection_ref TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (citation_source_id, citation_revision, citation_content_hash)
        REFERENCES brainspine_source_revisions (source_id, revision, content_hash),
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

CREATE INDEX brainspine_nodes_parent_idx ON brainspine_nodes (parent_node_id);
CREATE INDEX brainspine_nodes_symbol_idx ON brainspine_nodes (symbol_key);
CREATE INDEX brainspine_nodes_citation_idx ON brainspine_nodes (citation_content_hash);

CREATE TABLE brainspine_edges (
    from_node_id TEXT NOT NULL REFERENCES brainspine_nodes (node_id),
    to_node_id TEXT NOT NULL REFERENCES brainspine_nodes (node_id),
    relation TEXT NOT NULL
        CHECK (relation IN ('supports', 'refutes', 'depends-on', 'derived-from', 'supersedes')),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('source', 'model', 'operator', 'checker')),
    author_ref TEXT NOT NULL CHECK (author_ref <> ''),
    rationale TEXT NOT NULL CHECK (rationale <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (from_node_id, to_node_id, relation),
    CHECK (from_node_id <> to_node_id)
);

CREATE INDEX brainspine_edges_target_idx ON brainspine_edges (to_node_id, relation);
