-- name: CreateRun :one
INSERT INTO brainspine_runs (run_id, name, configuration_artifact_ref, budget_limit_usd_micros)
VALUES (sqlc.arg(run_id), sqlc.arg(name), sqlc.arg(configuration_artifact_ref), sqlc.arg(budget_limit_usd_micros))
ON CONFLICT (run_id) DO UPDATE SET run_id = EXCLUDED.run_id
WHERE brainspine_runs.name = EXCLUDED.name
  AND brainspine_runs.configuration_artifact_ref = EXCLUDED.configuration_artifact_ref
  AND brainspine_runs.budget_limit_usd_micros = EXCLUDED.budget_limit_usd_micros
RETURNING *;

-- name: CreateDocument :one
INSERT INTO brainspine_documents (content_hash, byte_size, artifact_ref)
VALUES (sqlc.arg(content_hash), sqlc.arg(byte_size), sqlc.arg(artifact_ref))
ON CONFLICT (content_hash) DO UPDATE SET content_hash = EXCLUDED.content_hash
WHERE brainspine_documents.byte_size = EXCLUDED.byte_size
RETURNING *;

-- name: GetDocument :one
SELECT * FROM brainspine_documents WHERE content_hash = sqlc.arg(content_hash);

-- name: ListDocuments :many
SELECT * FROM brainspine_documents ORDER BY content_hash
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- Same immutable revision/content/metadata retains the first URI and retrieval
-- time, even when a retry arrives through another locator at a later time.
-- name: CreateSourceRevision :one
INSERT INTO brainspine_source_revisions
    (source_id, revision, content_hash, raw_source_content_hash, source_uri, title, media_type, license, retrieved_at)
VALUES (sqlc.arg(source_id), sqlc.arg(revision), sqlc.arg(content_hash), sqlc.narg(raw_source_content_hash),
        sqlc.arg(source_uri), sqlc.arg(title), sqlc.arg(media_type), sqlc.arg(license), sqlc.arg(retrieved_at))
ON CONFLICT (source_id, revision) DO UPDATE SET source_id = EXCLUDED.source_id
WHERE brainspine_source_revisions.content_hash = EXCLUDED.content_hash
  AND brainspine_source_revisions.raw_source_content_hash IS NOT DISTINCT FROM EXCLUDED.raw_source_content_hash
  AND brainspine_source_revisions.title = EXCLUDED.title
  AND brainspine_source_revisions.media_type = EXCLUDED.media_type
  AND brainspine_source_revisions.license = EXCLUDED.license
RETURNING *;

-- name: GetSourceRevision :one
SELECT * FROM brainspine_source_revisions
WHERE source_id = sqlc.arg(source_id) AND revision = sqlc.arg(revision);

-- Call in the source-registration transaction. Duplicate enqueue preserves the
-- lease, retry schedule and terminal state; it never starts a second task.
-- name: EnqueueProjectionTask :one
INSERT INTO brainspine_projection_tasks (source_id, revision)
VALUES (sqlc.arg(source_id), sqlc.arg(revision))
ON CONFLICT (source_id, revision) DO UPDATE SET source_id = EXCLUDED.source_id
RETURNING *;

-- name: BackfillProjectionTasks :execrows
INSERT INTO brainspine_projection_tasks (source_id, revision)
SELECT source_id, revision FROM brainspine_source_revisions
ON CONFLICT (source_id, revision) DO NOTHING;

-- Claim at most one due row. Expired final attempts become failed without
-- granting another lease; no returned row is not proof that the queue is empty.
-- Commit before calling the external projection backend.
-- name: ClaimProjectionTask :one
WITH candidate AS (
    SELECT source_id, revision
    FROM brainspine_projection_tasks
    WHERE ((status = 'pending' AND next_attempt_at <= statement_timestamp())
        OR (status = 'running' AND lease_until <= statement_timestamp()))
      AND sqlc.arg(lease_seconds)::INT BETWEEN 1 AND 3600
      AND sqlc.arg(max_attempts)::INT BETWEEN 1 AND 1000
    ORDER BY COALESCE(lease_until, next_attempt_at), source_id, revision
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), claimed AS (
    UPDATE brainspine_projection_tasks AS task
    SET status = CASE WHEN task.attempts >= sqlc.arg(max_attempts)::INT
            THEN 'failed'::brainspine_projection_status
            ELSE 'running'::brainspine_projection_status END,
        attempts = task.attempts + CASE
            WHEN task.attempts < sqlc.arg(max_attempts)::INT THEN 1 ELSE 0 END,
        lease_generation = task.lease_generation + CASE
            WHEN task.attempts < sqlc.arg(max_attempts)::INT THEN 1 ELSE 0 END,
        lease_until = CASE WHEN task.attempts < sqlc.arg(max_attempts)::INT
            THEN statement_timestamp() + make_interval(secs => sqlc.arg(lease_seconds)::INT)
            ELSE NULL END,
        last_error = CASE WHEN task.attempts >= sqlc.arg(max_attempts)::INT
            THEN 'projection attempt budget exhausted before reclaim'
            ELSE task.last_error END,
        updated_at = statement_timestamp()
    FROM candidate
    WHERE task.source_id = candidate.source_id AND task.revision = candidate.revision
    RETURNING task.*
)
SELECT * FROM claimed WHERE status = 'running';

