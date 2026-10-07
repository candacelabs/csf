-- name: GetAgentConfiguration :one
SELECT agent_id, revision,
       langfuse_endpoint_url, langfuse_public_key_secret_ref, langfuse_secret_key_secret_ref,
       opensearch_endpoint_url, opensearch_index, opensearch_embedding_model, opensearch_credentials_secret_ref,
       updated_at
FROM csf_agent_configurations
WHERE agent_id = sqlc.arg(agent_id);

-- An initial create is admitted exactly once. A duplicate reports no row so
-- the service can return a revision conflict without overwriting state.
-- name: CreateAgentConfiguration :one
INSERT INTO csf_agent_configurations (
    agent_id, revision,
    langfuse_endpoint_url, langfuse_public_key_secret_ref, langfuse_secret_key_secret_ref,
    opensearch_endpoint_url, opensearch_index, opensearch_embedding_model, opensearch_credentials_secret_ref
) VALUES (
    sqlc.arg(agent_id), 1,
    sqlc.arg(langfuse_endpoint_url), sqlc.arg(langfuse_public_key_secret_ref), sqlc.arg(langfuse_secret_key_secret_ref),
    sqlc.arg(opensearch_endpoint_url), sqlc.arg(opensearch_index), sqlc.arg(opensearch_embedding_model), sqlc.arg(opensearch_credentials_secret_ref)
)
ON CONFLICT (agent_id) DO NOTHING
RETURNING agent_id, revision,
          langfuse_endpoint_url, langfuse_public_key_secret_ref, langfuse_secret_key_secret_ref,
          opensearch_endpoint_url, opensearch_index, opensearch_embedding_model, opensearch_credentials_secret_ref,
          updated_at;

-- A nonzero expected revision is a compare-and-swap update. A stale revision
-- reports no row and never changes the retained secret references.
-- name: UpdateAgentConfiguration :one
UPDATE csf_agent_configurations
SET revision = revision + 1,
    langfuse_endpoint_url = sqlc.arg(langfuse_endpoint_url),
    langfuse_public_key_secret_ref = sqlc.arg(langfuse_public_key_secret_ref),
    langfuse_secret_key_secret_ref = sqlc.arg(langfuse_secret_key_secret_ref),
    opensearch_endpoint_url = sqlc.arg(opensearch_endpoint_url),
    opensearch_index = sqlc.arg(opensearch_index),
    opensearch_embedding_model = sqlc.arg(opensearch_embedding_model),
    opensearch_credentials_secret_ref = sqlc.arg(opensearch_credentials_secret_ref),
    updated_at = statement_timestamp()
WHERE agent_id = sqlc.arg(agent_id)
  AND revision = sqlc.arg(expected_revision)::BIGINT
RETURNING agent_id, revision,
          langfuse_endpoint_url, langfuse_public_key_secret_ref, langfuse_secret_key_secret_ref,
          opensearch_endpoint_url, opensearch_index, opensearch_embedding_model, opensearch_credentials_secret_ref,
          updated_at;
