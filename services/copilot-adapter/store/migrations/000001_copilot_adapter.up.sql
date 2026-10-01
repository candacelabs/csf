-- This is the complete pre-release schema. Keep fresh installs at one
-- canonical shape; there is no deployed schema history to upgrade here.
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

CREATE TABLE sessions (
    agent_id TEXT NOT NULL DEFAULT '' CHECK (agent_id = '' OR length(agent_id) BETWEEN 1 AND 64),
    id UUID PRIMARY KEY,
    display_name TEXT NOT NULL,
    model TEXT NOT NULL,
    working_directory TEXT NOT NULL,
    system_instructions TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_turn_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    worktree_id UUID NOT NULL,
    permission_mode TEXT NOT NULL DEFAULT 'ask'
        CHECK (permission_mode IN ('ask', 'approveAll', 'allowlist')),
    failure_reason TEXT CHECK (failure_reason IS NULL OR length(failure_reason) <= 4096),
    failure_code INTEGER NOT NULL DEFAULT 0 CHECK (failure_code >= 0)
);

CREATE TABLE turns (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    status TEXT NOT NULL,
    prompt_text TEXT NOT NULL,
    prompt_mode TEXT NOT NULL,
    author TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    delivery_status TEXT NOT NULL DEFAULT 'accepted',
    started_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX turns_session_id_id_key ON turns (session_id, id);

CREATE TABLE transcript_items (
    session_id UUID NOT NULL,
    seq BIGINT NOT NULL,
    turn_id UUID,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    author TEXT,
    tool_name TEXT,
    body TEXT NOT NULL,
    tool_call_id TEXT,
    PRIMARY KEY (session_id, seq)
);

CREATE TABLE pending_requests (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    turn_id UUID,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    prompt TEXT NOT NULL,
    tool_name TEXT,
    decision TEXT NOT NULL,
    answer TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    delivery_status TEXT NOT NULL DEFAULT 'pending'
);

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

CREATE TABLE session_event_versions (
    session_id UUID NOT NULL,
    event_seq BIGINT NOT NULL,
    id UUID NOT NULL,
    worktree_id UUID NOT NULL,
    display_name TEXT NOT NULL,
    model TEXT NOT NULL,
    working_directory TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_turn_at TIMESTAMPTZ,
    turn_count BIGINT NOT NULL,
    failure_reason TEXT CHECK (failure_reason IS NULL OR length(failure_reason) <= 4096),
    permission_mode TEXT NOT NULL DEFAULT 'ask'
        CHECK (permission_mode IN ('ask', 'approveAll', 'allowlist')),
    failure_code INTEGER NOT NULL DEFAULT 0 CHECK (failure_code >= 0),
    PRIMARY KEY (session_id, event_seq)
);

CREATE TABLE turn_event_versions (
    session_id UUID NOT NULL,
    event_seq BIGINT NOT NULL,
    id UUID NOT NULL,
    status TEXT NOT NULL,
    prompt_text TEXT NOT NULL,
    prompt_mode TEXT NOT NULL,
    author TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, event_seq)
);

CREATE TABLE request_event_versions (
    session_id UUID NOT NULL,
    event_seq BIGINT NOT NULL,
    id UUID NOT NULL,
    turn_id UUID,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    prompt TEXT NOT NULL,
    tool_name TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, event_seq)
);

CREATE TABLE subagents (
    session_id UUID NOT NULL,
    id TEXT NOT NULL,
    turn_id UUID,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT,
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

CREATE TABLE subagent_event_versions (
    session_id UUID NOT NULL,
    event_seq BIGINT NOT NULL,
    id TEXT NOT NULL,
    turn_id UUID,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT,
    activity_count BIGINT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, event_seq)
);

CREATE TABLE chat_schedules (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    display_name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    cron_expression TEXT NOT NULL,
    timezone TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);

CREATE TABLE scheduled_turn_occurrences (
    occurrence_id TEXT PRIMARY KEY,
    turn_id UUID NOT NULL
);

CREATE TABLE session_creations (
    agent_id TEXT CHECK (agent_id IS NULL OR agent_id = '' OR length(agent_id) BETWEEN 1 AND 64),
    idempotency_key UUID PRIMARY KEY,
    session_id UUID NOT NULL UNIQUE,
    model TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    worktree_mode TEXT NOT NULL,
    worktree_id UUID,
    base_ref TEXT,
    display_name TEXT,
    system_instructions TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    sdk_create_attempt_id UUID,
    completed_at TIMESTAMPTZ,
    permission_mode TEXT NOT NULL DEFAULT 'ask'
        CHECK (permission_mode IN ('ask', 'approveAll', 'allowlist'))
);