-- The lease must still be current and unexpired. A stale worker returns no row.
-- name: CompleteProjectionTask :one
UPDATE brainspine_projection_tasks
SET status = 'succeeded', lease_until = NULL, last_error = '',
    updated_at = statement_timestamp()
WHERE source_id = sqlc.arg(source_id) AND revision = sqlc.arg(revision)
  AND status = 'running' AND lease_generation = sqlc.arg(lease_generation)::BIGINT
  AND lease_until > statement_timestamp()
RETURNING *;

-- Retry delay is min(max, base * 2^(attempts-1)); the exponent is bounded before
-- evaluating POWER and both input delays are at most one day. No sleeping lease
-- occupies a worker slot. A terminal task is not reset by duplicate ingestion.
-- name: FailProjectionTask :one
UPDATE brainspine_projection_tasks
SET status = CASE WHEN attempts >= sqlc.arg(max_attempts)::INT
        THEN 'failed'::brainspine_projection_status
        ELSE 'pending'::brainspine_projection_status END,
    lease_until = NULL,
    next_attempt_at = statement_timestamp() + make_interval(secs => LEAST(
        sqlc.arg(retry_max_seconds)::INT::DOUBLE PRECISION,
        sqlc.arg(retry_base_seconds)::INT::DOUBLE PRECISION
            * power(2::DOUBLE PRECISION, LEAST(GREATEST(attempts - 1, 0), 30))
    )),
    last_error = left(sqlc.arg(last_error)::TEXT, 4096),
    updated_at = statement_timestamp()
WHERE source_id = sqlc.arg(source_id) AND revision = sqlc.arg(revision)
  AND status = 'running' AND lease_generation = sqlc.arg(lease_generation)::BIGINT
  AND lease_until > statement_timestamp()
  AND sqlc.arg(max_attempts)::INT BETWEEN 1 AND 1000
  AND sqlc.arg(retry_base_seconds)::INT BETWEEN 1 AND 86400
  AND sqlc.arg(retry_max_seconds)::INT BETWEEN sqlc.arg(retry_base_seconds)::INT AND 86400
RETURNING *;

-- name: GetProjectionTask :one
SELECT * FROM brainspine_projection_tasks
WHERE source_id = sqlc.arg(source_id) AND revision = sqlc.arg(revision);

-- Include zero counts; the PostgreSQL enum owns the complete status set.
-- name: CountProjectionTasks :many
SELECT statuses.status::brainspine_projection_status AS status,
       count(task.source_id)::BIGINT AS count
FROM unnest(enum_range(NULL::brainspine_projection_status)) AS statuses(status)
LEFT JOIN brainspine_projection_tasks AS task ON task.status = statuses.status
GROUP BY statuses.status
ORDER BY statuses.status;

-- name: ListSourceRevisions :many
SELECT * FROM brainspine_source_revisions WHERE source_id = sqlc.arg(source_id)
ORDER BY retrieved_at, revision
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- name: ListDocumentSources :many
SELECT * FROM brainspine_source_revisions WHERE content_hash = sqlc.arg(content_hash)
ORDER BY source_id, revision
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- A bounded source search is a metadata lookup, not a full-text/vector search.
-- name: SearchSourceRevisions :many
SELECT * FROM brainspine_source_revisions
WHERE source_id ILIKE '%' || sqlc.arg(query)::TEXT || '%'
   OR source_uri ILIKE '%' || sqlc.arg(query)::TEXT || '%'
   OR title ILIKE '%' || sqlc.arg(query)::TEXT || '%'
