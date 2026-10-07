-- EVAL-SUITE (#416): the held-out suite every csf build is scored on before
-- it goes live; candace/services/ouroboros/evaluate owns the rules. A new
-- file rather than an edit of 001_init.sql, because a live database records
-- 001 as applied and would never see the edit. Every instant is a parameter
-- the service read from its clock.

-- One version of the suite: its tickets, rotation and the derivation of its
-- size and budget, as evaluate.Suite encodes them.
CREATE TABLE csf_eval_suites (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    selected_at TIMESTAMPTZ NOT NULL,
    suite JSONB NOT NULL
);

-- One suite ticket replayed on one build, read at the suite's budget.
CREATE TABLE csf_eval_replays (
    build TEXT NOT NULL CHECK (build ~ '^[0-9a-f]{12}$'),
    suite_version INTEGER NOT NULL REFERENCES csf_eval_suites (version),
    ticket BIGINT NOT NULL CHECK (ticket > 0),
    node TEXT NOT NULL CHECK (node IN ('host', 'burst')),
    assignment_id TEXT NOT NULL CHECK (length(assignment_id) BETWEEN 1 AND 64),
    tool_calls BIGINT NOT NULL CHECK (tool_calls >= 0),
    episodes BIGINT NOT NULL CHECK (episodes >= 0),
    recall DOUBLE PRECISION NOT NULL CHECK (recall >= 0 AND recall <= 1),
    cost_usd_micros BIGINT NOT NULL CHECK (cost_usd_micros >= 0),
    seconds BIGINT NOT NULL CHECK (seconds >= 0),
    recorded_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (build, suite_version, ticket)
);

-- One scoring run of a build: its wall clock from the first replay launched
-- to the last recorded. The score itself is always computed from the
-- replays.
CREATE TABLE csf_eval_scores (
    build TEXT NOT NULL CHECK (build ~ '^[0-9a-f]{12}$'),
    suite_version INTEGER NOT NULL REFERENCES csf_eval_suites (version),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (build, suite_version)
);
