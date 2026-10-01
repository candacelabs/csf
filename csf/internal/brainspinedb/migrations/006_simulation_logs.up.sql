ALTER TABLE brainspine_simulations
 ADD COLUMN log_document_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN log_projection_error TEXT NOT NULL DEFAULT '',
 ADD COLUMN log_indexed_at TIMESTAMPTZ,
 ADD COLUMN log_retry_at TIMESTAMPTZ NOT NULL DEFAULT now();
