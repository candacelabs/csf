-- No secondary indexes yet: the test substrate (candace/pkg/pgmem) does not
-- translate CREATE INDEX, and the list queries page by (created_at, id) over
-- tables sized for one operator. Follow-up: add them once pgmem accepts them.
CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    display_name TEXT NOT NULL,
    model TEXT NOT NULL,
    working_directory TEXT NOT NULL,
    system_instructions TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_turn_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ
);

CREATE TABLE turns (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    status TEXT NOT NULL,
    prompt_text TEXT NOT NULL,
    prompt_mode TEXT NOT NULL,
    author TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ
);

CREATE TABLE transcript_items (
    session_id UUID NOT NULL,
    seq BIGINT NOT NULL,
    turn_id UUID,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    author TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    body TEXT NOT NULL,
    PRIMARY KEY (session_id, seq)
);

CREATE TABLE pending_requests (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL,
    turn_id UUID,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    prompt TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    decision TEXT NOT NULL,
    answer TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ
);

CREATE TABLE session_events (
    session_id UUID NOT NULL,
    seq BIGINT NOT NULL,
    kind TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    payload JSONB NOT NULL,
    PRIMARY KEY (session_id, seq)
);
