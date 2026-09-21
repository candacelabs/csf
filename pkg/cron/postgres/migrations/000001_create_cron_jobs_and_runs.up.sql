-- Durable scheduler state. This schema deliberately stores schedule semantics
-- as relational columns; no API/event wire representation is persisted here.
CREATE TABLE candace_cron_jobs (
    job_name             TEXT        PRIMARY KEY
                                      CHECK (length(job_name) BETWEEN 1 AND 128)
                                      CHECK (job_name ~ '^[a-z][a-z0-9._/-]*$'),
    schedule_kind        TEXT        NOT NULL
                                      CHECK (schedule_kind IN (
                                          'daily', 'weekly', 'monthly',
                                          'last_day_of_month', 'every', 'raw'
                                      )),
    local_hour           SMALLINT,
    local_minute         SMALLINT,
    weekday              SMALLINT,
    month_day            SMALLINT,
    interval_nanoseconds BIGINT,
    raw_expression       TEXT,
    timezone             TEXT        NOT NULL
                                      CHECK (length(timezone) BETWEEN 1 AND 128),
    interval_anchor_at   TIMESTAMPTZ,
    schedule_cursor_at   TIMESTAMPTZ,
    next_run_at          TIMESTAMPTZ,
    catch_up_policy      TEXT        NOT NULL DEFAULT 'none'
                                      CHECK (catch_up_policy IN ('none', 'latest', 'all')),
    overlap_policy       TEXT        NOT NULL DEFAULT 'skip'
                                      CHECK (overlap_policy IN ('skip', 'allow')),
    enabled              BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (local_hour IS NULL OR local_hour BETWEEN 0 AND 23),
    CHECK (local_minute IS NULL OR local_minute BETWEEN 0 AND 59),
    CHECK (weekday IS NULL OR weekday BETWEEN 0 AND 6),
    CHECK (month_day IS NULL OR month_day BETWEEN 1 AND 31),
    CHECK (interval_nanoseconds IS NULL OR (interval_nanoseconds >= 1000 AND interval_nanoseconds % 1000 = 0)),
    CHECK (
        (schedule_kind = 'daily'
            AND local_hour IS NOT NULL AND local_minute IS NOT NULL
            AND weekday IS NULL AND month_day IS NULL
            AND interval_nanoseconds IS NULL AND raw_expression IS NULL
            AND interval_anchor_at IS NULL)
        OR
        (schedule_kind = 'weekly'
            AND local_hour IS NOT NULL AND local_minute IS NOT NULL AND weekday IS NOT NULL
            AND month_day IS NULL AND interval_nanoseconds IS NULL AND raw_expression IS NULL
            AND interval_anchor_at IS NULL)
        OR
        (schedule_kind = 'monthly'
            AND local_hour IS NOT NULL AND local_minute IS NOT NULL AND month_day IS NOT NULL
            AND weekday IS NULL AND interval_nanoseconds IS NULL AND raw_expression IS NULL
            AND interval_anchor_at IS NULL)
        OR
        (schedule_kind = 'last_day_of_month'
            AND local_hour IS NOT NULL AND local_minute IS NOT NULL
            AND weekday IS NULL AND month_day IS NULL
            AND interval_nanoseconds IS NULL AND raw_expression IS NULL
            AND interval_anchor_at IS NULL)
        OR
        (schedule_kind = 'every'
            AND local_hour IS NULL AND local_minute IS NULL
            AND weekday IS NULL AND month_day IS NULL
            AND interval_nanoseconds IS NOT NULL AND raw_expression IS NULL
            AND interval_anchor_at IS NOT NULL)
        OR
        (schedule_kind = 'raw'
            AND local_hour IS NULL AND local_minute IS NULL
            AND weekday IS NULL AND month_day IS NULL
            AND interval_nanoseconds IS NULL
            AND length(raw_expression) BETWEEN 1 AND 256
            AND interval_anchor_at IS NULL)
    )
);

CREATE TABLE candace_cron_runs (
    occurrence_id        TEXT        PRIMARY KEY
                                      CHECK (occurrence_id ~ '^occ_[0-9a-f]{64}$'),
    job_name             TEXT        NOT NULL
                                      REFERENCES candace_cron_jobs (job_name)
                                      ON DELETE RESTRICT,
    scheduled_at         TIMESTAMPTZ NOT NULL,
    status               TEXT        NOT NULL
                                      CHECK (status IN ('running', 'succeeded', 'failed', 'canceled', 'skipped')),
    attempt              INTEGER     NOT NULL DEFAULT 0
                                      CHECK (attempt >= 0),
    worker_id            TEXT,
    lease_token          TEXT,
    lease_until          TIMESTAMPTZ,
    started_at           TIMESTAMPTZ,
    finished_at          TIMESTAMPTZ,
    error_summary        TEXT,
    skip_reason          TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (job_name, scheduled_at),
    CHECK (error_summary IS NULL OR (octet_length(error_summary) <= 4096
        AND status IN ('failed', 'canceled'))),
    CHECK (skip_reason IS NULL OR (octet_length(skip_reason) BETWEEN 1 AND 4096)),
    CHECK (
        (status = 'running'
            AND worker_id IS NOT NULL AND octet_length(worker_id) BETWEEN 1 AND 128
            AND lease_token IS NOT NULL AND octet_length(lease_token) BETWEEN 1 AND 256
            AND lease_until IS NOT NULL AND started_at IS NOT NULL
            AND finished_at IS NULL AND attempt >= 1)
        OR
        (status IN ('succeeded', 'failed', 'canceled', 'skipped')
            AND worker_id IS NULL AND lease_token IS NULL AND lease_until IS NULL
            AND finished_at IS NOT NULL
            AND (status = 'skipped' OR (started_at IS NOT NULL AND attempt >= 1)))
    ),
    CHECK ((status = 'skipped') = (skip_reason IS NOT NULL))
);

CREATE INDEX candace_cron_runs_expired_lease_idx
    ON candace_cron_runs (lease_until, scheduled_at, occurrence_id)
    WHERE status = 'running';

CREATE INDEX candace_cron_runs_recent_idx
    ON candace_cron_runs (scheduled_at DESC, occurrence_id DESC);
