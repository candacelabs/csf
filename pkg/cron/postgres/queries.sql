-- name: UpsertJob :one
INSERT INTO candace_cron_jobs (
    job_name,
    schedule_kind,
    local_hour,
    local_minute,
    weekday,
    month_day,
    interval_nanoseconds,
    raw_expression,
    timezone,
    interval_anchor_at,
    schedule_cursor_at,
    next_run_at,
    catch_up_policy,
    overlap_policy,
    enabled
) VALUES (
    sqlc.arg(job_name),
    sqlc.arg(schedule_kind),
    sqlc.narg(local_hour),
    sqlc.narg(local_minute),
    sqlc.narg(weekday),
    sqlc.narg(month_day),
    sqlc.narg(interval_nanoseconds),
    sqlc.narg(raw_expression),
    sqlc.arg(timezone),
    sqlc.narg(interval_anchor_at),
    sqlc.narg(schedule_cursor_at),
    sqlc.narg(next_run_at),
    sqlc.arg(catch_up_policy),
    sqlc.arg(overlap_policy),
    sqlc.arg(enabled)
)
ON CONFLICT (job_name) DO UPDATE SET
    schedule_kind = EXCLUDED.schedule_kind,
    local_hour = EXCLUDED.local_hour,
    local_minute = EXCLUDED.local_minute,
    weekday = EXCLUDED.weekday,
    month_day = EXCLUDED.month_day,
    interval_nanoseconds = EXCLUDED.interval_nanoseconds,
    raw_expression = EXCLUDED.raw_expression,
    timezone = EXCLUDED.timezone,
    interval_anchor_at = EXCLUDED.interval_anchor_at,
    schedule_cursor_at = EXCLUDED.schedule_cursor_at,
    next_run_at = EXCLUDED.next_run_at,
    catch_up_policy = EXCLUDED.catch_up_policy,
    overlap_policy = EXCLUDED.overlap_policy,
    enabled = EXCLUDED.enabled,
    updated_at = now()
RETURNING *;

-- name: GetJobForUpdate :one
SELECT *
FROM candace_cron_jobs
WHERE job_name = sqlc.arg(job_name)
FOR UPDATE;

-- name: LockReconciliation :exec
SELECT pg_advisory_xact_lock(824236291);

-- name: ListExpiredRunningRuns :many
SELECT run.*
FROM candace_cron_runs AS run
JOIN candace_cron_jobs AS job ON job.job_name = run.job_name
WHERE job.enabled
  AND run.status = 'running'
  AND run.lease_until <= sqlc.arg(expired_at)
ORDER BY run.lease_until, run.scheduled_at, run.occurrence_id
LIMIT sqlc.arg(row_limit);

-- name: ListJobs :many
SELECT *
FROM candace_cron_jobs
ORDER BY job_name;

-- name: ListJobsForUpdate :many
SELECT *
FROM candace_cron_jobs
ORDER BY job_name
FOR UPDATE;

-- name: SetJobEnabled :one
UPDATE candace_cron_jobs
SET enabled = sqlc.arg(enabled),
    updated_at = now()
WHERE job_name = sqlc.arg(job_name)
RETURNING *;

-- name: GetRunForUpdate :one
SELECT *
FROM candace_cron_runs
WHERE occurrence_id = sqlc.arg(occurrence_id)
FOR UPDATE;

-- name: HasLiveRunForJob :one
SELECT EXISTS (
    SELECT 1
    FROM candace_cron_runs
    WHERE job_name = sqlc.arg(job_name)
      AND status = 'running'
      AND lease_until > sqlc.arg(claimed_at)
      AND occurrence_id <> sqlc.arg(excluded_occurrence_id)
);

