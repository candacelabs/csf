-- Per-agent external-tool configuration is durable state. Values are opaque
-- secret references; secret material remains outside this database.
CREATE TABLE IF NOT EXISTS csf_agent_configurations (
    agent_id TEXT PRIMARY KEY,
    revision BIGINT NOT NULL CHECK (revision > 0),
    langfuse_endpoint_url TEXT NOT NULL DEFAULT '',
    langfuse_public_key_secret_ref TEXT NOT NULL DEFAULT '',
    langfuse_secret_key_secret_ref TEXT NOT NULL DEFAULT '',
    opensearch_endpoint_url TEXT NOT NULL DEFAULT '',
    opensearch_index TEXT NOT NULL DEFAULT '',
    opensearch_embedding_model TEXT NOT NULL DEFAULT '',
    opensearch_credentials_secret_ref TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()
);
