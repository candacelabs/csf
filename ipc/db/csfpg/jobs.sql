-- The job ledger: admitted requests to external executors. Every read that
-- returns jobs is restricted to the kinds its caller decodes, so one ledger
-- never hands another consumer's request to the wrong decoder.

-- Budget accounts are immutable after creation: a repeated ensure with a
-- different limit returns no row.
-- name: EnsureJobBudget :one
INSERT INTO csf_job_budgets (account, limit_usd_micros) VALUES ($1, $2)
ON CONFLICT (account) DO UPDATE SET account = EXCLUDED.account
WHERE csf_job_budgets.limit_usd_micros = EXCLUDED.limit_usd_micros
RETURNING *;

-- The first statement of every admission transaction: it serializes
-- admissions against one account, including concurrent retries.
-- name: LockJobBudget :one
SELECT * FROM csf_job_budgets WHERE account = $1 FOR UPDATE;

-- name: GetJobBudget :one
SELECT * FROM csf_job_budgets WHERE account = $1;

-- name: ReserveJobBudget :execrows
UPDATE csf_job_budgets SET reserved_usd_micros = reserved_usd_micros + sqlc.arg(amount)::BIGINT
WHERE account = sqlc.arg(account)
AND limit_usd_micros - reserved_usd_micros >= sqlc.arg(amount)::BIGINT;

-- name: InsertJob :one
INSERT INTO csf_jobs
 (job_id, kind, executor, budget_account, request, request_sha256, total_units, managed,
  executor_target, executor_image, artifact_uri, reservation_usd_micros, timeout_seconds)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING *;

-- name: GetJob :one
SELECT * FROM csf_jobs WHERE job_id = $1 AND kind = ANY(sqlc.arg(kinds)::TEXT[]);

-- name: LockJob :one
SELECT * FROM csf_jobs WHERE job_id = $1 AND kind = ANY(sqlc.arg(kinds)::TEXT[]) FOR UPDATE;

-- name: ListJobs :many
SELECT * FROM csf_jobs WHERE kind = ANY(sqlc.arg(kinds)::TEXT[])
ORDER BY created_at DESC, job_id LIMIT sqlc.arg(row_limit);

-- name: RequestJobCancellation :one
UPDATE csf_jobs SET cancellation_requested = true,
 state = CASE WHEN state = 'pending' THEN 'cancelled'::csf_job_state ELSE state END,
 cleanup_confirmed = CASE WHEN state = 'pending' AND managed THEN true ELSE cleanup_confirmed END,
 updated_at = now()
WHERE job_id = $1 AND kind = ANY(sqlc.arg(kinds)::TEXT[]) RETURNING *;

-- name: ListJobMetricDefinitions :many
SELECT * FROM csf_job_metric_definitions WHERE job_id = $1 ORDER BY name;

-- A replayed identical definition counts one row; a changed one counts none.
-- name: InsertJobMetricDefinition :execrows
INSERT INTO csf_job_metric_definitions (job_id, name, unit, description) VALUES ($1,$2,$3,$4)
ON CONFLICT (job_id, name) DO UPDATE SET name = EXCLUDED.name
WHERE csf_job_metric_definitions.unit = EXCLUDED.unit
AND csf_job_metric_definitions.description = EXCLUDED.description;

-- A replayed identical value counts one row; a conflicting one counts none.
-- name: InsertJobMeasurement :execrows
INSERT INTO csf_job_measurements (job_id, metric, step, value, recorded_at, evidence_hash) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (job_id, metric, step) DO UPDATE SET metric = EXCLUDED.metric
WHERE csf_job_measurements.value = EXCLUDED.value;

-- name: LatestJobMeasurements :many
SELECT DISTINCT ON (metric) * FROM csf_job_measurements
WHERE job_id = $1 ORDER BY metric, step DESC;

-- name: RecordJobProgress :exec
UPDATE csf_jobs SET completed_units = GREATEST(completed_units, sqlc.arg(completed_units)::BIGINT),
 state = sqlc.arg(state), reason = sqlc.arg(reason), updated_at = now()
WHERE job_id = sqlc.arg(job_id);

