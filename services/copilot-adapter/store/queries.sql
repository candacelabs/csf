-- name: CreateSession :one
INSERT INTO sessions (
    id,
    worktree_id,
    display_name,
    model,
    agent_id,
    working_directory,
    system_instructions,
    status,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(worktree_id),
    sqlc.arg(display_name),
    sqlc.arg(model),
    sqlc.arg(agent_id),
    sqlc.arg(working_directory),
    sqlc.arg(system_instructions),
    sqlc.arg(status),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
)
RETURNING *;

-- name: ClaimSessionCreation :one
INSERT INTO session_creations (
    idempotency_key,
    session_id,
    model,
    agent_id,
    repository_id,
    worktree_mode,
    worktree_id,
    base_ref,
    display_name,
    system_instructions,
    created_at
) VALUES (
    sqlc.arg(idempotency_key),
    sqlc.arg(session_id),
    sqlc.arg(model),
    sqlc.arg(agent_id),
    sqlc.arg(repository_id),
    sqlc.arg(worktree_mode),
    sqlc.narg(worktree_id),
    sqlc.narg(base_ref),
    sqlc.narg(display_name),
    sqlc.narg(system_instructions),
    sqlc.arg(created_at)
)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetSessionCreation :one
SELECT *
FROM session_creations
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: GetSessionCreationBySessionID :one
SELECT *
FROM session_creations
WHERE session_id = sqlc.arg(session_id);

-- name: ListIncompleteSessionCreations :many
SELECT *
FROM session_creations
WHERE completed_at IS NULL
ORDER BY created_at ASC, idempotency_key ASC;

-- name: BeginSessionCreationSDKAttempt :one
UPDATE session_creations
SET sdk_create_attempt_id = sqlc.arg(sdk_create_attempt_id)
WHERE idempotency_key = sqlc.arg(idempotency_key)
  AND session_id = sqlc.arg(session_id)
  AND sdk_create_attempt_id IS NULL
RETURNING *;

-- name: CompleteSessionCreation :one
UPDATE session_creations
SET completed_at = COALESCE(completed_at, sqlc.arg(completed_at))
WHERE idempotency_key = sqlc.arg(idempotency_key)
  AND session_id = sqlc.arg(session_id)
  AND sdk_create_attempt_id = sqlc.arg(sdk_create_attempt_id)
RETURNING *;

-- name: CompleteQuarantinedSessionCreation :exec
UPDATE session_creations
SET completed_at = COALESCE(completed_at, sqlc.arg(completed_at))
WHERE session_id = sqlc.arg(session_id);

-- name: EnsureSessionCounter :exec
INSERT INTO session_counters (
    session_id,
    last_transcript_seq,
    last_event_seq
) VALUES (
    sqlc.arg(session_id),
    0,
    0
)
ON CONFLICT (session_id) DO NOTHING;

-- name: AllocateTranscriptSeq :one
UPDATE session_counters
SET last_transcript_seq = last_transcript_seq + 1
WHERE session_id = sqlc.arg(session_id)
RETURNING last_transcript_seq AS seq;

-- name: AllocateSessionEventSeq :one
UPDATE session_counters
SET last_event_seq = last_event_seq + 1
WHERE session_id = sqlc.arg(session_id)
RETURNING last_event_seq AS seq;

-- name: CreateWorktree :one
INSERT INTO worktrees (
    id,
    repository_id,
    repository_root,
    path,
    base_ref,
    managed,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(repository_id),
    sqlc.arg(repository_root),
    sqlc.arg(path),
    sqlc.arg(base_ref),
    sqlc.arg(managed),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
)
ON CONFLICT (path) DO UPDATE
SET updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: GetWorktree :one
SELECT *
FROM worktrees
WHERE id = sqlc.arg(id);

-- name: GetWorktreeByPath :one
SELECT *
FROM worktrees
WHERE path = sqlc.arg(path);

-- name: AdoptLegacyWorktree :one
UPDATE worktrees
SET repository_id = sqlc.arg(repository_id),
    repository_root = sqlc.arg(repository_root),
    base_ref = sqlc.arg(base_ref),
    managed = sqlc.arg(managed),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND repository_id = 'legacy'
RETURNING *;

-- name: AdoptLegacyWorktreeSessions :exec
UPDATE sessions
SET working_directory = sqlc.arg(working_directory),
    updated_at = sqlc.arg(updated_at)
WHERE worktree_id = sqlc.arg(worktree_id);

-- name: QuarantineWorktreeSessions :many
UPDATE sessions
SET status = 'failed',
    ended_at = sqlc.arg(ended_at),
    updated_at = sqlc.arg(updated_at)
WHERE worktree_id = sqlc.arg(worktree_id)
  AND status NOT IN ('ended', 'failed')
RETURNING *;

-- name: ListWorktrees :many
SELECT worktrees.*
FROM worktrees
LEFT JOIN sessions ON sessions.worktree_id = worktrees.id
GROUP BY worktrees.id
ORDER BY CASE
           WHEN MAX(sessions.updated_at) IS NOT NULL AND MAX(sessions.updated_at) > worktrees.updated_at
             THEN MAX(sessions.updated_at)
           ELSE worktrees.updated_at
         END DESC,
         worktrees.id DESC;

-- name: CountWorktreeSessions :one
SELECT COUNT(*) AS session_count
FROM sessions
WHERE worktree_id = sqlc.arg(worktree_id);

