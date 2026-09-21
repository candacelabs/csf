-- Usage facts and trace delivery stay relational in the existing Workbench DB.
-- Missing provider numbers remain NULL. Billing multiplier is not currency.
ALTER TABLE turns ADD COLUMN started_at TIMESTAMPTZ;

UPDATE turns
SET started_at = observed.started_at
FROM (
    SELECT session_id, turn_id, min(occurred_at) AS started_at
    FROM session_events
    WHERE kind = 'turnStarted' AND turn_id IS NOT NULL
    GROUP BY session_id, turn_id
) AS observed
WHERE turns.id = observed.turn_id AND turns.session_id = observed.session_id
  AND (turns.completed_at IS NULL OR observed.started_at <= turns.completed_at);

-- Only snapshots at or after the observed start can carry that timestamp.
ALTER TABLE turn_event_versions ADD COLUMN started_at TIMESTAMPTZ;
UPDATE turn_event_versions
SET started_at = turns.started_at
FROM turns, session_events AS event
WHERE turn_event_versions.id = turns.id AND turn_event_versions.session_id = turns.session_id
  AND event.session_id = turn_event_versions.session_id AND event.seq = turn_event_versions.event_seq
  AND turns.started_at IS NOT NULL AND event.occurred_at >= turns.started_at;

-- Session ownership is a database invariant, including nullable usage links.
CREATE UNIQUE INDEX turns_session_id_id_key ON turns (session_id, id);

CREATE TABLE provider_usage_events (
    session_id UUID NOT NULL REFERENCES sessions(id),
    event_id TEXT NOT NULL CHECK (event_id <> ''),
    turn_id UUID,
    kind TEXT NOT NULL CHECK (kind IN ('modelCall', 'sessionCheckpoint')),
    occurred_at TIMESTAMPTZ NOT NULL,
    model TEXT,
    api_call_id TEXT,
    provider_call_id TEXT,
    service_request_id TEXT,
    input_tokens BIGINT CHECK (input_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens >= 0),
    cache_read_tokens BIGINT CHECK (cache_read_tokens >= 0),
    cache_write_tokens BIGINT CHECK (cache_write_tokens >= 0),
    reasoning_tokens BIGINT CHECK (reasoning_tokens >= 0),
    api_duration_ms BIGINT CHECK (api_duration_ms >= 0),
    billing_multiplier DOUBLE PRECISION CHECK (billing_multiplier >= 0 AND billing_multiplier <= 1.7976931348623157e308),
    premium_requests DOUBLE PRECISION CHECK (premium_requests >= 0 AND premium_requests <= 1.7976931348623157e308),
    nano_aiu DOUBLE PRECISION CHECK (nano_aiu >= 0 AND nano_aiu <= 1.7976931348623157e308),
    provider_event JSONB,
    PRIMARY KEY (session_id, event_id),
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id),
    CHECK (premium_requests IS NULL OR kind = 'sessionCheckpoint')
);

CREATE TABLE trace_deliveries (
    delivery_id TEXT PRIMARY KEY CHECK (delivery_id <> ''),
    session_id UUID NOT NULL REFERENCES sessions(id),
    turn_id UUID,
    usage_event_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'accepted', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation >= 0),
    lease_until TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT '' CHECK (length(last_error) <= 4096),
    accepted_at TIMESTAMPTZ,
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id),
    FOREIGN KEY (session_id, usage_event_id)
        REFERENCES provider_usage_events(session_id, event_id),
    CHECK ((turn_id IS NOT NULL) <> (usage_event_id IS NOT NULL)),
    CHECK ((status = 'running') = (lease_until IS NOT NULL)),
    CHECK (status <> 'running' OR attempts > 0),
    CHECK ((status = 'accepted') = (accepted_at IS NOT NULL))
);

-- Completed turns are retained facts; delivery is asynchronous and idempotent.
INSERT INTO trace_deliveries (delivery_id, session_id, turn_id)
SELECT 'turn:' || id, session_id, id FROM turns
WHERE completed_at IS NOT NULL
ON CONFLICT (delivery_id) DO NOTHING;

-- Restrict read indexes to the two retained-history paths measured by EXPLAIN.
CREATE INDEX provider_usage_latest_premium_idx
    ON provider_usage_events (session_id, occurred_at DESC, event_id DESC)
    WHERE kind = 'sessionCheckpoint' AND premium_requests IS NOT NULL;

CREATE INDEX trace_deliveries_due_idx
    ON trace_deliveries (COALESCE(lease_until, next_attempt_at), delivery_id)
    WHERE status IN ('pending', 'running');
