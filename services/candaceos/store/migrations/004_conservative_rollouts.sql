CREATE TABLE candaceos_deployment_rollouts (
    rollout_id TEXT PRIMARY KEY,
    deployment_id TEXT NOT NULL REFERENCES candaceos_deployments(deployment_id),
    app_revision_id TEXT NOT NULL REFERENCES candaceos_app_revisions(app_revision_id),
    desired_state TEXT NOT NULL CHECK (desired_state IN ('running', 'stopped')),
    requested_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX candaceos_deployment_rollouts_deployment_idx
    ON candaceos_deployment_rollouts (deployment_id, requested_at DESC, rollout_id);

-- Migration 002 briefly copied the deployment's current desired state onto
-- every historical run. Those rows use a derived rollout ID, so repair them to
-- the only conservative interpretation available when their direction was not
-- originally recorded.
UPDATE candaceos_deployment_runs
SET desired_state = 'running'
WHERE starts_with(rollout_id, deployment_id || ':');

INSERT INTO candaceos_deployment_rollouts (
    rollout_id, deployment_id, app_revision_id, desired_state, requested_at
)
SELECT
    rollout_id,
    min(deployment_id),
    min(app_revision_id),
    CASE WHEN bool_or(desired_state = 'running') THEN 'running' ELSE 'stopped' END,
    min(requested_at)
FROM candaceos_deployment_runs
GROUP BY rollout_id;

ALTER TABLE candaceos_deployment_runs
    ADD CONSTRAINT candaceos_deployment_runs_rollout_fk
    FOREIGN KEY (rollout_id) REFERENCES candaceos_deployment_rollouts(rollout_id);

-- A node remains possibly active after an incomplete or failed operation. Only
-- a successful, live stop is definitive evidence that it is inactive. Dry-run
-- attempts never alter the previously known live state and are ignored here.
CREATE VIEW candaceos_possibly_active_deployment_nodes AS
SELECT deployment_id, node_id
FROM (
    SELECT DISTINCT ON (deployment_id, node_id)
        deployment_id, node_id, desired_state, status
    FROM candaceos_deployment_runs
    WHERE dry_run IS DISTINCT FROM TRUE
    ORDER BY deployment_id, node_id, requested_at DESC, rollout_id DESC, run_id DESC
) AS latest_attempt
WHERE status <> 'succeeded' OR desired_state = 'running';

-- Retain rollback compatibility after deployment_runs gains its rollout
-- foreign key. A pre-002 Core omits both new run columns and never creates the
-- rollout parent, so its trigger supplies conservative values and the parent
-- row in the same statement.
CREATE OR REPLACE FUNCTION candaceos_fill_legacy_deployment_run_columns()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.rollout_id IS NULL THEN
        NEW.rollout_id := NEW.deployment_id || ':' || NEW.requested_at::text;
    END IF;
    IF NEW.desired_state IS NULL THEN
        NEW.desired_state := 'running';
    END IF;
    INSERT INTO candaceos_deployment_rollouts (
        rollout_id, deployment_id, app_revision_id, desired_state, requested_at
    ) VALUES (
        NEW.rollout_id, NEW.deployment_id, NEW.app_revision_id,
        NEW.desired_state, NEW.requested_at
    ) ON CONFLICT (rollout_id) DO NOTHING;
    RETURN NEW;
END;
$$;
