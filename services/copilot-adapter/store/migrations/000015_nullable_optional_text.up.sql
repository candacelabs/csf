CREATE TABLE transcript_items_nullable (
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
INSERT INTO transcript_items_nullable
SELECT session_id, seq, turn_id, kind, occurred_at, NULLIF(author, ''),
       NULLIF(tool_name, ''), body, NULLIF(tool_call_id, '')
FROM transcript_items;
DROP TABLE transcript_items;
ALTER TABLE transcript_items_nullable RENAME TO transcript_items;

CREATE TABLE pending_requests_nullable (
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
INSERT INTO pending_requests_nullable
SELECT id, session_id, turn_id, kind, status, prompt, NULLIF(tool_name, ''),
       decision, answer, created_at, resolved_at, delivery_status
FROM pending_requests;
DROP TABLE pending_requests;
ALTER TABLE pending_requests_nullable RENAME TO pending_requests;

CREATE TABLE request_event_versions_nullable (
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
INSERT INTO request_event_versions_nullable
SELECT session_id, event_seq, id, turn_id, kind, status, prompt, NULLIF(tool_name, ''),
       created_at, resolved_at
FROM request_event_versions;
DROP TABLE request_event_versions;
ALTER TABLE request_event_versions_nullable RENAME TO request_event_versions;

CREATE TABLE subagents_nullable (
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
INSERT INTO subagents_nullable
SELECT session_id, id, turn_id, display_name, status, NULLIF(summary, ''),
       activity_count, started_at, updated_at, completed_at
FROM subagents;
DROP TABLE subagents;
ALTER TABLE subagents_nullable RENAME TO subagents;

CREATE TABLE subagent_event_versions_nullable (
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
INSERT INTO subagent_event_versions_nullable
SELECT session_id, event_seq, id, turn_id, display_name, status, NULLIF(summary, ''),
       activity_count, started_at, updated_at, completed_at
FROM subagent_event_versions;
DROP TABLE subagent_event_versions;
ALTER TABLE subagent_event_versions_nullable RENAME TO subagent_event_versions;

CREATE TABLE session_creations_nullable (
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
    agent_id TEXT CHECK (agent_id IS NULL OR agent_id = '' OR length(agent_id) BETWEEN 1 AND 64)
);
INSERT INTO session_creations_nullable
SELECT idempotency_key, session_id, model, repository_id, worktree_mode, worktree_id,
       base_ref, display_name, system_instructions, created_at, sdk_create_attempt_id,
       completed_at, NULLIF(agent_id, '')
FROM session_creations;
DROP TABLE session_creations;
ALTER TABLE session_creations_nullable RENAME TO session_creations;
