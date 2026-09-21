CREATE TABLE IF NOT EXISTS candaceos_operator_runs (
    run_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    title TEXT NOT NULL,
    prompt TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'waiting', 'succeeded', 'failed', 'aborted')),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS candaceos_operator_approvals (
    approval_id TEXT PRIMARY KEY,
    run_id TEXT REFERENCES candaceos_operator_runs(run_id),
    tool_call_id TEXT,
    request_kind TEXT NOT NULL,
    title TEXT NOT NULL,
    detail TEXT NOT NULL,
    risk TEXT NOT NULL CHECK (risk IN ('low', 'medium', 'high')),
    payload_sha256 TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
    requested_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    resolved_by TEXT
);

CREATE TABLE IF NOT EXISTS candaceos_activity_receipts (
    receipt_id BIGSERIAL PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    summary TEXT NOT NULL,
    payload_sha256 TEXT,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS candaceos_activity_receipts_entity_idx
    ON candaceos_activity_receipts (entity_type, entity_id, receipt_id DESC);

CREATE TABLE IF NOT EXISTS candaceos_nodes (
    node_id TEXT PRIMARY KEY,
    address TEXT NOT NULL,
    role TEXT NOT NULL,
    status TEXT NOT NULL,
    warden_term BIGINT NOT NULL CHECK (warden_term >= 0),
    last_seen_at TIMESTAMPTZ,
    observed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS candaceos_node_labels (
    node_id TEXT NOT NULL REFERENCES candaceos_nodes(node_id) ON DELETE CASCADE,
    label_key TEXT NOT NULL,
    label_value TEXT NOT NULL,
    PRIMARY KEY (node_id, label_key)
);

CREATE TABLE IF NOT EXISTS candaceos_app_revisions (
    app_revision_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    source_repository TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    source_sha256 TEXT NOT NULL,
    compose_file TEXT NOT NULL,
    image_digest TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (app_name, source_revision, source_sha256)
);

CREATE TABLE IF NOT EXISTS candaceos_deployments (
    deployment_id TEXT PRIMARY KEY,
    app_revision_id TEXT NOT NULL REFERENCES candaceos_app_revisions(app_revision_id),
    project_name TEXT NOT NULL,
    workspace_path TEXT NOT NULL,
    desired_state TEXT NOT NULL CHECK (desired_state IN ('running', 'stopped')),
    placement_mode TEXT NOT NULL CHECK (placement_mode IN ('node', 'leader', 'labels')),
    exact_node_id TEXT,
    replicas INTEGER NOT NULL CHECK (replicas > 0),
    stateful BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK ((placement_mode = 'node') = (exact_node_id IS NOT NULL)),
    CHECK (NOT stateful OR placement_mode = 'node')
);

CREATE TABLE IF NOT EXISTS candaceos_deployment_runs (
    run_id TEXT PRIMARY KEY,
    deployment_id TEXT NOT NULL REFERENCES candaceos_deployments(deployment_id),
    app_revision_id TEXT NOT NULL REFERENCES candaceos_app_revisions(app_revision_id),
    node_id TEXT NOT NULL,
    warden_term BIGINT NOT NULL CHECK (warden_term > 0),
    leader_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    dry_run BOOLEAN,
    error_message TEXT,
    requested_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS candaceos_deployment_runs_deployment_idx
    ON candaceos_deployment_runs (deployment_id, requested_at DESC);

CREATE TABLE IF NOT EXISTS candaceos_deployment_labels (
    deployment_id TEXT NOT NULL REFERENCES candaceos_deployments(deployment_id) ON DELETE CASCADE,
    label_key TEXT NOT NULL,
    label_value TEXT NOT NULL,
    PRIMARY KEY (deployment_id, label_key)
);
