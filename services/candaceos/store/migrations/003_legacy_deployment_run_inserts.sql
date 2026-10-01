-- A pre-002 Core can be restored automatically after the rollout migration.
-- Its INSERT omits both new columns, so fill them before NOT NULL and CHECK
-- constraints run. Explicit values from current Core pass through unchanged.
-- OR REPLACE plus DROP IF EXISTS also repairs databases that briefly received
-- this compatibility trigger as part of migration 002.
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
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS candaceos_deployment_runs_legacy_insert
ON candaceos_deployment_runs;

CREATE TRIGGER candaceos_deployment_runs_legacy_insert
BEFORE INSERT ON candaceos_deployment_runs
FOR EACH ROW
EXECUTE FUNCTION candaceos_fill_legacy_deployment_run_columns();
