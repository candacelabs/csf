-- Copyright 2026 Candace Labs
--
-- An invariant satisfied on more instances than it was checked on. The CHECK
-- comparing satisfied with checked on csf_meter_invariants must reject it.
INSERT INTO csf_meter_invariants (measured_on, commit_sha, name, satisfied, checked, mode, owner_slice)
VALUES ('2026-10-06', '369d12d', 'code_compiles', 251, 250, 'hard_gate', 'green_main')
