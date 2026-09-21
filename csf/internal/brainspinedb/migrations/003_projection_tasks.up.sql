-- Apply transactionally through the existing prefixed sqlmigrate ledger.
-- Existing declarations are drift errors; do not mask them with IF NOT EXISTS.
-- Schema version 3: durable projection work belongs to the existing Go host.
CREATE TYPE brainspine_projection_status AS ENUM ('pending', 'running', 'succeeded', 'failed');

CREATE TABLE brainspine_projection_tasks (
    source_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    status brainspine_projection_status NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_generation BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    lease_until TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(last_error) <= 4096),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, revision),
    FOREIGN KEY (source_id, revision)
        REFERENCES brainspine_source_revisions (source_id, revision),
    CHECK ((status = 'running') = (lease_until IS NOT NULL)),
    CHECK (status <> 'running' OR attempts > 0)
);

CREATE INDEX brainspine_projection_pending_idx
    ON brainspine_projection_tasks (next_attempt_at, source_id, revision)
    WHERE status = 'pending';
CREATE INDEX brainspine_projection_expired_idx
    ON brainspine_projection_tasks (lease_until, source_id, revision)
    WHERE status = 'running';

INSERT INTO brainspine_projection_tasks (source_id, revision)
SELECT source_id, revision FROM brainspine_source_revisions
ON CONFLICT (source_id, revision) DO NOTHING;
