-- Copyright 2026 Candace Labs
--
-- Progress beyond the admitted units. The CHECK comparing completed_units with
-- total_units must reject it.
UPDATE csf_jobs SET completed_units = total_units + 1 WHERE job_id = 'job-fixture'
