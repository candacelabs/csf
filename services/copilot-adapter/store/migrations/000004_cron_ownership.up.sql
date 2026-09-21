-- Chat schedules own only the product metadata that binds a prompt to a
-- session. candace/pkg/cron owns recurrence cursors, occurrences, leases,
-- attempts, overlap and completion in its own relational tables.
DROP TABLE chat_schedule_runs;

ALTER TABLE chat_schedules DROP COLUMN next_run_at;
ALTER TABLE chat_schedules DROP COLUMN last_run_at;
ALTER TABLE chat_schedules DROP COLUMN last_run_status;
ALTER TABLE chat_schedules DROP COLUMN last_error;

-- A cron occurrence maps to at most one durable turn. This is delivery
-- provenance/idempotency, not a second scheduler run ledger. The separate
-- primary-key relation is supported by both PostgreSQL and the repository's
-- pgmem contract emulator.
CREATE TABLE scheduled_turn_occurrences (
    occurrence_id TEXT PRIMARY KEY,
    turn_id UUID NOT NULL
);
