-- name: CreateDocument :one
INSERT INTO csf_documents (content_hash, byte_size, artifact_ref)
VALUES (sqlc.arg(content_hash), sqlc.arg(byte_size), sqlc.arg(artifact_ref))
ON CONFLICT (content_hash) DO UPDATE SET content_hash = EXCLUDED.content_hash
WHERE csf_documents.byte_size = EXCLUDED.byte_size
RETURNING *;

-- name: GetDocument :one
SELECT * FROM csf_documents WHERE content_hash = sqlc.arg(content_hash);

-- name: ListDocuments :many
SELECT * FROM csf_documents ORDER BY content_hash
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- Same immutable revision/content/metadata retains the first URI and retrieval
-- time, even when a retry arrives through another locator at a later time.
-- name: CreateSourceRevision :one
INSERT INTO csf_source_revisions
    (source_id, revision, content_hash, raw_source_content_hash, source_uri, title, media_type, license, retrieved_at)
VALUES (sqlc.arg(source_id), sqlc.arg(revision), sqlc.arg(content_hash), sqlc.narg(raw_source_content_hash),
        sqlc.arg(source_uri), sqlc.arg(title), sqlc.arg(media_type), sqlc.arg(license), sqlc.arg(retrieved_at))
ON CONFLICT (source_id, revision) DO UPDATE SET source_id = EXCLUDED.source_id
WHERE csf_source_revisions.content_hash = EXCLUDED.content_hash
  AND csf_source_revisions.raw_source_content_hash IS NOT DISTINCT FROM EXCLUDED.raw_source_content_hash
  AND csf_source_revisions.title = EXCLUDED.title
  AND csf_source_revisions.media_type = EXCLUDED.media_type
  AND csf_source_revisions.license = EXCLUDED.license
RETURNING *;

-- name: GetSourceRevision :one
SELECT * FROM csf_source_revisions
WHERE source_id = sqlc.arg(source_id) AND revision = sqlc.arg(revision);

-- Call in the source-registration transaction. Duplicate enqueue preserves the
-- lease, retry schedule and terminal state; it never starts a second task.
-- name: EnqueueProjectionTask :one
INSERT INTO csf_projection_tasks (source_id, revision)
VALUES (sqlc.arg(source_id), sqlc.arg(revision))
ON CONFLICT (source_id, revision) DO UPDATE SET source_id = EXCLUDED.source_id
RETURNING *;

-- name: BackfillProjectionTasks :execrows
INSERT INTO csf_projection_tasks (source_id, revision)
SELECT source_id, revision FROM csf_source_revisions
ON CONFLICT (source_id, revision) DO NOTHING;

-- Claim at most one due row. Expired final attempts become failed without
-- granting another lease; no returned row is not proof that the queue is empty.
-- Commit before calling the external projection backend.
-- name: ClaimProjectionTask :one
WITH candidate AS (
    SELECT source_id, revision
    FROM csf_projection_tasks
    WHERE ((status = 'pending' AND next_attempt_at <= statement_timestamp())
        OR (status = 'running' AND lease_until <= statement_timestamp()))
      AND sqlc.arg(lease_seconds)::INT BETWEEN 1 AND 3600
      AND sqlc.arg(max_attempts)::INT BETWEEN 1 AND 1000
    ORDER BY COALESCE(lease_until, next_attempt_at), source_id, revision
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), claimed AS (
    UPDATE csf_projection_tasks AS task
    SET status = CASE WHEN task.attempts >= sqlc.arg(max_attempts)::INT
            THEN 'failed'::csf_projection_status
            ELSE 'running'::csf_projection_status END,
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
UPDATE csf_projection_tasks
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
UPDATE csf_projection_tasks
SET status = CASE WHEN attempts >= sqlc.arg(max_attempts)::INT
        THEN 'failed'::csf_projection_status
        ELSE 'pending'::csf_projection_status END,
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
SELECT * FROM csf_projection_tasks
WHERE source_id = sqlc.arg(source_id) AND revision = sqlc.arg(revision);

-- Include zero counts; the PostgreSQL enum owns the complete status set.
-- name: CountProjectionTasks :many
SELECT statuses.status::csf_projection_status AS status,
       count(task.source_id)::BIGINT AS count
FROM unnest(enum_range(NULL::csf_projection_status)) AS statuses(status)
LEFT JOIN csf_projection_tasks AS task ON task.status = statuses.status
GROUP BY statuses.status
ORDER BY statuses.status;

-- name: ListSourceRevisions :many
SELECT * FROM csf_source_revisions WHERE source_id = sqlc.arg(source_id)
ORDER BY retrieved_at, revision
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- name: ListDocumentSources :many
SELECT * FROM csf_source_revisions WHERE content_hash = sqlc.arg(content_hash)
ORDER BY source_id, revision
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- A bounded source search is a metadata lookup, not a full-text/vector search.
-- name: SearchSourceRevisions :many
SELECT * FROM csf_source_revisions
WHERE source_id ILIKE '%' || sqlc.arg(query)::TEXT || '%'
   OR source_uri ILIKE '%' || sqlc.arg(query)::TEXT || '%'
   OR title ILIKE '%' || sqlc.arg(query)::TEXT || '%'
ORDER BY source_id, revision
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- Parent creation precedes child creation; this API never changes a parent.
-- Citation offsets are half-open byte intervals in the retained document.
-- name: CreateNode :one
INSERT INTO csf_nodes
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
    SELECT 1 FROM csf_documents
    WHERE content_hash = sqlc.narg(citation_content_hash)::TEXT
      AND byte_size >= sqlc.narg(citation_end_byte)::BIGINT
)
ON CONFLICT (node_id) DO UPDATE SET node_id = EXCLUDED.node_id
WHERE csf_nodes.kind = EXCLUDED.kind
  AND csf_nodes.symbol_key = EXCLUDED.symbol_key
  AND csf_nodes.title = EXCLUDED.title
  AND csf_nodes.statement = EXCLUDED.statement
  AND csf_nodes.parent_node_id IS NOT DISTINCT FROM EXCLUDED.parent_node_id
  AND csf_nodes.author_kind = EXCLUDED.author_kind
  AND csf_nodes.author_ref = EXCLUDED.author_ref
  AND csf_nodes.citation_content_hash IS NOT DISTINCT FROM EXCLUDED.citation_content_hash
  AND csf_nodes.citation_source_id IS NOT DISTINCT FROM EXCLUDED.citation_source_id
  AND csf_nodes.citation_revision IS NOT DISTINCT FROM EXCLUDED.citation_revision
  AND csf_nodes.citation_chunk_id IS NOT DISTINCT FROM EXCLUDED.citation_chunk_id
  AND csf_nodes.citation_start_byte IS NOT DISTINCT FROM EXCLUDED.citation_start_byte
  AND csf_nodes.citation_end_byte IS NOT DISTINCT FROM EXCLUDED.citation_end_byte
  AND csf_nodes.text_projection_ref IS NOT DISTINCT FROM EXCLUDED.text_projection_ref
  AND csf_nodes.vector_projection_ref IS NOT DISTINCT FROM EXCLUDED.vector_projection_ref