ORDER BY source_id, revision
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- Parent creation precedes child creation; this API never changes a parent.
-- Citation offsets are half-open byte intervals in the retained document.
-- name: CreateNode :one
INSERT INTO brainspine_nodes
    (node_id, kind, symbol_key, title, statement, parent_node_id, author_kind, author_ref,
     citation_content_hash, citation_source_id, citation_revision, citation_chunk_id,
     citation_start_byte, citation_end_byte, text_projection_ref, vector_projection_ref)
SELECT sqlc.arg(node_id)::TEXT, sqlc.arg(kind)::TEXT, sqlc.arg(symbol_key)::TEXT,
       sqlc.arg(title)::TEXT, sqlc.arg(statement)::TEXT, sqlc.narg(parent_node_id)::TEXT,
       sqlc.arg(author_kind)::TEXT, sqlc.arg(author_ref)::TEXT,
       sqlc.narg(citation_content_hash)::TEXT, sqlc.narg(citation_source_id)::TEXT,
       sqlc.narg(citation_revision)::TEXT, sqlc.narg(citation_chunk_id)::TEXT,
       sqlc.narg(citation_start_byte)::BIGINT, sqlc.narg(citation_end_byte)::BIGINT,
       sqlc.narg(text_projection_ref)::TEXT, sqlc.narg(vector_projection_ref)::TEXT
WHERE sqlc.narg(citation_end_byte)::BIGINT IS NULL OR EXISTS (
    SELECT 1 FROM brainspine_documents
    WHERE content_hash = sqlc.narg(citation_content_hash)::TEXT
      AND byte_size >= sqlc.narg(citation_end_byte)::BIGINT
)
ON CONFLICT (node_id) DO UPDATE SET node_id = EXCLUDED.node_id
WHERE brainspine_nodes.kind = EXCLUDED.kind
  AND brainspine_nodes.symbol_key = EXCLUDED.symbol_key
  AND brainspine_nodes.title = EXCLUDED.title
  AND brainspine_nodes.statement = EXCLUDED.statement
  AND brainspine_nodes.parent_node_id IS NOT DISTINCT FROM EXCLUDED.parent_node_id
  AND brainspine_nodes.author_kind = EXCLUDED.author_kind
  AND brainspine_nodes.author_ref = EXCLUDED.author_ref
  AND brainspine_nodes.citation_content_hash IS NOT DISTINCT FROM EXCLUDED.citation_content_hash
  AND brainspine_nodes.citation_source_id IS NOT DISTINCT FROM EXCLUDED.citation_source_id
  AND brainspine_nodes.citation_revision IS NOT DISTINCT FROM EXCLUDED.citation_revision
  AND brainspine_nodes.citation_chunk_id IS NOT DISTINCT FROM EXCLUDED.citation_chunk_id
  AND brainspine_nodes.citation_start_byte IS NOT DISTINCT FROM EXCLUDED.citation_start_byte
  AND brainspine_nodes.citation_end_byte IS NOT DISTINCT FROM EXCLUDED.citation_end_byte
  AND brainspine_nodes.text_projection_ref IS NOT DISTINCT FROM EXCLUDED.text_projection_ref
  AND brainspine_nodes.vector_projection_ref IS NOT DISTINCT FROM EXCLUDED.vector_projection_ref
RETURNING *;

-- name: GetNode :one
SELECT * FROM brainspine_nodes WHERE node_id = sqlc.arg(node_id);

-- NULL selects roots, otherwise immediate children in the symbolic tree.
-- name: ListChildNodes :many
SELECT * FROM brainspine_nodes WHERE parent_node_id IS NOT DISTINCT FROM sqlc.narg(parent_node_id)::TEXT
ORDER BY created_at, node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- All versions are returned. Neither timestamps nor model edges pick a winner.
-- name: ListSymbolNodes :many
SELECT * FROM brainspine_nodes WHERE symbol_key = sqlc.arg(symbol_key)
ORDER BY created_at, node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- name: ListDocumentNodes :many
SELECT * FROM brainspine_nodes WHERE citation_content_hash = sqlc.arg(content_hash)
ORDER BY citation_start_byte NULLS FIRST, node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- Supersession links versions of the same typed symbol. A refutation is an
-- independent assertion and does not erase or deactivate either endpoint.
-- name: CreateEdge :one
INSERT INTO brainspine_edges (from_node_id, to_node_id, relation, author_kind, author_ref, rationale)
SELECT origin.node_id, target.node_id, sqlc.arg(relation)::TEXT,
       sqlc.arg(author_kind)::TEXT, sqlc.arg(author_ref)::TEXT, sqlc.arg(rationale)::TEXT