-- name: InsertRunningRun :one
INSERT INTO candace_cron_runs (
    occurrence_id,
    job_name,
    scheduled_at,
    status,
    attempt,
    worker_id,
    lease_token,
    lease_until,
    started_at,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(occurrence_id),
    sqlc.arg(job_name),
    sqlc.arg(scheduled_at),
    'running',
    1,
    sqlc.arg(worker_id),
    sqlc.arg(lease_token),
    sqlc.arg(lease_until),
    sqlc.arg(claimed_at),
    sqlc.arg(claimed_at),
    sqlc.arg(claimed_at)
)
RETURNING *;

-- name: MarkExpiredRunSkipped :one
UPDATE candace_cron_runs
SET status = 'skipped',
    worker_id = NULL,
    lease_token = NULL,
    lease_until = NULL,
    finished_at = sqlc.arg(skipped_at),
    error_summary = NULL,
    skip_reason = sqlc.arg(skip_reason),
    updated_at = sqlc.arg(skipped_at)
WHERE occurrence_id = sqlc.arg(occurrence_id)
  AND status = 'running'
  AND lease_until <= sqlc.arg(skipped_at)
RETURNING *;

-- name: InsertSkippedRun :one
INSERT INTO candace_cron_runs (
    occurrence_id,
    job_name,
    scheduled_at,
    status,
    attempt,
    finished_at,
    skip_reason,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(occurrence_id),
    sqlc.arg(job_name),
    sqlc.arg(scheduled_at),
    'skipped',
    0,
    sqlc.arg(skipped_at),
    sqlc.arg(skip_reason),
    sqlc.arg(skipped_at),
    sqlc.arg(skipped_at)
)
RETURNING *;

-- name: AcquireExpiredRun :one
UPDATE candace_cron_runs
SET attempt = attempt + 1,
    worker_id = sqlc.arg(worker_id),
    lease_token = sqlc.arg(lease_token),
    lease_until = sqlc.arg(lease_until),
    started_at = sqlc.arg(claimed_at),
    finished_at = NULL,
    error_summary = NULL,
    skip_reason = NULL,
    updated_at = sqlc.arg(claimed_at)
WHERE occurrence_id = sqlc.arg(occurrence_id)
  AND status = 'running'
  AND lease_until <= sqlc.arg(claimed_at)
RETURNING *;

-- name: ListRecentRuns :many
SELECT *
FROM (
    SELECT *
    FROM candace_cron_runs
    ORDER BY scheduled_at DESC, occurrence_id DESC
    LIMIT sqlc.arg(row_limit)
) AS recent
ORDER BY scheduled_at, occurrence_id;

-- name: RenewRunLease :execrows
UPDATE candace_cron_runs
SET lease_until = sqlc.arg(lease_until),
    updated_at = sqlc.arg(renewed_at)
WHERE occurrence_id = sqlc.arg(occurrence_id)
  AND status = 'running'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_until > sqlc.arg(renewed_at);

-- name: FinishRun :execrows
UPDATE candace_cron_runs
SET status = sqlc.arg(status),
    worker_id = NULL,
    lease_token = NULL,
    lease_until = NULL,
    finished_at = sqlc.arg(finished_at),
    error_summary = sqlc.narg(error_summary),
    updated_at = sqlc.arg(finished_at)
WHERE occurrence_id = sqlc.arg(occurrence_id)
  AND status = 'running'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_until > sqlc.arg(finished_at);

-- name: AdvanceJobNextRun :execrows
UPDATE candace_cron_jobs
SET next_run_at = CASE
        WHEN next_run_at IS NULL THEN sqlc.arg(next_run_at)
        ELSE GREATEST(next_run_at, sqlc.arg(next_run_at))
    END,
    schedule_cursor_at = CASE
        WHEN schedule_cursor_at IS NULL THEN sqlc.arg(schedule_cursor_at)
        ELSE GREATEST(schedule_cursor_at, sqlc.arg(schedule_cursor_at))
    END,
    updated_at = sqlc.arg(updated_at)
WHERE job_name = sqlc.arg(job_name);
