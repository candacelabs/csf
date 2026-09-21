-- name: AcquireMigrationLock :exec
SELECT pg_advisory_lock(729731104);

-- name: ReleaseMigrationLock :one
SELECT pg_advisory_unlock(729731104);

-- name: ListAppliedMigrationVersions :many
SELECT version FROM candaceos_schema_migrations ORDER BY version;

-- name: RecordMigration :exec
INSERT INTO candaceos_schema_migrations (version, name, applied_at)
VALUES (sqlc.arg(version), sqlc.arg(name), sqlc.arg(applied_at));

-- name: CreateRun :exec
INSERT INTO candaceos_operator_runs (
    run_id, session_id, title, prompt, status, started_at
) VALUES (
    sqlc.arg(run_id), sqlc.arg(session_id), sqlc.arg(title), sqlc.arg(prompt),
    sqlc.arg(status), sqlc.arg(started_at)
);

-- name: UpdateRunStatus :execrows
UPDATE candaceos_operator_runs
SET status = sqlc.arg(status), finished_at = sqlc.narg(finished_at)
WHERE run_id = sqlc.arg(run_id);

-- name: FailInterruptedOperatorRuns :many
UPDATE candaceos_operator_runs
SET status = 'failed', finished_at = sqlc.arg(interrupted_at)
WHERE status IN ('queued', 'running', 'waiting')
RETURNING run_id;

-- name: FailInterruptedDeploymentRuns :many
UPDATE candaceos_deployment_runs
SET status = 'failed',
    error_message = 'CandaceOS Core restarted before the node outcome was recorded',
    finished_at = sqlc.arg(interrupted_at)
WHERE status = 'running'
RETURNING run_id;

-- name: GetRun :one
SELECT * FROM candaceos_operator_runs WHERE run_id = sqlc.arg(run_id);

-- name: ListRecentRuns :many
SELECT * FROM candaceos_operator_runs ORDER BY started_at DESC LIMIT sqlc.arg(row_limit);

-- name: CreateApproval :exec
INSERT INTO candaceos_operator_approvals (
    approval_id, run_id, tool_call_id, request_kind, title, detail, risk,
    payload_sha256, status, requested_at, expires_at
) VALUES (
    sqlc.arg(approval_id), sqlc.narg(run_id), sqlc.narg(tool_call_id),
    sqlc.arg(request_kind), sqlc.arg(title), sqlc.arg(detail), sqlc.arg(risk),
    sqlc.arg(payload_sha256), 'pending', sqlc.arg(requested_at), sqlc.arg(expires_at)
);

-- name: ResolveApproval :execrows
UPDATE candaceos_operator_approvals
SET status = sqlc.arg(status), resolved_at = sqlc.arg(resolved_at), resolved_by = sqlc.arg(resolved_by)
WHERE approval_id = sqlc.arg(approval_id) AND status = 'pending';

-- name: ExpirePendingApprovalsOnRestart :many
UPDATE candaceos_operator_approvals
SET status = 'expired', resolved_at = sqlc.arg(interrupted_at), resolved_by = 'core-restart'
WHERE status = 'pending'
RETURNING approval_id, payload_sha256;

-- name: ExpireApprovals :execrows
UPDATE candaceos_operator_approvals
SET status = 'expired', resolved_at = sqlc.arg(expired_at), resolved_by = 'timeout'
WHERE status = 'pending' AND expires_at <= sqlc.arg(expired_at);

-- name: GetApproval :one
SELECT * FROM candaceos_operator_approvals WHERE approval_id = sqlc.arg(approval_id);

-- name: ListPendingApprovals :many
SELECT * FROM candaceos_operator_approvals
WHERE status = 'pending' AND expires_at > sqlc.arg(now_at)
ORDER BY requested_at ASC;

-- name: AppendReceipt :one
INSERT INTO candaceos_activity_receipts (
    entity_type, entity_id, kind, summary, payload_sha256, occurred_at
) VALUES (
    sqlc.arg(entity_type), sqlc.arg(entity_id), sqlc.arg(kind), sqlc.arg(summary),
    sqlc.narg(payload_sha256), sqlc.arg(occurred_at)
)
RETURNING receipt_id;

