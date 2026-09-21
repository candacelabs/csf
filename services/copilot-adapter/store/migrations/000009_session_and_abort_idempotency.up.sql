-- Session creation claims its client key before provisioning a worktree or
-- crossing the Copilot SDK boundary. Nullable columns preserve absent versus
-- present request fields without persisting the generated HTTP wire payload.
CREATE TABLE session_creations (
    idempotency_key UUID PRIMARY KEY,
    session_id UUID NOT NULL UNIQUE,
    model TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    worktree_mode TEXT NOT NULL,
    worktree_id UUID,
    base_ref TEXT,
    display_name TEXT,
    system_instructions TEXT,
    created_at TIMESTAMPTZ NOT NULL
);

-- An abort retry names both its client identity and exact turn target. The
-- turn row remains authoritative for the durable result.
CREATE TABLE turn_abort_submissions (
    session_id UUID NOT NULL,
    idempotency_key UUID NOT NULL,
    turn_id UUID NOT NULL,
    PRIMARY KEY (session_id, idempotency_key)
);
