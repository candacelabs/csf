-- Copyright 2026 Candace Labs
--
-- A measurement of positive infinity. The CHECK comparing value with
-- 'Infinity'::DOUBLE PRECISION must reject it.
INSERT INTO csf_job_measurements (job_id, metric, step, value, recorded_at, evidence_hash)
VALUES ('job-fixture', 'reward', 5, 1e999, '2026-10-01T00:00:04Z', repeat('c', 64))