-- name: ListWorktreeSessions :many
SELECT *
FROM sessions
WHERE worktree_id = sqlc.arg(worktree_id)
ORDER BY id ASC;

-- name: TouchWorktree :one
UPDATE worktrees
SET updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetSession :one
SELECT *
FROM sessions
WHERE id = sqlc.arg(id);

-- name: LockSession :one
UPDATE sessions
SET updated_at = updated_at
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListSessions :many
SELECT *
FROM sessions
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL
       OR created_at < sqlc.narg(cursor_created_at)::timestamptz
       OR (created_at = sqlc.narg(cursor_created_at)::timestamptz
           AND id < sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: UpdateSessionMetadata :one
UPDATE sessions
SET model = COALESCE(sqlc.narg(model)::text, model),
    display_name = COALESCE(sqlc.narg(display_name)::text, display_name),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status NOT IN ('ended', 'failed')
RETURNING *;

-- name: UpdateSessionStatus :one
UPDATE sessions
SET status = sqlc.arg(status),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status NOT IN ('ended', 'failed')
RETURNING *;

-- name: CompleteStartingSession :one
UPDATE sessions
SET status = 'idle',
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status = 'starting'
RETURNING *;

-- name: FailStartingSession :one
UPDATE sessions
SET status = 'failed',
    ended_at = sqlc.arg(ended_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND status = 'starting'
RETURNING *;

-- name: MarkSessionEnded :one
UPDATE sessions
SET status = sqlc.arg(status),
    ended_at = sqlc.arg(ended_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: FinalizeSessionTurns :many
UPDATE turns
SET status = 'aborted',
    completed_at = sqlc.arg(completed_at)
WHERE session_id = sqlc.arg(session_id)
  AND status IN ('queued', 'running')
RETURNING *;

-- name: AbandonSessionRequests :many
UPDATE pending_requests
SET status = 'denied',
    decision = 'deny',
    delivery_status = 'abandoned',
    resolved_at = sqlc.arg(resolved_at)
WHERE session_id = sqlc.arg(session_id)
  AND status = 'pending'
RETURNING *;

-- name: AbandonTurnRequests :many
UPDATE pending_requests
SET status = 'denied',
    decision = 'deny',
    delivery_status = 'abandoned',
    resolved_at = sqlc.arg(resolved_at)
WHERE session_id = sqlc.arg(session_id)
  AND turn_id = sqlc.arg(turn_id)
  AND status = 'pending'
  AND delivery_status IN ('pending', 'delivering')
RETURNING *;

-- name: ListAbandonedTurnRequests :many
SELECT *
FROM pending_requests
WHERE session_id = sqlc.arg(session_id)
  AND turn_id = sqlc.arg(turn_id)
  AND status = 'denied'
  AND delivery_status = 'abandoned'
ORDER BY created_at ASC, id ASC;

-- name: FinalizeSessionSubagents :many
UPDATE subagents
SET status = 'failed',
    updated_at = sqlc.arg(updated_at),
    completed_at = sqlc.arg(completed_at)
WHERE session_id = sqlc.arg(session_id)
  AND status = 'active'
RETURNING *;

-- name: PauseSessionSchedules :execrows
UPDATE chat_schedules
SET status = 'paused',
    updated_at = sqlc.arg(updated_at)
WHERE session_id = sqlc.arg(session_id)
  AND status = 'active'
  AND deleted_at IS NULL;

-- name: PauseWorktreeSchedules :execrows
UPDATE chat_schedules
SET status = 'paused',
    updated_at = sqlc.arg(updated_at)
WHERE session_id IN (
    SELECT id
    FROM sessions
    WHERE worktree_id = sqlc.arg(worktree_id)
)
  AND status = 'active'
  AND deleted_at IS NULL;

-- name: TouchSessionLastTurn :one
UPDATE sessions
SET last_turn_at = sqlc.arg(last_turn_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CountSessionTurns :one
SELECT COUNT(*) AS turn_count
FROM turns
WHERE session_id = sqlc.arg(session_id);

-- name: CountActiveSessionTurns :one
SELECT COUNT(*) AS turn_count
FROM turns
WHERE session_id = sqlc.arg(session_id)
  AND status IN ('queued', 'running');

-- name: ListActiveSessionTurns :many
SELECT *
FROM turns
WHERE session_id = sqlc.arg(session_id)
  AND status IN ('queued', 'running')
ORDER BY created_at ASC, id ASC;

-- name: ListResumableSessions :many
SELECT *
FROM sessions
WHERE status NOT IN ('ended', 'failed')
ORDER BY created_at ASC, id ASC;

-- name: ClaimBridgeEventProjection :one
INSERT INTO bridge_event_receipts (
    session_id,
    event_id,
    projected_at
) VALUES (
    sqlc.arg(session_id),
    sqlc.arg(event_id),
    sqlc.arg(projected_at)
)
ON CONFLICT (session_id, event_id) DO NOTHING
RETURNING event_id;

-- name: CreateTurn :one
INSERT INTO turns (
    id,
    session_id,
    status,
    prompt_text,
    prompt_mode,
    author,
    created_at,
    delivery_status
) VALUES (
    sqlc.arg(id),
    sqlc.arg(session_id),
    sqlc.arg(status),
    sqlc.arg(prompt_text),
    sqlc.arg(prompt_mode),
    sqlc.arg(author),
    sqlc.arg(created_at),
    sqlc.arg(delivery_status)
)
RETURNING *;

-- name: ClaimPromptSubmission :one
INSERT INTO prompt_submissions (
    session_id,
    idempotency_key,
    turn_id
) VALUES (
    sqlc.arg(session_id),
    sqlc.arg(idempotency_key),
    sqlc.arg(turn_id)
)
ON CONFLICT (session_id, idempotency_key) DO NOTHING
RETURNING turn_id;

-- name: GetTurnByPromptIdempotencyKey :one
SELECT turns.*
FROM turns
JOIN prompt_submissions
  ON prompt_submissions.turn_id = turns.id
WHERE prompt_submissions.session_id = sqlc.arg(session_id)
  AND prompt_submissions.idempotency_key = sqlc.arg(idempotency_key);

-- name: ClaimScheduledTurnOccurrence :one
INSERT INTO scheduled_turn_occurrences (
    occurrence_id,
    turn_id
) VALUES (
    sqlc.arg(schedule_occurrence_id),
    sqlc.arg(turn_id)
)
ON CONFLICT (occurrence_id) DO NOTHING
RETURNING turn_id;

-- name: GetTurnByScheduleOccurrence :one
SELECT turns.*
FROM turns
JOIN scheduled_turn_occurrences
  ON scheduled_turn_occurrences.turn_id = turns.id
WHERE scheduled_turn_occurrences.occurrence_id = sqlc.arg(schedule_occurrence_id);

-- name: GetTurn :one
SELECT *
FROM turns
WHERE id = sqlc.arg(id);

-- name: ClaimTurnAbortSubmission :one
INSERT INTO turn_abort_submissions (
    session_id,
    idempotency_key,
    turn_id
) VALUES (
    sqlc.arg(session_id),
    sqlc.arg(idempotency_key),
    sqlc.arg(turn_id)
)
ON CONFLICT (session_id, idempotency_key) DO NOTHING
RETURNING turn_id;

-- name: GetTurnByAbortIdempotencyKey :one
SELECT turns.*
FROM turns
JOIN turn_abort_submissions
  ON turn_abort_submissions.turn_id = turns.id
WHERE turn_abort_submissions.session_id = sqlc.arg(session_id)
  AND turn_abort_submissions.idempotency_key = sqlc.arg(idempotency_key);

-- name: MarkTurnDeliveryAccepted :one
UPDATE turns
SET delivery_status = 'accepted'
WHERE id = sqlc.arg(id)
  AND delivery_status IN ('pending', 'unknown')
RETURNING *;

-- name: MarkTurnDeliveryUnknown :one
UPDATE turns
SET delivery_status = 'unknown'
WHERE id = sqlc.arg(id)
  AND delivery_status = 'pending'
RETURNING *;

-- name: MarkTurnDeliveryFailed :one
UPDATE turns
SET delivery_status = 'failed',
    status = 'failed',
    completed_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(id)
  AND delivery_status IN ('pending', 'unknown')
  AND status IN ('queued', 'running')
RETURNING *;

-- name: UpdateTurnStatus :one
UPDATE turns
SET status = sqlc.arg(status),
    completed_at = sqlc.narg(completed_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: MarkTurnRunning :one
UPDATE turns
SET status = 'running',
    delivery_status = 'accepted'
WHERE id = sqlc.arg(id)
  AND status = 'queued'
RETURNING *;

-- name: FinalizeTurn :one
UPDATE turns
SET status = sqlc.arg(status),
    completed_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(id)
  AND status IN ('queued', 'running')
RETURNING *;

-- name: GetRunningTurn :one
SELECT *
FROM turns
WHERE session_id = sqlc.arg(session_id)
  AND status = 'running'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: InsertTranscriptItem :one
INSERT INTO transcript_items (
    session_id,
    seq,
    turn_id,
    kind,
    occurred_at,
    author,
    tool_name,
    tool_call_id,
    body
) VALUES (
    sqlc.arg(session_id),
    sqlc.arg(seq),
    sqlc.narg(turn_id),
    sqlc.arg(kind),
    sqlc.arg(occurred_at),
    sqlc.arg(author),
    sqlc.arg(tool_name),
    sqlc.arg(tool_call_id),
    sqlc.arg(body)
)
RETURNING *;

-- name: ListTranscriptItemsAfterSeq :many
SELECT *
FROM transcript_items
WHERE session_id = sqlc.arg(session_id)
  AND seq > sqlc.arg(after_seq)
ORDER BY seq ASC
LIMIT sqlc.arg(row_limit);

-- name: GetTranscriptItem :one
SELECT *
FROM transcript_items
WHERE session_id = sqlc.arg(session_id)
  AND seq = sqlc.arg(seq);

-- name: InsertSessionEvent :one
INSERT INTO session_events (
    session_id,
    seq,
    kind,
    occurred_at,
    transcript_seq,
    turn_id,
    request_id,
    subagent_id,
    subagent_activity_seq,
    delta_text
) VALUES (
    sqlc.arg(session_id),
    sqlc.arg(seq),
    sqlc.arg(kind),
    sqlc.arg(occurred_at),
    sqlc.narg(transcript_seq),
    sqlc.narg(turn_id),
    sqlc.narg(request_id),
    sqlc.narg(subagent_id),
    sqlc.narg(subagent_activity_seq),
    sqlc.arg(delta_text)
)
RETURNING *;

-- name: ListSessionEventsAfterSeq :many
SELECT *
FROM session_events
WHERE session_id = sqlc.arg(session_id)
  AND seq > sqlc.arg(after_seq)
ORDER BY seq ASC
LIMIT sqlc.arg(row_limit);

-- name: SnapshotSessionEvent :one
INSERT INTO session_event_versions (
    session_id,
    event_seq,
    id,
    worktree_id,
    display_name,
    model,
    working_directory,
    status,
    created_at,
    updated_at,
    last_turn_at,
    turn_count
)
SELECT
    sessions.id,
    sqlc.arg(event_seq),
    sessions.id,
    sessions.worktree_id,
    sessions.display_name,
    sessions.model,
    sessions.working_directory,
    sessions.status,
    sessions.created_at,
    sessions.updated_at,
    sessions.last_turn_at,
    (SELECT COUNT(*) FROM turns WHERE turns.session_id = sessions.id)
FROM sessions
WHERE sessions.id = sqlc.arg(session_id)
RETURNING *;

-- name: GetSessionEventVersion :one
SELECT *
FROM session_event_versions
WHERE session_id = sqlc.arg(session_id)
  AND event_seq = sqlc.arg(event_seq);

-- name: SnapshotTurnEvent :one
INSERT INTO turn_event_versions (
    session_id,
    event_seq,
    id,
    status,
    prompt_text,
    prompt_mode,
    author,
    created_at,
    completed_at,
    started_at
)
SELECT
    turns.session_id,
    sqlc.arg(event_seq),
    turns.id,
    turns.status,
    turns.prompt_text,
    turns.prompt_mode,
    turns.author,
    turns.created_at,
    turns.completed_at,
    turns.started_at
FROM turns
WHERE turns.id = sqlc.arg(turn_id)
RETURNING *;

-- name: GetTurnEventVersion :one
SELECT *
FROM turn_event_versions
WHERE session_id = sqlc.arg(session_id)
  AND event_seq = sqlc.arg(event_seq);

-- name: SnapshotRequestEvent :one
INSERT INTO request_event_versions (
    session_id,
    event_seq,
    id,
    turn_id,
    kind,
    status,
    prompt,
    tool_name,
    created_at,
    resolved_at
)
SELECT
    pending_requests.session_id,
    sqlc.arg(event_seq),
    pending_requests.id,
    pending_requests.turn_id,
    pending_requests.kind,
    pending_requests.status,
    pending_requests.prompt,
    pending_requests.tool_name,
    pending_requests.created_at,
    pending_requests.resolved_at
FROM pending_requests
WHERE pending_requests.id = sqlc.arg(request_id)
RETURNING *;

-- name: GetRequestEventVersion :one
SELECT *
FROM request_event_versions
WHERE session_id = sqlc.arg(session_id)
  AND event_seq = sqlc.arg(event_seq);

-- name: SnapshotSubagentEvent :one
INSERT INTO subagent_event_versions (
    session_id,
    event_seq,
    id,
    turn_id,
    display_name,
    status,
    summary,
    activity_count,
    started_at,
    updated_at,
    completed_at
)
SELECT
    subagents.session_id,
    sqlc.arg(event_seq),
    subagents.id,
    subagents.turn_id,
    subagents.display_name,
    subagents.status,
    subagents.summary,
    subagents.activity_count,
    subagents.started_at,
    subagents.updated_at,
    subagents.completed_at
FROM subagents
WHERE subagents.session_id = sqlc.arg(session_id)
  AND subagents.id = sqlc.arg(subagent_id)
RETURNING *;

-- name: GetSubagentEventVersion :one
SELECT *
FROM subagent_event_versions
WHERE session_id = sqlc.arg(session_id)
  AND event_seq = sqlc.arg(event_seq);

-- name: InsertSessionRequest :one
INSERT INTO pending_requests (
    id,
    session_id,
    turn_id,
    kind,
    status,
    prompt,
    tool_name,
    decision,
    answer,
    created_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(session_id),
    sqlc.narg(turn_id),
    sqlc.arg(kind),
    sqlc.arg(status),
    sqlc.arg(prompt),
    sqlc.arg(tool_name),
    sqlc.arg(decision),
    sqlc.arg(answer),
    sqlc.arg(created_at)
)
RETURNING *;

-- name: GetSessionRequest :one
SELECT *
FROM pending_requests
WHERE id = sqlc.arg(id);

-- name: ListSessionRequests :many
SELECT *
FROM pending_requests
WHERE session_id = sqlc.arg(session_id)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY created_at ASC, id ASC;

-- name: ListPendingPermissionSessionRequests :many
SELECT *
FROM pending_requests
WHERE session_id = sqlc.arg(session_id)
  AND kind = 'permission'
  AND status = 'pending'
ORDER BY created_at ASC, id ASC;

-- name: PrepareSessionRequestResolution :one
UPDATE pending_requests
SET decision = sqlc.arg(decision),
    answer = sqlc.arg(answer),
    delivery_status = 'delivering'
WHERE id = sqlc.arg(id)
  AND status = 'pending'
  AND (
    delivery_status = 'pending'
    OR (
      delivery_status = 'delivering'
      AND decision = sqlc.arg(decision)
      AND answer = sqlc.arg(answer)
    )
  )
RETURNING *;

-- name: CompleteSessionRequestResolution :one
UPDATE pending_requests
SET status = sqlc.arg(status),
    delivery_status = 'delivered',
    resolved_at = sqlc.arg(resolved_at)
WHERE id = sqlc.arg(id)
  AND status = 'pending'
  AND delivery_status = 'delivering'
  AND decision = sqlc.arg(decision)
  AND answer = sqlc.arg(answer)
RETURNING *;

-- name: CompleteExternalSessionRequestResolution :one
UPDATE pending_requests
SET status = sqlc.arg(status),
    decision = sqlc.arg(decision),
    answer = '',
    delivery_status = 'delivered',
    resolved_at = sqlc.arg(resolved_at)
WHERE id = sqlc.arg(id)
  AND status = 'pending'
RETURNING *;

-- name: CreateChatSchedule :one
INSERT INTO chat_schedules (
    id,
    session_id,
    display_name,
    prompt,
    cron_expression,
    timezone,
    status,
    created_at,
    updated_at
) VALUES (
    sqlc.arg(id),
    sqlc.arg(session_id),
    sqlc.arg(display_name),
    sqlc.arg(prompt),
    sqlc.arg(cron_expression),
    sqlc.arg(timezone),
    sqlc.arg(status),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
)
RETURNING *;

-- name: ClaimChatScheduleCreation :one
INSERT INTO chat_schedule_creations (
    idempotency_key,
    schedule_id,
    session_id,
    display_name,
    prompt,
    cron_expression,
    timezone,
    created_at
) VALUES (
    sqlc.arg(idempotency_key),
    sqlc.arg(schedule_id),
    sqlc.arg(session_id),
    sqlc.arg(display_name),
    sqlc.arg(prompt),
    sqlc.arg(cron_expression),
    sqlc.arg(timezone),
    sqlc.arg(created_at)
)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetChatScheduleCreation :one
SELECT *
FROM chat_schedule_creations
WHERE idempotency_key = sqlc.arg(idempotency_key);

-- name: GetChatScheduleByCreationKey :one
SELECT chat_schedules.*
FROM chat_schedules
JOIN chat_schedule_creations
  ON chat_schedule_creations.schedule_id = chat_schedules.id
WHERE chat_schedule_creations.idempotency_key = sqlc.arg(idempotency_key);

-- name: GetChatSchedule :one
SELECT *
FROM chat_schedules
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL;

-- name: ListChatSchedules :many
SELECT *
FROM chat_schedules
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateChatSchedule :one
UPDATE chat_schedules
SET display_name = sqlc.arg(display_name),
    prompt = sqlc.arg(prompt),
    cron_expression = sqlc.arg(cron_expression),
    timezone = sqlc.arg(timezone),
    status = sqlc.arg(status),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING *;

-- name: DeleteChatSchedule :one
UPDATE chat_schedules
SET deleted_at = sqlc.arg(deleted_at),
    updated_at = sqlc.arg(deleted_at),
    status = 'paused'
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING id;

-- name: UpsertSubagent :one
INSERT INTO subagents (
    session_id,
    id,
    turn_id,
    display_name,
    status,
    summary,
    activity_count,
    started_at,
    updated_at,
    completed_at
) VALUES (
    sqlc.arg(session_id),
    sqlc.arg(id),
    sqlc.narg(turn_id),
    COALESCE(NULLIF(sqlc.arg(display_name), ''), sqlc.arg(id)),
    sqlc.arg(status),
    sqlc.arg(summary),
    sqlc.arg(activity_count),
    sqlc.arg(started_at),
    sqlc.arg(updated_at),
    sqlc.narg(completed_at)
)
ON CONFLICT (session_id, id) DO UPDATE
SET turn_id = COALESCE(EXCLUDED.turn_id, subagents.turn_id),
    display_name = CASE
      WHEN sqlc.arg(display_name) = '' THEN subagents.display_name
      ELSE EXCLUDED.display_name
    END,
    status = EXCLUDED.status,
    summary = CASE
      WHEN EXCLUDED.summary IS NULL THEN subagents.summary
      ELSE EXCLUDED.summary
    END,
    activity_count = CASE
      WHEN EXCLUDED.activity_count > subagents.activity_count THEN EXCLUDED.activity_count
      ELSE subagents.activity_count
    END,
    updated_at = EXCLUDED.updated_at,
    completed_at = EXCLUDED.completed_at
RETURNING *;

-- name: GetSubagent :one
SELECT *
FROM subagents
WHERE session_id = sqlc.arg(session_id)
  AND id = sqlc.arg(id);

-- name: ListSubagents :many
SELECT *
FROM subagents
WHERE session_id = sqlc.arg(session_id)
ORDER BY CASE WHEN status = 'active' THEN 0 ELSE 1 END,
         updated_at DESC,
         id ASC;

-- name: AllocateSubagentActivitySeq :one
UPDATE subagents
SET activity_count = activity_count + 1,
    updated_at = sqlc.arg(updated_at)
WHERE session_id = sqlc.arg(session_id)
  AND id = sqlc.arg(subagent_id)
RETURNING activity_count AS seq;

-- name: InsertSubagentActivity :one
INSERT INTO subagent_activities (
    session_id,
    subagent_id,
    seq,
    kind,
    occurred_at,
    body,
    tool_name,
    tool_call_id
) VALUES (
    sqlc.arg(session_id),
    sqlc.arg(subagent_id),
    sqlc.arg(seq),
    sqlc.arg(kind),
    sqlc.arg(occurred_at),
    sqlc.arg(body),
    sqlc.arg(tool_name),
    sqlc.arg(tool_call_id)
)
RETURNING *;

-- name: ListSubagentActivities :many
SELECT *
FROM subagent_activities
WHERE session_id = sqlc.arg(session_id)
  AND subagent_id = sqlc.arg(subagent_id)
  AND seq > sqlc.arg(after_seq)
ORDER BY seq ASC
LIMIT sqlc.arg(row_limit);

-- name: GetSubagentActivity :one
SELECT *
FROM subagent_activities
WHERE session_id = sqlc.arg(session_id)
  AND subagent_id = sqlc.arg(subagent_id)
  AND seq = sqlc.arg(seq);

-- Preserve the first real start observation; never substitute enqueue time.
-- name: RecordTurnStarted :one
UPDATE turns
SET started_at = COALESCE(started_at, sqlc.arg(started_at)::TIMESTAMPTZ)
WHERE id = sqlc.arg(turn_id)::UUID
  AND (completed_at IS NULL OR COALESCE(started_at, sqlc.arg(started_at)::TIMESTAMPTZ) <= completed_at)
RETURNING *;

-- Exact provider identity retries return the original row; conflicting facts
-- return no row. The caller validates nullable turn ownership in this session.
-- name: InsertProviderUsageEvent :one
INSERT INTO provider_usage_events (session_id, event_id, turn_id, kind, occurred_at, model, api_call_id, provider_call_id, service_request_id, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, api_duration_ms, billing_multiplier, premium_requests, nano_aiu, provider_event)
VALUES (sqlc.arg(session_id)::uuid, sqlc.arg(event_id)::text, sqlc.narg(turn_id)::uuid, sqlc.arg(kind)::text, sqlc.arg(occurred_at)::timestamptz, sqlc.narg(model)::text, sqlc.narg(api_call_id)::text, sqlc.narg(provider_call_id)::text, sqlc.narg(service_request_id)::text, sqlc.narg(input_tokens)::bigint, sqlc.narg(output_tokens)::bigint, sqlc.narg(cache_read_tokens)::bigint, sqlc.narg(cache_write_tokens)::bigint, sqlc.narg(reasoning_tokens)::bigint, sqlc.narg(api_duration_ms)::bigint, sqlc.narg(billing_multiplier)::double precision, sqlc.narg(premium_requests)::double precision, sqlc.narg(nano_aiu)::double precision, sqlc.narg(provider_event)::jsonb)
ON CONFLICT (session_id, event_id) DO UPDATE SET event_id = EXCLUDED.event_id
WHERE TRUE
  AND provider_usage_events.turn_id IS NOT DISTINCT FROM EXCLUDED.turn_id
  AND provider_usage_events.kind IS NOT DISTINCT FROM EXCLUDED.kind
  AND provider_usage_events.occurred_at IS NOT DISTINCT FROM EXCLUDED.occurred_at
  AND provider_usage_events.model IS NOT DISTINCT FROM EXCLUDED.model
  AND provider_usage_events.api_call_id IS NOT DISTINCT FROM EXCLUDED.api_call_id
  AND provider_usage_events.provider_call_id IS NOT DISTINCT FROM EXCLUDED.provider_call_id
  AND provider_usage_events.service_request_id IS NOT DISTINCT FROM EXCLUDED.service_request_id
  AND provider_usage_events.input_tokens IS NOT DISTINCT FROM EXCLUDED.input_tokens
  AND provider_usage_events.output_tokens IS NOT DISTINCT FROM EXCLUDED.output_tokens
  AND provider_usage_events.cache_read_tokens IS NOT DISTINCT FROM EXCLUDED.cache_read_tokens
  AND provider_usage_events.cache_write_tokens IS NOT DISTINCT FROM EXCLUDED.cache_write_tokens
  AND provider_usage_events.reasoning_tokens IS NOT DISTINCT FROM EXCLUDED.reasoning_tokens
  AND provider_usage_events.api_duration_ms IS NOT DISTINCT FROM EXCLUDED.api_duration_ms
  AND provider_usage_events.billing_multiplier IS NOT DISTINCT FROM EXCLUDED.billing_multiplier
  AND provider_usage_events.premium_requests IS NOT DISTINCT FROM EXCLUDED.premium_requests
  AND provider_usage_events.nano_aiu IS NOT DISTINCT FROM EXCLUDED.nano_aiu
  AND provider_usage_events.provider_event IS NOT DISTINCT FROM EXCLUDED.provider_event
RETURNING *;

-- name: GetProviderUsageEvent :one
SELECT * FROM provider_usage_events
WHERE session_id = sqlc.arg(session_id) AND event_id = sqlc.arg(event_id);

-- Sums are model-call facts only, never cumulative checkpoint double-counting.
-- NULL sums mean no supplied value, not a measured zero. Premium usage is the
-- latest non-NULL session checkpoint, not a sum and not a dollar conversion.
-- name: ListSessionTelemetry :many
WITH session_page AS (
    SELECT * FROM sessions AS session
    WHERE (sqlc.narg(cursor_created_at)::TIMESTAMPTZ IS NULL
           OR session.created_at < sqlc.narg(cursor_created_at)::TIMESTAMPTZ
           OR (session.created_at = sqlc.narg(cursor_created_at)::TIMESTAMPTZ
               AND session.id < sqlc.narg(cursor_id)::UUID))
    ORDER BY session.created_at DESC, session.id DESC
    LIMIT LEAST(GREATEST(sqlc.arg(row_limit)::INT, 1), 200)
), telemetry AS (
SELECT session.id AS session_id, session.display_name, session.model,
       session.status, session.created_at, session.updated_at,
       turns_summary.turn_count, turns_summary.known_started_turn_count,
       turns_summary.known_completed_turn_count, turns_summary.duration_known_turn_count,
       turns_summary.duration_seconds,
       latest_turn.id AS latest_turn_id, latest_turn.started_at AS latest_turn_started_at,
       latest_turn.completed_at AS latest_turn_completed_at, latest_turn.status AS latest_turn_status,
       usage.observed_model_call_count,
       usage.input_tokens, usage.output_tokens, usage.cache_read_tokens, usage.cache_write_tokens, usage.reasoning_tokens, usage.api_duration_ms,
       premium.premium_requests, usage.latest_usage_at, usage.uncorrelated_usage_count
FROM session_page AS session
LEFT JOIN LATERAL (
    SELECT count(*)::BIGINT AS turn_count,
           count(*) FILTER (WHERE started_at IS NOT NULL)::BIGINT AS known_started_turn_count,
           count(*) FILTER (WHERE completed_at IS NOT NULL)::BIGINT AS known_completed_turn_count,
           count(*) FILTER (WHERE started_at IS NOT NULL AND completed_at >= started_at)::BIGINT AS duration_known_turn_count,
           sum(extract(epoch FROM completed_at - started_at))
               FILTER (WHERE started_at IS NOT NULL AND completed_at >= started_at)::DOUBLE PRECISION AS duration_seconds
    FROM turns WHERE session_id = session.id
) AS turns_summary ON TRUE
LEFT JOIN LATERAL (
    SELECT id, started_at, completed_at, status FROM turns
    WHERE session_id = session.id
    ORDER BY created_at DESC, id DESC LIMIT 1
) AS latest_turn ON TRUE
LEFT JOIN LATERAL (
    SELECT count(*) FILTER (WHERE kind = 'modelCall')::BIGINT AS observed_model_call_count,
           sum(input_tokens) FILTER (WHERE kind = 'modelCall')::BIGINT AS input_tokens,
           sum(output_tokens) FILTER (WHERE kind = 'modelCall')::BIGINT AS output_tokens,
           sum(cache_read_tokens) FILTER (WHERE kind = 'modelCall')::BIGINT AS cache_read_tokens,
           sum(cache_write_tokens) FILTER (WHERE kind = 'modelCall')::BIGINT AS cache_write_tokens,
           sum(reasoning_tokens) FILTER (WHERE kind = 'modelCall')::BIGINT AS reasoning_tokens,
           sum(api_duration_ms) FILTER (WHERE kind = 'modelCall')::BIGINT AS api_duration_ms,
           max(occurred_at)::TIMESTAMPTZ AS latest_usage_at,
           count(*) FILTER (WHERE kind = 'modelCall' AND turn_id IS NULL)::BIGINT AS uncorrelated_usage_count
    FROM provider_usage_events WHERE session_id = session.id
) AS usage ON TRUE
LEFT JOIN LATERAL (
    SELECT premium_requests FROM provider_usage_events
    WHERE session_id = session.id AND kind = 'sessionCheckpoint'
      AND premium_requests IS NOT NULL
    ORDER BY occurred_at DESC, event_id DESC LIMIT 1
) AS premium ON TRUE

)
-- The empty arm declares nullable output types for SQLC 1.31; PostgreSQL
-- eliminates it. Unknown observations stay SQL NULL in the real arm.
SELECT session.id AS session_id,
       session.display_name AS display_name,
       session.model AS model,
       session.status AS status,
       session.created_at AS created_at,
       session.updated_at AS updated_at,
       0::BIGINT AS turn_count,
       0::BIGINT AS known_started_turn_count,
       0::BIGINT AS known_completed_turn_count,
       0::BIGINT AS duration_known_turn_count,
       NULL::DOUBLE PRECISION AS duration_seconds,
       NULL::UUID AS latest_turn_id,
       NULL::TIMESTAMPTZ AS latest_turn_started_at,
       NULL::TIMESTAMPTZ AS latest_turn_completed_at,
       NULL::TEXT AS latest_turn_status,
       0::BIGINT AS observed_model_call_count,
       NULL::BIGINT AS input_tokens,
       NULL::BIGINT AS output_tokens,
       NULL::BIGINT AS cache_read_tokens,
       NULL::BIGINT AS cache_write_tokens,
       NULL::BIGINT AS reasoning_tokens,
       NULL::BIGINT AS api_duration_ms,
       NULL::DOUBLE PRECISION AS premium_requests,
       NULL::TIMESTAMPTZ AS latest_usage_at,
       0::BIGINT AS uncorrelated_usage_count
FROM sessions AS session WHERE FALSE
UNION ALL
SELECT * FROM telemetry
ORDER BY created_at DESC, session_id DESC;

-- Enqueue alongside the source event transaction; duplicates preserve state.
-- name: EnqueueTurnTrace :one
INSERT INTO trace_deliveries (delivery_id, session_id, turn_id)
SELECT 'turn:' || id, session_id, id FROM turns
WHERE id = sqlc.arg(turn_id) AND completed_at IS NOT NULL
ON CONFLICT (delivery_id) DO UPDATE SET delivery_id = EXCLUDED.delivery_id
RETURNING *;

-- name: EnqueueUsageTrace :one
INSERT INTO trace_deliveries (delivery_id, session_id, usage_event_id)
SELECT 'usage:' || usage.session_id || ':' || usage.event_id, usage.session_id, usage.event_id
FROM provider_usage_events AS usage
WHERE usage.session_id = sqlc.arg(session_id) AND usage.event_id = sqlc.arg(event_id)
ON CONFLICT (delivery_id) DO UPDATE SET delivery_id = EXCLUDED.delivery_id
RETURNING *;

-- The schema requires a lease exactly when running, so COALESCE is the due
-- timestamp for both pending and running rows and matches the partial index.
-- No row means no lease granted, not proof the queue is drained: an expired
-- final attempt is terminalized without granting another lease. Keep polling.
-- name: ClaimTraceDelivery :one
WITH candidate AS (
    SELECT delivery_id FROM trace_deliveries
    WHERE status IN ('pending', 'running')
      AND COALESCE(lease_until, next_attempt_at) <= statement_timestamp()
      AND sqlc.arg(lease_seconds)::INT BETWEEN 1 AND 3600
      AND sqlc.arg(max_attempts)::INT BETWEEN 1 AND 1000
    ORDER BY COALESCE(lease_until, next_attempt_at), delivery_id
    FOR UPDATE SKIP LOCKED LIMIT 1
), claimed AS (
    UPDATE trace_deliveries AS delivery
    SET status = CASE WHEN delivery.attempts >= sqlc.arg(max_attempts)::INT THEN 'failed' ELSE 'running' END,
        attempts = delivery.attempts + CASE WHEN delivery.attempts < sqlc.arg(max_attempts)::INT THEN 1 ELSE 0 END,
        generation = delivery.generation + CASE WHEN delivery.attempts < sqlc.arg(max_attempts)::INT THEN 1 ELSE 0 END,
        lease_until = CASE WHEN delivery.attempts < sqlc.arg(max_attempts)::INT
            THEN statement_timestamp() + make_interval(secs => sqlc.arg(lease_seconds)::INT) ELSE NULL END,
        last_error = CASE WHEN delivery.attempts >= sqlc.arg(max_attempts)::INT
            THEN 'trace attempt budget exhausted before reclaim' ELSE delivery.last_error END
    FROM candidate WHERE delivery.delivery_id = candidate.delivery_id
    RETURNING delivery.*
)
SELECT * FROM claimed WHERE status = 'running';

-- name: CompleteTraceDelivery :one
UPDATE trace_deliveries
SET status = 'accepted', lease_until = NULL, last_error = '', accepted_at = statement_timestamp()
WHERE delivery_id = sqlc.arg(delivery_id) AND generation = sqlc.arg(generation)::BIGINT
  AND status = 'running' AND lease_until > statement_timestamp()
RETURNING *;

-- name: FailTraceDelivery :one
UPDATE trace_deliveries
SET status = CASE WHEN attempts >= sqlc.arg(max_attempts)::INT THEN 'failed' ELSE 'pending' END,
    lease_until = NULL,
    next_attempt_at = statement_timestamp() + make_interval(secs => LEAST(
        sqlc.arg(retry_max_seconds)::INT::DOUBLE PRECISION,
        sqlc.arg(retry_base_seconds)::INT::DOUBLE PRECISION
            * power(2::DOUBLE PRECISION, LEAST(GREATEST(attempts - 1, 0), 30))
    )),
    last_error = left(sqlc.arg(last_error)::TEXT, 4096)
WHERE delivery_id = sqlc.arg(delivery_id) AND generation = sqlc.arg(generation)::BIGINT
  AND status = 'running' AND lease_until > statement_timestamp()
  AND sqlc.arg(max_attempts)::INT BETWEEN 1 AND 1000
  AND sqlc.arg(retry_base_seconds)::INT BETWEEN 1 AND 86400
  AND sqlc.arg(retry_max_seconds)::INT BETWEEN sqlc.arg(retry_base_seconds)::INT AND 86400
RETURNING *;

-- name: CountTraceDeliveries :many
WITH statuses AS (
    SELECT 'pending'::TEXT AS status UNION ALL SELECT 'running'::TEXT
    UNION ALL SELECT 'accepted'::TEXT UNION ALL SELECT 'failed'::TEXT
)
SELECT statuses.status, count(delivery.delivery_id)::BIGINT AS count
FROM statuses LEFT JOIN trace_deliveries AS delivery ON delivery.status = statuses.status
GROUP BY statuses.status ORDER BY statuses.status;

-- Retained source rows are paged for trace delivery without truncating payloads.
-- name: ListTurnTraceTranscript :many
SELECT * FROM transcript_items
WHERE session_id = sqlc.arg(session_id) AND turn_id = sqlc.arg(turn_id)
  AND seq > sqlc.arg(after_seq)::BIGINT
ORDER BY seq
LIMIT sqlc.arg(row_limit)::INT;

-- name: ListSessionTasks :many
SELECT * FROM session_tasks ORDER BY session_id;

-- name: GetSessionTask :one
SELECT * FROM session_tasks WHERE session_id = $1;

-- name: LinkSessionTask :one
INSERT INTO session_tasks (session_id, task_url)
SELECT sqlc.arg(session_id), sqlc.arg(task_url)::TEXT
WHERE sqlc.arg(expected_generation)::BIGINT = 0
ON CONFLICT (session_id) DO NOTHING
RETURNING *;

-- name: ReplaceSessionTask :one
UPDATE session_tasks
SET task_url = sqlc.arg(task_url), generation = generation + 1
WHERE session_id = sqlc.arg(session_id)
  AND generation = sqlc.arg(expected_generation)::BIGINT
RETURNING *;