FROM brainspine_nodes AS origin, brainspine_nodes AS target
WHERE origin.node_id = sqlc.arg(from_node_id)::TEXT AND target.node_id = sqlc.arg(to_node_id)::TEXT
  AND (sqlc.arg(relation)::TEXT <> 'supersedes' OR
       (origin.kind = target.kind AND origin.symbol_key = target.symbol_key))
ON CONFLICT (from_node_id, to_node_id, relation) DO UPDATE SET relation = EXCLUDED.relation
WHERE brainspine_edges.author_kind = EXCLUDED.author_kind
  AND brainspine_edges.author_ref = EXCLUDED.author_ref
  AND brainspine_edges.rationale = EXCLUDED.rationale
RETURNING *;

-- name: ListNodeEdges :many
SELECT * FROM brainspine_edges
WHERE from_node_id = sqlc.arg(node_id) OR to_node_id = sqlc.arg(node_id)
ORDER BY relation, from_node_id, to_node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- name: GetEdge :one
SELECT * FROM brainspine_edges
WHERE from_node_id = sqlc.arg(from_node_id) AND to_node_id = sqlc.arg(to_node_id)
  AND relation = sqlc.arg(relation);

-- name: GetRun :one
SELECT * FROM brainspine_runs WHERE run_id = sqlc.arg(run_id);

-- Every mutation of attempts, scores, activations, or run completion begins
-- with this statement in the same transaction. It must be a separate statement
-- so READ COMMITTED takes a fresh snapshot after a contended lock is acquired.
-- name: LockRun :one
SELECT * FROM brainspine_runs WHERE run_id = sqlc.arg(run_id) FOR UPDATE;

-- name: ListRuns :many
SELECT * FROM brainspine_runs ORDER BY created_at DESC, run_id;

-- name: CreateCandidate :one
INSERT INTO brainspine_candidates (run_id, candidate_hash, name, program_artifact_ref)
VALUES (sqlc.arg(run_id), sqlc.arg(candidate_hash), sqlc.arg(name), sqlc.arg(program_artifact_ref))
ON CONFLICT (run_id, candidate_hash) DO UPDATE SET candidate_hash = EXCLUDED.candidate_hash
WHERE brainspine_candidates.name = EXCLUDED.name
  AND brainspine_candidates.program_artifact_ref = EXCLUDED.program_artifact_ref
RETURNING *;

-- name: ListCandidates :many
SELECT * FROM brainspine_candidates WHERE run_id = sqlc.arg(run_id)
ORDER BY created_at, candidate_hash;

-- name: GetAttempt :one
SELECT * FROM brainspine_attempts WHERE attempt_id = sqlc.arg(attempt_id);

-- name: ListAttempts :many
SELECT * FROM brainspine_attempts WHERE run_id = sqlc.arg(run_id)
ORDER BY created_at, attempt_id;

-- Requires LockRun in this transaction. An exact retry returns the existing
-- attempt regardless of its current status, without changing the reservation.
-- A conflicting ID, closed run, or insufficient allowance returns no row.
-- A cross-run concurrent ID collision raises a uniqueness error; roll back the
-- transaction, never swallow that error and commit a partial reservation.
-- name: ReserveAttempt :one
WITH existing AS MATERIALIZED (
    SELECT * FROM brainspine_attempts WHERE attempt_id = sqlc.arg(attempt_id)::TEXT
), allowance AS (
    UPDATE brainspine_runs
    SET reserved_usd_micros = reserved_usd_micros + sqlc.arg(reservation_usd_micros)::BIGINT
    WHERE run_id = sqlc.arg(run_id)::TEXT
      AND status = 'running'
      AND sqlc.arg(reservation_usd_micros)::BIGINT BETWEEN 0 AND 300000000
      AND budget_limit_usd_micros - reserved_usd_micros - charged_usd_micros >= sqlc.arg(reservation_usd_micros)::BIGINT
      AND NOT EXISTS (SELECT 1 FROM existing)
    RETURNING run_id
), inserted AS (
    INSERT INTO brainspine_attempts
        (attempt_id, run_id, candidate_hash, split, evaluator, request_artifact_ref, reservation_usd_micros)
    SELECT sqlc.arg(attempt_id)::TEXT, allowance.run_id, sqlc.arg(candidate_hash)::TEXT,
           sqlc.arg(split)::TEXT, sqlc.arg(evaluator)::TEXT, sqlc.arg(request_artifact_ref)::TEXT,
           sqlc.arg(reservation_usd_micros)::BIGINT
    FROM allowance
    RETURNING *
)
SELECT * FROM inserted
UNION ALL
SELECT * FROM existing
WHERE run_id = sqlc.arg(run_id)::TEXT
  AND candidate_hash = sqlc.arg(candidate_hash)::TEXT
  AND split = sqlc.arg(split)::TEXT
  AND evaluator = sqlc.arg(evaluator)::TEXT
  AND request_artifact_ref = sqlc.arg(request_artifact_ref)::TEXT
  AND reservation_usd_micros = sqlc.arg(reservation_usd_micros)::BIGINT;

