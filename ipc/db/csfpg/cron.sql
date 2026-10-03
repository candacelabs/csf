-- Cron: the declared triggers and their occurrences; candace/services/cron
-- owns the rules. Every write that fences on a lease is one conditional
-- statement, so the store takes no row lock: a stale token or an expired
-- lease changes no row, and the command tag reports it. Every time is a
-- parameter the service read from its clock.

-- name: ListCronTriggers :many
SELECT * FROM csf_cron_triggers ORDER BY trigger_name;

-- name: GetCronTrigger :one
SELECT * FROM csf_cron_triggers WHERE trigger_name = sqlc.arg(trigger_name);

-- A declared trigger is inserted or brought back to its declaration, enabled.
-- name: UpsertCronTrigger :one
INSERT INTO csf_cron_triggers (
    trigger_name, schedule_kind, local_hour, local_minute, weekday, month_day,
    interval_nanoseconds, raw_expression, timezone, interval_anchor_at, next_run_at,
    catch_up_policy, overlap_policy, enabled, created_at, updated_at
) VALUES (
    sqlc.arg(trigger_name), sqlc.arg(schedule_kind), sqlc.narg(local_hour), sqlc.narg(local_minute),
    sqlc.narg(weekday), sqlc.narg(month_day), sqlc.narg(interval_nanoseconds), sqlc.narg(raw_expression),
    sqlc.arg(timezone), sqlc.narg(interval_anchor_at), sqlc.arg(next_run_at),
    sqlc.arg(catch_up_policy), sqlc.arg(overlap_policy), TRUE, sqlc.arg(updated_at), sqlc.arg(updated_at)
)
ON CONFLICT (trigger_name) DO UPDATE SET
    schedule_kind = EXCLUDED.schedule_kind,
    local_hour = EXCLUDED.local_hour,
    local_minute = EXCLUDED.local_minute,
    weekday = EXCLUDED.weekday,
    month_day = EXCLUDED.month_day,
    interval_nanoseconds = EXCLUDED.interval_nanoseconds,
    raw_expression = EXCLUDED.raw_expression,
    timezone = EXCLUDED.timezone,
    interval_anchor_at = EXCLUDED.interval_anchor_at,
    next_run_at = EXCLUDED.next_run_at,
    catch_up_policy = EXCLUDED.catch_up_policy,
    overlap_policy = EXCLUDED.overlap_policy,
    enabled = TRUE,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- A trigger no longer declared keeps its history and stops firing.
-- name: DisableCronTrigger :exec
UPDATE csf_cron_triggers
SET enabled = FALSE, updated_at = sqlc.arg(updated_at)
WHERE trigger_name = sqlc.arg(trigger_name) AND enabled;

-- The cursor only moves forward.
-- name: AdvanceCronTrigger :execrows
UPDATE csf_cron_triggers
SET next_run_at = CASE WHEN next_run_at < sqlc.arg(next_run_at) THEN sqlc.arg(next_run_at) ELSE next_run_at END,
    updated_at = sqlc.arg(updated_at)
WHERE trigger_name = sqlc.arg(trigger_name) AND enabled;

-- name: GetCronOccurrence :one
SELECT * FROM csf_cron_occurrences WHERE occurrence_id = sqlc.arg(occurrence_id);

-- Another occurrence of the same trigger holding a live lease, for the
-- overlap policy.
-- name: LiveCronOccurrence :one
SELECT occurrence_id FROM csf_cron_occurrences
WHERE trigger_name = sqlc.arg(trigger_name)
  AND status = 'running'
  AND lease_until > sqlc.arg(at)
  AND occurrence_id <> sqlc.arg(excluded_occurrence_id)
LIMIT 1;

-- name: InsertRunningCronOccurrence :one
INSERT INTO csf_cron_occurrences (
    occurrence_id, trigger_name, scheduled_at, status, attempt,
    lease_owner, lease_token, lease_until, started_at, created_at, updated_at
) VALUES (
    sqlc.arg(occurrence_id), sqlc.arg(trigger_name), sqlc.arg(scheduled_at), 'running', 1,
    sqlc.arg(lease_owner), sqlc.arg(lease_token), sqlc.arg(lease_until),
    sqlc.arg(claimed_at), sqlc.arg(claimed_at), sqlc.arg(claimed_at)
)
RETURNING *;

-- name: InsertSkippedCronOccurrence :one
INSERT INTO csf_cron_occurrences (
    occurrence_id, trigger_name, scheduled_at, status, attempt,
    finished_at, skip_reason, created_at, updated_at
) VALUES (
    sqlc.arg(occurrence_id), sqlc.arg(trigger_name), sqlc.arg(scheduled_at), 'skipped', 0,
    sqlc.arg(skipped_at), sqlc.arg(skip_reason), sqlc.arg(skipped_at), sqlc.arg(skipped_at)
)
RETURNING *;

-- A running occurrence whose lease expired is reclaimed as the next attempt.
-- name: AcquireExpiredCronOccurrence :one
UPDATE csf_cron_occurrences
SET attempt = attempt + 1,
    lease_owner = sqlc.arg(lease_owner),
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

-- name: SkipExpiredCronOccurrence :one
UPDATE csf_cron_occurrences
SET status = 'skipped',
    lease_owner = NULL,
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

-- name: RenewCronOccurrenceLease :execrows
UPDATE csf_cron_occurrences
SET lease_until = sqlc.arg(lease_until), updated_at = sqlc.arg(renewed_at)
WHERE occurrence_id = sqlc.arg(occurrence_id)
  AND status = 'running'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_until > sqlc.arg(renewed_at);

-- name: FinishCronOccurrence :execrows
UPDATE csf_cron_occurrences
SET status = sqlc.arg(status),
    lease_owner = NULL,
    lease_token = NULL,
    lease_until = NULL,
    finished_at = sqlc.arg(finished_at),
    error_summary = sqlc.narg(error_summary),
    updated_at = sqlc.arg(finished_at)
WHERE occurrence_id = sqlc.arg(occurrence_id)
  AND status = 'running'
  AND lease_token = sqlc.arg(lease_token)
  AND lease_until > sqlc.arg(finished_at);

-- Abandoned leases of enabled triggers, oldest expiry first.
-- name: ListExpiredCronOccurrences :many
SELECT occurrence.*
FROM csf_cron_occurrences AS occurrence
JOIN csf_cron_triggers AS declaration ON declaration.trigger_name = occurrence.trigger_name
WHERE declaration.enabled
  AND occurrence.status = 'running'
  AND occurrence.lease_until <= sqlc.arg(expired_at)
ORDER BY occurrence.lease_until, occurrence.scheduled_at, occurrence.occurrence_id
LIMIT sqlc.arg(row_limit);

-- The newest occurrences, newest first; the caller reverses them.
-- name: ListRecentCronOccurrences :many
SELECT * FROM csf_cron_occurrences
ORDER BY scheduled_at DESC, occurrence_id DESC
LIMIT sqlc.arg(row_limit);
