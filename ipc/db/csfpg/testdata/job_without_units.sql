-- Copyright 2026 Candace Labs
--
-- A job admitting no work. The CHECK on csf_jobs.total_units must reject it.
INSERT INTO csf_jobs (job_id, kind, executor, budget_account, request, request_sha256, total_units,
    reservation_usd_micros, timeout_seconds)
VALUES ('job-empty', 'overnight.compute', 'local', 'budget-fixture', '{}', repeat('b', 64), 0, 0, 60)
