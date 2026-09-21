-- A host-defined agent identity selects session-scoped MCP configuration.
-- Empty is the explicit legacy/human-session value; the adapter itself does
-- not interpret permissions or service configuration for a nonempty identity.
-- The generated OpenAPI contract owns the complete identifier grammar. Keep
-- this durable invariant within the PostgreSQL subset used by pgmem as well.
ALTER TABLE sessions
ADD COLUMN agent_id TEXT NOT NULL DEFAULT ''
    CHECK (agent_id = '' OR length(agent_id) BETWEEN 1 AND 64);

-- Persist it in the creation receipt so a crash before SDK activation restores
-- the same identity and receives the same host-selected MCP configuration.
ALTER TABLE session_creations
ADD COLUMN agent_id TEXT NOT NULL DEFAULT ''
    CHECK (agent_id = '' OR length(agent_id) BETWEEN 1 AND 64);