RETURNING *;

-- name: GetNode :one
SELECT * FROM csf_nodes WHERE node_id = sqlc.arg(node_id);

-- NULL selects roots, otherwise immediate children in the symbolic tree.
-- name: ListChildNodes :many
SELECT * FROM csf_nodes WHERE parent_node_id IS NOT DISTINCT FROM sqlc.narg(parent_node_id)::TEXT
ORDER BY created_at, node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- All versions are returned. Neither timestamps nor model edges pick a winner.
-- name: ListSymbolNodes :many
SELECT * FROM csf_nodes WHERE symbol_key = sqlc.arg(symbol_key)
ORDER BY created_at, node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- name: ListDocumentNodes :many
SELECT * FROM csf_nodes WHERE citation_content_hash = sqlc.arg(content_hash)
ORDER BY citation_start_byte NULLS FIRST, node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- Supersession links versions of the same typed symbol. A refutation is an
-- independent assertion and does not erase or deactivate either endpoint.
-- name: CreateEdge :one
INSERT INTO csf_edges (from_node_id, to_node_id, relation, author_kind, author_ref, rationale)
SELECT origin.node_id, target.node_id, sqlc.arg(relation)::TEXT,
       sqlc.arg(author_kind)::TEXT, sqlc.arg(author_ref)::TEXT, sqlc.arg(rationale)::TEXT
FROM csf_nodes AS origin, csf_nodes AS target
WHERE origin.node_id = sqlc.arg(from_node_id)::TEXT AND target.node_id = sqlc.arg(to_node_id)::TEXT
  AND (sqlc.arg(relation)::TEXT <> 'supersedes' OR
       (origin.kind = target.kind AND origin.symbol_key = target.symbol_key))
ON CONFLICT (from_node_id, to_node_id, relation) DO UPDATE SET relation = EXCLUDED.relation
WHERE csf_edges.author_kind = EXCLUDED.author_kind
  AND csf_edges.author_ref = EXCLUDED.author_ref
  AND csf_edges.rationale = EXCLUDED.rationale
RETURNING *;

-- name: ListNodeEdges :many
SELECT * FROM csf_edges
WHERE from_node_id = sqlc.arg(node_id) OR to_node_id = sqlc.arg(node_id)
ORDER BY relation, from_node_id, to_node_id
LIMIT sqlc.arg(result_limit)::INT OFFSET sqlc.arg(result_offset)::BIGINT;

-- name: GetEdge :one
SELECT * FROM csf_edges
WHERE from_node_id = sqlc.arg(from_node_id) AND to_node_id = sqlc.arg(to_node_id)
  AND relation = sqlc.arg(relation);