CREATE TABLE turn_abort_submissions (
    session_id UUID NOT NULL,
    idempotency_key UUID NOT NULL,
    turn_id UUID NOT NULL,
    PRIMARY KEY (session_id, idempotency_key)
);

CREATE TABLE prompt_submissions (
    session_id UUID NOT NULL,
    idempotency_key UUID NOT NULL,
    turn_id UUID NOT NULL,
    PRIMARY KEY (session_id, idempotency_key)
);

CREATE TABLE bridge_event_receipts (
    session_id UUID NOT NULL,
    event_id TEXT NOT NULL,
    projected_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, event_id)
);

CREATE TABLE session_permission_tools (
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    tool_name TEXT NOT NULL CHECK (tool_name <> ''),
    PRIMARY KEY (session_id, position)
);

CREATE TABLE session_permission_shell_globs (
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    shell_glob TEXT NOT NULL CHECK (shell_glob <> ''),
    PRIMARY KEY (session_id, position)
);

CREATE TABLE session_creation_permission_tools (
    idempotency_key UUID NOT NULL REFERENCES session_creations(idempotency_key) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    tool_name TEXT NOT NULL CHECK (tool_name <> ''),
    PRIMARY KEY (idempotency_key, position)
);

CREATE TABLE session_creation_permission_shell_globs (
    idempotency_key UUID NOT NULL REFERENCES session_creations(idempotency_key) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    shell_glob TEXT NOT NULL CHECK (shell_glob <> ''),
    PRIMARY KEY (idempotency_key, position)
);

CREATE TABLE chat_schedule_creations (
    idempotency_key UUID PRIMARY KEY,
    schedule_id UUID NOT NULL UNIQUE,
    session_id UUID NOT NULL,
    display_name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    cron_expression TEXT NOT NULL,
    timezone TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE session_tasks (
    session_id UUID PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    task_url TEXT NOT NULL CHECK (task_url <> ''),
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0)
);

CREATE TABLE provider_usage_events (
    session_id UUID NOT NULL REFERENCES sessions(id),
    event_id TEXT NOT NULL CHECK (event_id <> ''),
    turn_id UUID,
    kind TEXT NOT NULL CHECK (kind IN ('modelCall', 'sessionCheckpoint')),
    occurred_at TIMESTAMPTZ NOT NULL,
    model TEXT,
    api_call_id TEXT,
    provider_call_id TEXT,
    service_request_id TEXT,
    input_tokens BIGINT CHECK (input_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens >= 0),
    cache_read_tokens BIGINT CHECK (cache_read_tokens >= 0),
    cache_write_tokens BIGINT CHECK (cache_write_tokens >= 0),
    reasoning_tokens BIGINT CHECK (reasoning_tokens >= 0),
    api_duration_ms BIGINT CHECK (api_duration_ms >= 0),
    billing_multiplier DOUBLE PRECISION CHECK (billing_multiplier >= 0 AND billing_multiplier <= 1.7976931348623157e308),
    premium_requests DOUBLE PRECISION CHECK (premium_requests >= 0 AND premium_requests <= 1.7976931348623157e308),
    nano_aiu DOUBLE PRECISION CHECK (nano_aiu >= 0 AND nano_aiu <= 1.7976931348623157e308),
    provider_event JSONB,
    PRIMARY KEY (session_id, event_id),
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id),
    CHECK (premium_requests IS NULL OR kind = 'sessionCheckpoint')
);

CREATE TABLE trace_deliveries (
    delivery_id TEXT PRIMARY KEY CHECK (delivery_id <> ''),
    session_id UUID NOT NULL REFERENCES sessions(id),
    turn_id UUID,
    usage_event_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'accepted', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation >= 0),
    lease_until TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(last_error) <= 4096),
    accepted_at TIMESTAMPTZ,
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id),
    FOREIGN KEY (session_id, usage_event_id)
        REFERENCES provider_usage_events(session_id, event_id),
    CHECK ((turn_id IS NOT NULL) <> (usage_event_id IS NOT NULL)),
    CHECK ((status = 'running') = (lease_until IS NOT NULL)),
    CHECK (status <> 'running' OR attempts > 0),
    CHECK ((status = 'accepted') = (accepted_at IS NOT NULL))
);

CREATE INDEX provider_usage_latest_premium_idx
    ON provider_usage_events (session_id, occurred_at DESC, event_id DESC)
    WHERE kind = 'sessionCheckpoint' AND premium_requests IS NOT NULL;

CREATE INDEX trace_deliveries_due_idx
    ON trace_deliveries (COALESCE(lease_until, next_attempt_at), delivery_id)
    WHERE status IN ('pending', 'running');