-- Only the first transition returns a row. A retry must inspect GetAttempt;
-- running/ambiguous is not permission to dispatch an external call again.
-- name: StartAttempt :one
UPDATE brainspine_attempts
SET status = 'running', started_at = now()
WHERE run_id = sqlc.arg(run_id) AND attempt_id = sqlc.arg(attempt_id) AND status = 'reserved'
RETURNING *;

-- name: BindProviderOperation :one
UPDATE brainspine_attempts
SET provider_operation_id = sqlc.arg(provider_operation_id)
WHERE run_id = sqlc.arg(run_id) AND attempt_id = sqlc.arg(attempt_id)
  AND sqlc.arg(provider_operation_id)::TEXT <> ''
  AND (provider_operation_id = '' OR provider_operation_id = sqlc.arg(provider_operation_id))
RETURNING *;

-- Ambiguity keeps the full reservation until provider reconciliation.
-- name: MarkAttemptAmbiguous :one
UPDATE brainspine_attempts SET status = 'ambiguous'
WHERE run_id = sqlc.arg(run_id) AND attempt_id = sqlc.arg(attempt_id)
  AND status IN ('running', 'ambiguous')
RETURNING *;

-- Requires LockRun first. Budget settlement and terminal outcome are one
-- statement. A retry returns the existing result only if all terminal fields
-- match. A conflicting retry or charge above the reservation returns no row.
-- name: FinalizeAttempt :one
WITH current_attempt AS MATERIALIZED (
    SELECT * FROM brainspine_attempts
    WHERE run_id = sqlc.arg(run_id)::TEXT AND attempt_id = sqlc.arg(attempt_id)::TEXT
    FOR UPDATE
), settled AS (
    UPDATE brainspine_runs AS runs
    SET reserved_usd_micros = runs.reserved_usd_micros - attempt.reservation_usd_micros,
        charged_usd_micros = runs.charged_usd_micros + sqlc.arg(charged_usd_micros)::BIGINT
    FROM current_attempt AS attempt
    WHERE runs.run_id = attempt.run_id
      AND attempt.status IN ('reserved', 'running', 'ambiguous')
      AND sqlc.arg(status)::TEXT IN ('succeeded', 'failed', 'cancelled')
      AND sqlc.arg(charged_usd_micros)::BIGINT BETWEEN 0 AND attempt.reservation_usd_micros
      AND sqlc.arg(outcome)::TEXT <> '' AND sqlc.arg(result_artifact_ref)::TEXT <> ''
    RETURNING runs.run_id
), completed AS (
    UPDATE brainspine_attempts AS attempts
    SET status = sqlc.arg(status)::TEXT,
        charged_usd_micros = sqlc.arg(charged_usd_micros)::BIGINT,
        outcome = sqlc.arg(outcome)::TEXT,
        result_artifact_ref = sqlc.arg(result_artifact_ref)::TEXT,
        finished_at = now()
    FROM current_attempt AS current, settled
    WHERE attempts.attempt_id = current.attempt_id AND current.run_id = settled.run_id
    RETURNING attempts.*
)
SELECT * FROM completed
UNION ALL
SELECT * FROM current_attempt
WHERE status IN ('succeeded', 'failed', 'cancelled')
  AND status = sqlc.arg(status)::TEXT
  AND charged_usd_micros = sqlc.arg(charged_usd_micros)::BIGINT
  AND outcome = sqlc.arg(outcome)::TEXT
  AND result_artifact_ref = sqlc.arg(result_artifact_ref)::TEXT;

