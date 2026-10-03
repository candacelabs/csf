-- Copyright 2026 Candace Labs
--
-- A reservation beyond the account limit. The CHECK on
-- csf_job_budgets.reserved_usd_micros must reject it.
UPDATE csf_job_budgets SET reserved_usd_micros = limit_usd_micros + 1 WHERE account = 'budget-fixture'