-- Remote executors: a submission is claimed and committed before the
-- executor is called, because a provider without an idempotency key cannot
-- be retried blindly.
-- name: ClaimJobSubmission :one
UPDATE csf_jobs SET state = 'submitting', updated_at = now()
WHERE job_id = (SELECT candidate.job_id FROM csf_jobs AS candidate
 WHERE candidate.state = 'pending' AND candidate.managed AND candidate.executor = sqlc.arg(executor)
 AND candidate.kind = ANY(sqlc.arg(kinds)::TEXT[])
 ORDER BY candidate.created_at, candidate.job_id FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING *;

-- name: MarkInterruptedJobSubmissions :exec
UPDATE csf_jobs SET state = 'submission_unknown', reason = sqlc.arg(reason), updated_at = now()
WHERE state = 'submitting' AND executor = sqlc.arg(executor)
AND updated_at < now() - make_interval(secs => sqlc.arg(grace_seconds)::INTEGER);

-- name: FinishJobSubmission :execrows
UPDATE csf_jobs SET state = $2, external_id = $3, reason = $4, updated_at = now()
WHERE job_id = $1 AND state IN ('submitting', 'submission_unknown');

-- Terminal jobs stay pollable briefly so trailing log pages are drained.
-- name: PollableJobs :many
SELECT * FROM csf_jobs WHERE executor = sqlc.arg(executor) AND external_id <> ''
AND kind = ANY(sqlc.arg(kinds)::TEXT[])
AND (state IN ('queued', 'running', 'cancelling')
 OR (state IN ('succeeded', 'failed', 'cancelled') AND updated_at > now() - interval '10 minutes'))
ORDER BY updated_at, job_id LIMIT sqlc.arg(row_limit);

-- The update time moves only when the state does.
-- name: UpdateJobExecution :exec
UPDATE csf_jobs SET state = $2, external_id = $3, reason = $4, log_stream = $5,
 cleanup_confirmed = $6, inspection_error = '',
 updated_at = CASE WHEN state <> $2 THEN now() ELSE updated_at END
WHERE job_id = $1;

-- name: SetJobInspectionError :exec
UPDATE csf_jobs SET inspection_error = $2 WHERE job_id = $1;

-- name: SetJobLogCursor :exec
UPDATE csf_jobs SET log_cursor = $2 WHERE job_id = $1;

-- Host executors: one transaction-scoped lock per executor serializes host
-- reconciliation, even across overlapping deploys.
-- name: LockJobExecutor :one
SELECT pg_try_advisory_xact_lock(67545231, hashtext(sqlc.arg(executor)::TEXT)) AS acquired;

-- name: NextHostJob :one
SELECT * FROM csf_jobs
WHERE executor = sqlc.arg(executor) AND managed AND NOT cleanup_confirmed
AND kind = ANY(sqlc.arg(kinds)::TEXT[])
ORDER BY (state = 'pending'), created_at, job_id LIMIT 1 FOR UPDATE;

-- name: NextUntracedJob :one
SELECT * FROM csf_jobs WHERE executor = sqlc.arg(executor)
 AND kind = ANY(sqlc.arg(kinds)::TEXT[])
 AND state IN ('succeeded', 'failed', 'cancelled')
 AND (NOT managed OR cleanup_confirmed)
 AND trace_url = '' AND trace_retry_at <= now()
ORDER BY created_at DESC LIMIT 1;

-- name: SetJobTrace :exec
UPDATE csf_jobs SET trace_url = $2, trace_export_error = $3,
 trace_retry_at = now() + interval '1 minute' WHERE job_id = $1;

-- name: NextUnarchivedJob :one
SELECT * FROM csf_jobs WHERE executor = sqlc.arg(executor)
 AND kind = ANY(sqlc.arg(kinds)::TEXT[])
 AND state IN ('succeeded', 'failed', 'cancelled')
 AND cleanup_confirmed
 AND log_document_id = '' AND log_retry_at <= now()
ORDER BY created_at DESC LIMIT 1;

-- name: SetJobLogArchive :exec
UPDATE csf_jobs SET log_document_id = $2,
 log_projection_error = $3, log_indexed_at = $4,
 log_retry_at = now() + interval '1 minute' WHERE job_id = $1;

-- name: CountJobStates :many
SELECT kind, executor, state, count(*)::BIGINT AS jobs
FROM csf_jobs WHERE kind = ANY(sqlc.arg(kinds)::TEXT[]) GROUP BY kind, executor, state;

-- name: LatestJobProgress :many
SELECT DISTINCT ON (kind, executor) kind, executor, total_units, completed_units, updated_at
FROM csf_jobs WHERE kind = ANY(sqlc.arg(kinds)::TEXT[])
ORDER BY kind, executor, created_at DESC, job_id;

-- name: ClaimJobTraceDelivery :one
INSERT INTO csf_job_trace_deliveries (destination, job_id, trace_id)
VALUES (sqlc.arg(destination), sqlc.arg(job_id), sqlc.arg(trace_id))
ON CONFLICT (destination, trace_id) DO NOTHING
RETURNING trace_id;

-- name: GetJobTraceDelivery :one
SELECT * FROM csf_job_trace_deliveries
WHERE destination = sqlc.arg(destination) AND trace_id = sqlc.arg(trace_id);

-- name: FinishJobTraceDelivery :one
UPDATE csf_job_trace_deliveries
SET state = sqlc.arg(state)::csf_trace_delivery_state,
 error = sqlc.arg(error), finished_at = now()
WHERE destination = sqlc.arg(destination) AND trace_id = sqlc.arg(trace_id)
 AND state = 'attempted'
 AND sqlc.arg(state)::csf_trace_delivery_state IN
     ('succeeded'::csf_trace_delivery_state, 'ambiguous'::csf_trace_delivery_state)
RETURNING trace_id;