-- Call after FinalizeAttempt in the same transaction for atomic result+scores.
-- An exact replay returns the existing score; a conflicting replay returns none.
-- name: RecordScore :one
INSERT INTO brainspine_scores (attempt_id, metric_name, metric_value, sample_count)
SELECT attempt_id, sqlc.arg(metric_name)::TEXT, sqlc.arg(metric_value)::DOUBLE PRECISION,
       sqlc.arg(sample_count)::BIGINT
FROM brainspine_attempts
WHERE attempt_id = sqlc.arg(attempt_id)::TEXT AND run_id = sqlc.arg(run_id)::TEXT AND status = 'succeeded'
ON CONFLICT (attempt_id, metric_name) DO UPDATE SET metric_name = EXCLUDED.metric_name
WHERE brainspine_scores.metric_value = EXCLUDED.metric_value
  AND brainspine_scores.sample_count = EXCLUDED.sample_count
RETURNING *;

-- name: ListScores :many
SELECT attempts.run_id, attempts.candidate_hash, attempts.split, scores.*
FROM brainspine_scores AS scores
JOIN brainspine_attempts AS attempts ON attempts.attempt_id = scores.attempt_id
WHERE attempts.run_id = sqlc.arg(run_id)
ORDER BY attempts.candidate_hash, attempts.split, scores.attempt_id, scores.metric_name;

-- Admission policy and expected previous epoch are checked by the caller while
-- holding LockRun. A successful scored validation is required; test scores can
-- never be supplied as the activation's validation attempt.
-- name: RecordActivation :one
INSERT INTO brainspine_activations
    (receipt_id, run_id, candidate_hash, validation_attempt_id, epoch, policy_artifact_ref, decision_artifact_ref)
SELECT sqlc.arg(receipt_id)::TEXT, attempts.run_id, attempts.candidate_hash, attempts.attempt_id,
       sqlc.arg(epoch)::BIGINT, sqlc.arg(policy_artifact_ref)::TEXT, sqlc.arg(decision_artifact_ref)::TEXT
FROM brainspine_attempts AS attempts
JOIN brainspine_runs AS runs ON runs.run_id = attempts.run_id
WHERE attempts.run_id = sqlc.arg(run_id)::TEXT
  AND attempts.candidate_hash = sqlc.arg(candidate_hash)::TEXT
  AND attempts.attempt_id = sqlc.arg(validation_attempt_id)::TEXT
  AND attempts.split = 'validation' AND attempts.status = 'succeeded'
  AND (runs.status = 'running' OR EXISTS (
      SELECT 1 FROM brainspine_activations WHERE receipt_id = sqlc.arg(receipt_id)::TEXT
  ))
  AND EXISTS (SELECT 1 FROM brainspine_scores WHERE attempt_id = attempts.attempt_id)
ON CONFLICT (receipt_id) DO UPDATE SET receipt_id = EXCLUDED.receipt_id
WHERE brainspine_activations.run_id = EXCLUDED.run_id
  AND brainspine_activations.candidate_hash = EXCLUDED.candidate_hash
  AND brainspine_activations.validation_attempt_id = EXCLUDED.validation_attempt_id
  AND brainspine_activations.epoch = EXCLUDED.epoch
  AND brainspine_activations.policy_artifact_ref = EXCLUDED.policy_artifact_ref
  AND brainspine_activations.decision_artifact_ref = EXCLUDED.decision_artifact_ref
RETURNING *;

-- name: ListActivations :many
SELECT * FROM brainspine_activations WHERE run_id = sqlc.arg(run_id)
ORDER BY epoch;

-- name: FinishRun :one
UPDATE brainspine_runs AS runs SET status = sqlc.arg(status), finished_at = now()
WHERE runs.run_id = sqlc.arg(run_id) AND runs.status = 'running'
  AND sqlc.arg(status)::TEXT IN ('completed', 'failed')
  AND reserved_usd_micros = 0
  AND NOT EXISTS (
      SELECT 1 FROM brainspine_attempts AS attempts
      WHERE attempts.run_id = runs.run_id AND attempts.status IN ('reserved', 'running', 'ambiguous')
  )
RETURNING *;
