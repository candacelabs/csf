-- A bridge event and this receipt commit in the same transaction. If the
-- client loses the COMMIT result, retrying the source event observes the
-- receipt and does not allocate another domain or session-event sequence.
CREATE TABLE bridge_event_receipts (
    session_id UUID NOT NULL,
    event_id TEXT NOT NULL,
    projected_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (session_id, event_id)
);
