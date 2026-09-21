-- Schedule creation retries carry one client UUID. The immutable relational
-- receipt stores the normalized request beside the schedule identity so a
-- lost HTTP response can be retried without creating a second cron product.
CREATE TABLE chat_schedule_creations (
    idempotency_key UUID PRIMARY KEY,
    schedule_id UUID NOT NULL UNIQUE,
    session_id UUID NOT NULL,
    display_name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    cron_expression TEXT NOT NULL,
    timezone TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
