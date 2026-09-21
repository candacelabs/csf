ALTER TABLE candaceos_deployment_runs
    ADD COLUMN rollout_id TEXT;

-- Runs created by one pre-rollout-schema reconciliation shared the exact
-- requested_at timestamp. Preserve that grouping when adopting existing data.
UPDATE candaceos_deployment_runs
SET rollout_id = deployment_id || ':' || requested_at::text;

ALTER TABLE candaceos_deployment_runs
    ALTER COLUMN rollout_id SET NOT NULL;

ALTER TABLE candaceos_deployment_runs
    ADD COLUMN desired_state TEXT;

-- Pre-migration runs did not record direction. Treat every historical attempt
-- as possibly running so a later stop converges conservatively instead of
-- silently leaving an app alive after an ambiguous or failed attempt.
UPDATE candaceos_deployment_runs
SET desired_state = 'running';

ALTER TABLE candaceos_deployment_runs
    ALTER COLUMN desired_state SET NOT NULL,
    ADD CONSTRAINT candaceos_deployment_runs_desired_state_check
        CHECK (desired_state IN ('running', 'stopped'));

CREATE INDEX candaceos_deployment_runs_rollout_idx
    ON candaceos_deployment_runs (deployment_id, requested_at DESC, rollout_id);
