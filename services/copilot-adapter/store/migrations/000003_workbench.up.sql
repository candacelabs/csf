CREATE TABLE worktrees (
    id UUID PRIMARY KEY,
    repository_id TEXT NOT NULL,
    repository_root TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    base_ref TEXT NOT NULL,
    managed BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE sessions ADD COLUMN worktree_id UUID;

INSERT INTO worktrees (
    id,
    repository_id,
    repository_root,
    path,
    base_ref,
    managed,
    created_at,
    updated_at
)
SELECT
    session.id,
    'legacy',
    session.working_directory,
    session.working_directory,
    'HEAD',
    FALSE,
    session.created_at,
    session.updated_at
FROM sessions AS session
WHERE session.id = (
    SELECT candidate.id
    FROM sessions AS candidate
    WHERE candidate.working_directory = session.working_directory
    ORDER BY candidate.created_at ASC, candidate.id ASC
    LIMIT 1
);

UPDATE sessions
SET worktree_id = (
    SELECT worktrees.id
    FROM worktrees
    WHERE worktrees.path = sessions.working_directory
)
WHERE worktree_id IS NULL;

ALTER TABLE sessions ALTER COLUMN worktree_id SET NOT NULL;

-- Session events persist relational references, never the generated HTTP/SSE
-- payload. The transcript and request tables remain authoritative and the
-- boundary reconstructs the current generated type when a frame is replayed.
-- Existing event history is intentionally reset during this pre-release schema
-- upgrade: its JSON payload was a wire-format cache and had no independent
-- domain facts that are not already retained by the referenced tables.
DROP TABLE session_events;

CREATE TABLE session_events (
    session_id UUID NOT NULL,
    seq BIGINT NOT NULL,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    transcript_seq BIGINT,
    turn_id UUID,
    request_id UUID,
    subagent_id TEXT,
    subagent_activity_seq BIGINT,
    delta_text TEXT NOT NULL,
    PRIMARY KEY (session_id, seq)
);

CREATE TABLE session_counters (
    session_id UUID PRIMARY KEY,
    last_transcript_seq BIGINT NOT NULL,
    last_event_seq BIGINT NOT NULL
);

INSERT INTO session_counters (
    session_id,
    last_transcript_seq,
    last_event_seq
)
SELECT
    sessions.id,
    COALESCE((
        SELECT MAX(transcript_items.seq)
        FROM transcript_items
        WHERE transcript_items.session_id = sessions.id
    ), 0),
    0
FROM sessions;

ALTER TABLE pending_requests
    ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'pending';

CREATE TABLE chat_schedules (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    display_name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    cron_expression TEXT NOT NULL,
    timezone TEXT NOT NULL,
    status TEXT NOT NULL,
    next_run_at TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    last_run_status TEXT NOT NULL,
    last_error TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);

CREATE TABLE chat_schedule_runs (
    occurrence_id TEXT NOT NULL,
    schedule_id UUID NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL,
    turn_id UUID,
    error TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (occurrence_id, schedule_id)
);

CREATE TABLE subagents (
    session_id UUID NOT NULL,
    id TEXT NOT NULL,
    turn_id UUID,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT NOT NULL,
    activity_count BIGINT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, id)
);

CREATE TABLE subagent_activities (
    session_id UUID NOT NULL,
    subagent_id TEXT NOT NULL,
    seq BIGINT NOT NULL,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    body TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,
    PRIMARY KEY (session_id, subagent_id, seq)
);
