-- Mutable aggregate rows cannot reconstruct an earlier SSE frame faithfully.
-- These relational version rows capture the domain state announced by each
-- event without persisting generated HTTP/SSE wire payloads.
-- A checkout may already have emitted reference-only 000003 events. Their
-- historical mutable state cannot be recovered faithfully, so discard only
-- those pointers while retaining domain rows and each counter's last_event_seq.
DELETE FROM session_events;

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
    tool_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, event_seq)
);

CREATE TABLE subagent_event_versions (
    session_id UUID NOT NULL,
    event_seq BIGINT NOT NULL,
    id TEXT NOT NULL,
    turn_id UUID,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL,
    summary TEXT NOT NULL,
    activity_count BIGINT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, event_seq)
);