-- name: ListRecentReceipts :many
SELECT * FROM candaceos_activity_receipts ORDER BY receipt_id DESC LIMIT sqlc.arg(row_limit);

-- name: UpsertNode :exec
INSERT INTO candaceos_nodes (
    node_id, address, role, status, warden_term, last_seen_at, observed_at
) VALUES (
    sqlc.arg(node_id), sqlc.arg(address), sqlc.arg(role), sqlc.arg(status),
    sqlc.arg(warden_term), sqlc.narg(last_seen_at), sqlc.arg(observed_at)
)
ON CONFLICT (node_id) DO UPDATE SET
    address = EXCLUDED.address,
    role = EXCLUDED.role,
    status = EXCLUDED.status,
    warden_term = EXCLUDED.warden_term,
    last_seen_at = EXCLUDED.last_seen_at,
    observed_at = EXCLUDED.observed_at;

-- name: ListNodes :many
SELECT * FROM candaceos_nodes ORDER BY node_id;

-- name: DeleteNodeLabels :exec
DELETE FROM candaceos_node_labels WHERE node_id = sqlc.arg(node_id);

-- name: UpsertNodeLabel :exec
INSERT INTO candaceos_node_labels (node_id, label_key, label_value)
VALUES (sqlc.arg(node_id), sqlc.arg(label_key), sqlc.arg(label_value))
ON CONFLICT (node_id, label_key) DO UPDATE SET label_value = EXCLUDED.label_value;

-- name: ListNodeLabels :many
SELECT * FROM candaceos_node_labels
WHERE node_id = sqlc.arg(node_id)
ORDER BY label_key;

-- name: UpsertAppRevision :exec
INSERT INTO candaceos_app_revisions (
    app_revision_id, app_name, source_repository, source_revision, source_sha256,
    compose_file, image_digest, created_at
) VALUES (
    sqlc.arg(app_revision_id), sqlc.arg(app_name), sqlc.arg(source_repository),
    sqlc.arg(source_revision), sqlc.arg(source_sha256), sqlc.arg(compose_file),
    sqlc.narg(image_digest), sqlc.arg(created_at)
)
ON CONFLICT (app_revision_id) DO NOTHING;

