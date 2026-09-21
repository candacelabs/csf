-- A link is explicit provenance, not a title/branch heuristic. Task status and
-- checkpoints remain owned by the issue authority; this table stores neither.
CREATE TABLE session_tasks (
    session_id UUID PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    -- The source adapter validates canonical issue identity before a write.
    -- This table owns association, referential integrity and revision ordering.
    task_url TEXT NOT NULL CHECK (task_url <> ''),
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0)
);
