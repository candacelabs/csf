-- Copyright 2026 Candace Labs
--
-- A measurement day that is not an ISO calendar date. The regular-expression
-- CHECK on csf_meter_measurements.measured_on must reject it.
INSERT INTO csf_meter_measurements (measured_on, commit_sha, name, ordinal, measures, now, target, source, moved_by)
VALUES ('06-10-2026', '369d12d', 'consistency', 0, 'mean of satisfied over checked', '0.590', '1.000',
    'consistency', '[]')
