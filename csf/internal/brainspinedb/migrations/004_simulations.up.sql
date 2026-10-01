CREATE TYPE brainspine_simulation_state AS ENUM
    ('pending', 'submitting', 'queued', 'running', 'succeeded', 'failed', 'cancelling', 'cancelled', 'submission_unknown');

CREATE TABLE brainspine_simulations (
    run_id TEXT PRIMARY KEY CHECK (run_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$'),
    campaign_id TEXT NOT NULL REFERENCES brainspine_runs(run_id),
    simulator INTEGER NOT NULL CHECK (simulator IN (1, 2)),
    executor INTEGER NOT NULL CHECK (executor IN (1, 2)),
    state brainspine_simulation_state NOT NULL DEFAULT 'pending',
    steps INTEGER NOT NULL CHECK (steps BETWEEN 1 AND 10000),
    completed_steps INTEGER NOT NULL DEFAULT 0 CHECK (completed_steps BETWEEN 0 AND steps),
    job_id TEXT NOT NULL DEFAULT '',
    job_queue TEXT NOT NULL DEFAULT '',
    job_definition TEXT NOT NULL DEFAULT '',
    artifact_uri TEXT NOT NULL DEFAULT '',
    reservation_usd_micros BIGINT NOT NULL CHECK (reservation_usd_micros BETWEEN 0 AND 300000000),
    timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds BETWEEN 60 AND 3600),
    reason TEXT NOT NULL DEFAULT '',
    log_stream TEXT NOT NULL DEFAULT '',
    log_token TEXT NOT NULL DEFAULT '',
    inspection_error TEXT NOT NULL DEFAULT '',
    cancellation_requested BOOLEAN NOT NULL DEFAULT FALSE,
    cleanup_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX brainspine_simulation_job ON brainspine_simulations(job_id) WHERE job_id <> '';
CREATE INDEX brainspine_simulation_queue ON brainspine_simulations(state, created_at);
CREATE TABLE brainspine_simulation_definitions (
    run_id TEXT NOT NULL REFERENCES brainspine_simulations(run_id),
    name TEXT NOT NULL,
    unit TEXT NOT NULL,
    description TEXT NOT NULL,
    PRIMARY KEY (run_id, name)
);
CREATE TABLE brainspine_simulation_measurements (
    run_id TEXT NOT NULL,
    metric TEXT NOT NULL,
    step BIGINT NOT NULL CHECK (step BETWEEN 0 AND 10000),
    value DOUBLE PRECISION NOT NULL CHECK (value > '-Infinity'::DOUBLE PRECISION AND value < 'Infinity'::DOUBLE PRECISION),
    recorded_at TIMESTAMPTZ NOT NULL,
    evidence_hash TEXT NOT NULL CHECK (evidence_hash ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (run_id, metric, step),
    FOREIGN KEY (run_id, metric) REFERENCES brainspine_simulation_definitions(run_id, name)
);
