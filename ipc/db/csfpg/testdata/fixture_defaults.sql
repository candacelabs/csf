-- Copyright 2026 Candace Labs
--
-- Columns the fixture rows leave to their defaults: a now() timestamp and an
-- enum default.
SELECT jobs.created_at IS NOT NULL AS stamped, tasks.status AS task_status, jobs.state AS job_state
FROM csf_projection_tasks AS tasks, csf_jobs AS jobs
WHERE tasks.source_id = 'source-fixture' AND jobs.job_id = 'job-fixture'