-- name: UpsertDeployment :exec
INSERT INTO candaceos_deployments (
    deployment_id, app_revision_id, project_name, workspace_path, desired_state,
    placement_mode, exact_node_id, replicas, stateful, created_at, updated_at
) VALUES (
    sqlc.arg(deployment_id), sqlc.arg(app_revision_id), sqlc.arg(project_name),
    sqlc.arg(workspace_path), sqlc.arg(desired_state), sqlc.arg(placement_mode),
    sqlc.narg(exact_node_id), sqlc.arg(replicas), sqlc.arg(stateful),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (deployment_id) DO UPDATE SET
    app_revision_id = EXCLUDED.app_revision_id,
    project_name = EXCLUDED.project_name,
    workspace_path = EXCLUDED.workspace_path,
    desired_state = EXCLUDED.desired_state,
    placement_mode = EXCLUDED.placement_mode,
    exact_node_id = EXCLUDED.exact_node_id,
    replicas = EXCLUDED.replicas,
    stateful = EXCLUDED.stateful,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteDeploymentLabels :exec
DELETE FROM candaceos_deployment_labels WHERE deployment_id = sqlc.arg(deployment_id);

-- name: UpsertDeploymentLabel :exec
INSERT INTO candaceos_deployment_labels (deployment_id, label_key, label_value)
VALUES (sqlc.arg(deployment_id), sqlc.arg(label_key), sqlc.arg(label_value))
ON CONFLICT (deployment_id, label_key) DO UPDATE SET label_value = EXCLUDED.label_value;

-- name: ListDeploymentRolloutRows :many
SELECT d.*, r.app_name, r.source_revision, r.source_sha256, r.compose_file, r.image_digest,
       COALESCE(rollout.rollout_id, '') AS latest_rollout_id,
       COALESCE(run.run_id, '') AS latest_run_id,
       COALESCE(run.node_id, '') AS latest_node_id,
       COALESCE(run.desired_state, rollout.desired_state, '') AS latest_desired_state,
       COALESCE(
           run.status,
           CASE WHEN rollout.rollout_id IS NOT NULL THEN 'succeeded' ELSE 'pending' END
       ) AS latest_status,
       COALESCE(run.dry_run, FALSE) AS latest_dry_run,
       COALESCE(run.finished_at, rollout.requested_at) AS latest_finished_at,
       COALESCE(active_nodes.node_ids, ARRAY[]::TEXT[]) AS possibly_active_node_ids
FROM candaceos_deployments d
JOIN candaceos_app_revisions r USING (app_revision_id)
LEFT JOIN LATERAL (
    SELECT candidate.rollout_id, candidate.desired_state, candidate.requested_at
    FROM candaceos_deployment_rollouts AS candidate
    WHERE candidate.deployment_id = d.deployment_id
    ORDER BY candidate.requested_at DESC, candidate.rollout_id DESC
    LIMIT 1
) AS rollout ON TRUE
LEFT JOIN LATERAL (
    SELECT dr.run_id, dr.node_id, dr.desired_state, dr.status, dr.dry_run, dr.finished_at
    FROM candaceos_deployment_runs AS dr
    WHERE dr.rollout_id = rollout.rollout_id
    ORDER BY dr.node_id
) AS run ON TRUE
LEFT JOIN LATERAL (
    SELECT array_agg(active.node_id ORDER BY active.node_id)::TEXT[] AS node_ids
    FROM candaceos_possibly_active_deployment_nodes AS active
    WHERE active.deployment_id = d.deployment_id
) AS active_nodes ON TRUE
ORDER BY d.deployment_id, run.node_id;

-- name: GetDeploymentForReconcile :one
SELECT d.*, r.app_name, r.source_repository, r.source_revision,
       r.source_sha256, r.compose_file, r.image_digest, r.created_at AS revision_created_at
FROM candaceos_deployments d
JOIN candaceos_app_revisions r USING (app_revision_id)
WHERE d.deployment_id = sqlc.arg(deployment_id);

-- name: ListDeploymentLabels :many
SELECT label_key, label_value
FROM candaceos_deployment_labels
WHERE deployment_id = sqlc.arg(deployment_id)
ORDER BY label_key;

-- name: ListPossiblyActiveDeploymentNodes :many
SELECT node_id
FROM candaceos_possibly_active_deployment_nodes
WHERE deployment_id = sqlc.arg(deployment_id)
ORDER BY node_id;

-- name: CreateDeploymentRollout :exec
INSERT INTO candaceos_deployment_rollouts (
    rollout_id, deployment_id, app_revision_id, desired_state, requested_at
) VALUES (
    sqlc.arg(rollout_id), sqlc.arg(deployment_id), sqlc.arg(app_revision_id),
    sqlc.arg(desired_state), sqlc.arg(requested_at)
);

-- name: CreateDeploymentRun :exec
INSERT INTO candaceos_deployment_runs (
    run_id, rollout_id, deployment_id, app_revision_id, node_id, desired_state,
    warden_term, leader_id, status, requested_at
) VALUES (
    sqlc.arg(run_id), sqlc.arg(rollout_id), sqlc.arg(deployment_id),
    sqlc.arg(app_revision_id), sqlc.arg(node_id), sqlc.arg(desired_state),
    sqlc.arg(warden_term), sqlc.arg(leader_id), 'running', sqlc.arg(requested_at)
);

-- name: FinishDeploymentRun :execrows
UPDATE candaceos_deployment_runs
SET status = sqlc.arg(status), dry_run = sqlc.arg(dry_run),
    error_message = sqlc.narg(error_message), finished_at = sqlc.arg(finished_at)
WHERE run_id = sqlc.arg(run_id) AND status = 'running';
